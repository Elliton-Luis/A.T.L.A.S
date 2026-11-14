package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"atlas/pkg/email"
	"atlas/pkg/nlu"
	"atlas/pkg/telegram"
)

type fakeInbox struct {
	calls int
	query email.Query
}

func (*fakeInbox) IsConfigured() bool { return true }
func (f *fakeInbox) Fetch(_ context.Context, query email.Query, _ int) ([]email.MessageSummary, error) {
	f.calls++
	f.query = query
	return []email.MessageSummary{{ID: "1", Subject: "Fatura", From: "João", Snippet: "Vence dia 20"}, {ID: "2", Subject: "Promoção", From: "Loja"}}, nil
}

func mockPersonalAPI(t *testing.T, answer string) *int {
	t.Helper()
	previous := http.DefaultTransport
	calls := new(int)
	http.DefaultTransport = auditTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"ok":true}`
		if r.URL.Host != "api.telegram.org" {
			*calls++
			encoded, err := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"finish_reason": "stop", "message": map[string]string{"content": answer}}}})
			if err != nil {
				t.Error(err)
			}
			body = string(encoded)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	return calls
}

func TestEmailAndSummaryComposeWithoutRefetch(t *testing.T) {
	s := automationServer(t)
	inbox := &fakeInbox{}
	s.emailReader = inbox
	s.nluClient = nlu.NewClient("test", "", "", 450, 350, 500)
	calls := mockPersonalAPI(t, `{"resumo":"Uma fatura exige atenção; a promoção não é prioridade.","relevantes":["1"]}`)
	result := s.executeSequence(context.Background(), []nlu.ActionStep{{Acao: "ler_emails", Termo: "emails do João de hoje"}, {Acao: "resumir"}})
	if inbox.calls != 1 || *calls != 1 || inbox.query.From != "João" {
		t.Fatalf("trabalho duplicado ou filtro perdido: inbox=%d, IA=%d, query=%+v", inbox.calls, *calls, inbox.query)
	}
	if !strings.Contains(result, "E-mails relevantes") || !strings.Contains(result, "Resumo") || !strings.Contains(result, "Uma fatura exige atenção") {
		t.Fatal(result)
	}
	if command, ok := matchFastPath("veja meus emails e faça um resumo"); !ok || !strings.Contains(command, "faça um resumo") {
		t.Fatal("roteamento perdeu composição", command)
	}
}

func TestSequenceValidatesBeforeAnyRead(t *testing.T) {
	s := automationServer(t)
	inbox := &fakeInbox{}
	s.emailReader = inbox
	result := s.executeSequence(context.Background(), []nlu.ActionStep{{Acao: "ler_emails"}, {Acao: "limpar_agenda"}})
	if inbox.calls != 0 || !strings.Contains(result, "Nenhuma etapa executada") {
		t.Fatal(result)
	}
}

func TestNaturalBriefingConfigurationReachesPersistence(t *testing.T) {
	s := automationServer(t)
	s.nluClient = nlu.NewClient("test", "", "", 450, 350, 500)
	calls := mockPersonalAPI(t, `{"tipo":"acao","acao":"configurar_briefing","horario":"07:00","acoes":[{"acao":"liturgia"},{"acao":"santo_do_dia"},{"acao":"versiculo_biblico","termo":"Provérbios 3:5-6","titulo":"Confiança"},{"acao":"pensamento","termo":"Focar no essencial"},{"acao":"listar_agenda"},{"acao":"noticias_resumo","termo":"tecnologia"},{"acao":"listar_tarefas"}]}`)
	s.processIncomingMessage(context.Background(), &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "Quero meu briefing todo dia às 7h com liturgia, santo do dia, versículo sobre confiança, meu pensamento Focar no essencial, agenda, notícias de tecnologia e checklist"})
	items, err := s.briefingItems(context.Background())
	if err != nil || len(items) != 7 || items[3].Term != "Focar no essencial" || items[5].Term != "tecnologia" || *calls != 1 {
		t.Fatalf("configuração não persistiu: %+v (%v), chamadas=%d", items, err, *calls)
	}
}

func TestBriefingAdditionRemovalAndValidation(t *testing.T) {
	s := automationServer(t)
	ctx := context.Background()
	result := s.configureBriefing(ctx, &nlu.IntentResult{Acao: "configurar_briefing", Acoes: []nlu.ActionStep{{Acao: "pensamento", Termo: "Foco"}, {Acao: "listar_agenda"}}})
	if !strings.HasPrefix(result, "✅") {
		t.Fatal(result)
	}
	s.configureBriefing(ctx, &nlu.IntentResult{Acao: "adicionar_briefing", Acoes: []nlu.ActionStep{{Acao: "noticias_resumo", Termo: "economia"}, {Acao: "pensamento", Termo: "Foco"}}})
	items, _ := s.briefingItems(ctx)
	if len(items) != 3 || items[0].Term != "Foco" {
		t.Fatal("adição substituiu itens ou duplicou pensamento", items)
	}
	s.configureBriefing(ctx, &nlu.IntentResult{Acao: "alterar_briefing", ID: 3, Acoes: []nlu.ActionStep{{Acao: "noticias_resumo", Termo: "tecnologia"}}})
	items, _ = s.briefingItems(ctx)
	if len(items) != 3 || items[2].Term != "tecnologia" || items[0].Term != "Foco" {
		t.Fatal("alteração perdeu ordem ou outros itens", items)
	}
	s.configureBriefing(ctx, &nlu.IntentResult{Acao: "remover_briefing", Termo: "tecnologia"})
	items, _ = s.briefingItems(ctx)
	if len(items) != 2 {
		t.Fatal(items)
	}
	result = s.configureBriefing(ctx, &nlu.IntentResult{Acao: "configurar_briefing", Acoes: []nlu.ActionStep{{Acao: "briefing"}}})
	items, _ = s.briefingItems(ctx)
	if len(items) != 2 || !strings.HasPrefix(result, "⚠️") {
		t.Fatal("configuração recursiva destruiu itens", result)
	}
}

func TestSpokenChecklistCompletionAndDefaultBriefing(t *testing.T) {
	s := automationServer(t)
	ctx := context.Background()
	calls := mockPersonalAPI(t, `{}`)
	result := s.taskCommand(ctx, "/tarefa", "Estudar Go; Treinar")
	if !strings.Contains(result, "0/2") {
		t.Fatal(result)
	}
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "anote que fiz Estudar Go"})
	tasks, err := s.db.DailyTasks(ctx, s.today())
	if err != nil || len(tasks) != 2 || !tasks[0].Done || tasks[1].Done || *calls != 0 {
		t.Fatalf("checklist incorreto: %+v (%v), IA=%d", tasks, err, *calls)
	}
	briefing, err := s.engine.BuildBriefing(ctx, s.today())
	if err != nil || !strings.Contains(briefing, "☑ Estudar Go") || !strings.Contains(briefing, "☐ Treinar") {
		t.Fatal(briefing, err)
	}
	if command, ok := matchFastPath("minhas metas diárias"); !ok || command != "/tarefas" {
		t.Fatal(command)
	}
}

func TestSpokenTaskParaphraseFallsBackToRealChecklistContext(t *testing.T) {
	s := automationServer(t)
	ctx := context.Background()
	if err := s.db.AddDailyTasks(ctx, s.today(), []string{"Treinar"}); err != nil {
		t.Fatal(err)
	}
	tasks, _ := s.db.DailyTasks(ctx, s.today())
	answer, err := json.Marshal(nlu.IntentResult{Tipo: "acao", Acao: "concluir_tarefa", ID: tasks[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	s.nluClient = nlu.NewClient("test", "", "", 450, 350, 500)
	calls := mockPersonalAPI(t, string(answer))
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "anote que fiz o treino"})
	tasks, _ = s.db.DailyTasks(ctx, s.today())
	if !tasks[0].Done || *calls != 1 {
		t.Fatal("paráfrase não passou pela NLU com ID real", tasks, *calls)
	}
}
