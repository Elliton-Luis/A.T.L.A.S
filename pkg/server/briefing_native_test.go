package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"atlas/pkg/nlu"
	"atlas/pkg/telegram"
)

const reportedBriefingRequest = "Eu quero criar um briefing de manhã, uma automação em que você vai me mandar a liturgia diária, o santo do dia, e vai dizer tudo o que eu tenho que fazer naquele dia, todos os meus compromissos, natação, aula, tudo o que eu tenho que fazer naquele dia, de manhã, todo dia, às sete horas da manhã."

func TestReportedSpokenBriefingWorksWithoutNLUProvider(t *testing.T) {
	s := automationServer(t)
	s.nluClient = nlu.NewClient("test", "", "", 450, 350, 500)
	aiCalls := 0
	var messages []string
	previous := http.DefaultTransport
	http.DefaultTransport = auditTransport(func(r *http.Request) (*http.Response, error) {
		status := 200
		body := `{"ok":true}`
		if r.URL.Host != "api.telegram.org" {
			aiCalls++
			status = 503
			body = `{"error":"provider unavailable"}`
		} else if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			messages = append(messages, payload.Text)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	s.processIncomingMessage(context.Background(), &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: reportedBriefingRequest})
	items, err := s.briefingItems(context.Background())
	if err != nil || len(items) != 3 || items[0].Action != "liturgia" || items[1].Action != "santo_do_dia" || items[2].Action != "agenda_completa" {
		t.Fatalf("pedido completo não configurou briefing: %+v (%v), mensagens=%v, chamadas IA=%d", items, err, messages, aiCalls)
	}
	if aiCalls != 0 {
		t.Fatalf("pedido conhecido dependeu da IA: %d chamadas", aiCalls)
	}
	routines, err := s.db.GetActiveRoutines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, routine := range routines {
		if routine.Tipo == "briefing" {
			t.Fatal("emissor antigo não foi substituído")
		}
		if routine.Nome == "Briefing matinal" {
			count++
			if routine.Horario != "07:00" {
				t.Fatal("horário falado perdido", routine.Horario)
			}
		}
	}
	if count != 1 {
		t.Fatal("esperada uma automação de briefing", count)
	}
	if len(messages) == 0 || !strings.Contains(messages[len(messages)-1], "07:00") {
		t.Fatal("não confirmou o horário", messages)
	}
}

func TestSpokenScheduleHoursAndClarificationPreserveComposition(t *testing.T) {
	for text, want := range map[string]string{"às sete horas da manhã": "07:00", "às sete e meia da manhã": "07:30", "às sete horas da noite": "19:00", "às 7h da tarde": "19:00", "às vinte e duas horas": "22:00", "para as oito e quinze": "08:15", "à meia-noite": "00:00", "às 25:00": ""} {
		if got := extractRoutineTime(text); got != want {
			t.Errorf("%s => %s, esperado %s", text, got, want)
		}
	}
	s := automationServer(t)
	ctx := context.Background()
	calls := mockPersonalAPI(t, `{}`)
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "Crie um briefing diário com liturgia e santo do dia, todo dia de manhã"})
	pending, err := s.db.GetPendingConfirmation(ctx, 1)
	if err != nil || pending.Tipo != "briefing_horario" {
		t.Fatal("não pediu horário", pending, err)
	}
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "sete horas da manhã"})
	items, _ := s.briefingItems(ctx)
	if len(items) != 2 || *calls != 0 {
		t.Fatal("composição foi perdida ou chamou IA", items, *calls)
	}
	if _, err := s.db.GetPendingConfirmation(ctx, 1); err == nil {
		t.Fatal("pergunta de horário continuou pendente")
	}
}

func TestSimpleBriefingDoesNotDropParameterizedSourcesOrExceptions(t *testing.T) {
	for _, text := range []string{"Configure meu briefing com liturgia e notícias sobre tecnologia às sete", "Configure meu briefing com liturgia e um pensamento: estudar com calma", "Não configure meu briefing com liturgia", "Crie briefing com santo do dia toda semana às sete", "Adicione emails do João no briefing"} {
		if intent, handled := parseSimpleBriefing(text); handled {
			t.Errorf("pedido complexo parcialmente interpretado: %s => %+v", text, intent)
		}
	}
	s := automationServer(t)
	ctx := context.Background()
	s.configureBriefing(ctx, &nlu.IntentResult{Acao: "configurar_briefing", Acoes: []nlu.ActionStep{{Acao: "pensamento", Termo: "Meu texto pessoal"}}})
	if reply, handled := s.handleSimpleBriefing(ctx, 1, "Adicione a liturgia diária ao meu briefing"); !handled || !strings.HasPrefix(reply, "✅") {
		t.Fatal(reply)
	}
	items, _ := s.briefingItems(ctx)
	if len(items) != 2 || items[0].Term != "Meu texto pessoal" || items[1].Action != "liturgia" {
		t.Fatal("adição substituiu conteúdo anterior", items)
	}
}
