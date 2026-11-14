package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"atlas/pkg/config"
	"atlas/pkg/nlu"
	"atlas/pkg/scheduler"
)

func automationServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(&config.Config{DBPath: filepath.Join(t.TempDir(), "atlas.db"), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestScheduleActionLifecycle(t *testing.T) {
	s := automationServer(t)
	ctx := context.Background()
	for _, text := range []string{"me mande a liturgia todo dia às 7:00", "envie o evangelho diariamente às 07h"} {
		reply, handled := s.handleAutomation(ctx, text)
		if !handled || !strings.Contains(reply, "✅") {
			t.Fatalf("%s: %s (%v)", text, reply, handled)
		}
	}
	routines, err := s.db.GetAllRoutines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, r := range routines {
		if r.Tipo != "action" {
			continue
		}
		count++
		var a scheduler.ActionRoutine
		if r.Horario != "07:00" || r.PromptCustom == nil || json.Unmarshal([]byte(*r.PromptCustom), &a) != nil || a.Action != "liturgia" {
			t.Fatalf("ação virou lembrete textual: %+v", r)
		}
	}
	if count != 1 {
		t.Fatalf("programação repetida criou %d ações", count)
	}
	if reply, handled := s.handleAutomation(ctx, "mude a liturgia para 8h"); !handled || !strings.Contains(reply, "08:00") {
		t.Fatalf("alteração: %s", reply)
	}
	if reply, handled := s.handleAutomation(ctx, "cancele o envio diário da liturgia"); !handled || !strings.Contains(reply, "cancelado") {
		t.Fatalf("cancelamento: %s", reply)
	}
	routines, _ = s.db.GetAllRoutines(ctx)
	for _, r := range routines {
		if r.Tipo == "action" {
			t.Fatal("ação cancelada continua cadastrada")
		}
	}
}

func TestBriefingScheduleReplacesDefault(t *testing.T) {
	s := automationServer(t)
	reply := s.saveScheduledAction(context.Background(), &nlu.IntentResult{Acao: "briefing", Horario: "07:00", Frequencia: "semanal", DiasSemana: []int{1, 2, 3, 4, 5}})
	if !strings.Contains(reply, "✅") {
		t.Fatal(reply)
	}
	routines, _ := s.db.GetActiveRoutines(context.Background())
	for _, r := range routines {
		if r.Tipo == "briefing" {
			t.Fatal("briefing padrão ainda dispara nos dias excluídos")
		}
	}
}

func TestFastPathPreservesOperations(t *testing.T) {
	for _, input := range []string{"mande a liturgia todo dia às 7h", "cancele a liturgia", "anote que hoje preciso estudar", "concluí o treino de hoje", "mude meus emails para 8h", "envie a liturgia toda terça às 7h", "envie a liturgia todas as terças", "me mande a liturgia amanhã às 7h", "não apague tudo"} {
		if cmd, ok := matchFastPath(input); ok {
			t.Errorf("%q perdeu intenção para %q", input, cmd)
		}
	}
	if cmd, ok := matchFastPath("leia os emails do João de ontem"); !ok || cmd != "/emails leia os emails do João de ontem" {
		t.Fatalf("filtros perdidos: %s", cmd)
	}
	if cmd, ok := matchFastPath("py print('todo dia')"); !ok || cmd != "/py print('todo dia')" {
		t.Fatalf("comando explícito interceptado: %s", cmd)
	}
}

type auditTransport func(*http.Request) (*http.Response, error)

func (f auditTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestScheduledLiturgyFetchesContent(t *testing.T) {
	s := automationServer(t)
	previous := http.DefaultTransport
	http.DefaultTransport = auditTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "liturgia.up.railway.app" {
			t.Fatalf("requisição inesperada: %s", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":"14/11/2026","primeiraLeitura":{"referencia":"3Jo 5-8","texto":"Leitura real"},"segundaLeitura":{"referencia":"Hb 1","texto":"Segunda leitura real"},"evangelho":{"referencia":"Lc 18","texto":"Evangelho real"}}`)), Header: make(http.Header)}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	text, err := s.engine.ExecuteAction(context.Background(), scheduler.ActionRoutine{Action: "liturgia"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Leitura real", "Segunda leitura real", "Evangelho real"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("conteúdo ausente: %s", text)
		}
	}
}

func TestUnknownActionNeverClaimsSuccess(t *testing.T) {
	s := automationServer(t)
	if text := s.executeAppAction(context.Background(), &nlu.IntentResult{Acao: "inexistente"}); !strings.Contains(text, "não reconhecida") {
		t.Fatal(text)
	}
}

func TestWebhookDeduplicatesUpdatesBeforeSideEffects(t *testing.T) {
	s := automationServer(t)
	previous := http.DefaultTransport
	http.DefaultTransport = auditTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	for i := 0; i < 2; i++ {
		request := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{"update_id":42,"message":{"message_id":7,"chat":{"id":1},"text":"/conta Internet dia 14 100"}}`))
		response := httptest.NewRecorder()
		s.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatal(response.Code)
		}
	}
	bills, err := s.db.GetAllContas(context.Background())
	if err != nil || len(bills) != 1 {
		t.Fatalf("update repetiu cadastro: %+v (%v)", bills, err)
	}
}
