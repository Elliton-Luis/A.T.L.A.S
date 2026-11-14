package scheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"atlas/pkg/config"
	"atlas/pkg/database"
)

type recordingSender struct {
	mu       sync.Mutex
	messages []string
	err      error
}

func (s *recordingSender) SendMessage(_ context.Context, _ int64, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return s.err
}
func (*recordingSender) SendDocument(context.Context, int64, string, string) error { return nil }

func routineEngine(t *testing.T) (*Engine, *recordingSender) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "atlas.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.ClearAllRoutines(context.Background()); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{}
	return NewEngine(&config.Config{Location: time.UTC, TelegramChatID: 1}, db, sender, nil), sender
}

func TestConcurrentTicksDeliverActionOnceAndRecoverMissedMinute(t *testing.T) {
	e, sender := routineEngine(t)
	ctx := context.Background()
	if _, err := e.db.SaveActionRoutine(ctx, "Liturgia", "07:00", `{"acao":"liturgia"}`); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := 0
	executor := func(_ context.Context, action ActionRoutine) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		if action.Action != "liturgia" {
			return "", fmt.Errorf("ação errada: %s", action.Action)
		}
		return "Evangelho atualizado do dia", nil
	}
	e.ExecuteAction = executor
	other := NewEngine(e.cfg, e.db, sender, nil)
	other.ExecuteAction = executor
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			engine := e
			if i%2 == 0 {
				engine = other
			}
			engine.processProactiveRoutines(ctx, "2026-11-14", "07:03")
		}(i)
	}
	wg.Wait()
	if calls != 1 || len(sender.messages) != 1 || sender.messages[0] != "Evangelho atualizado do dia" {
		t.Fatalf("execuções=%d, entregas=%v", calls, sender.messages)
	}
	e.processProactiveRoutines(ctx, "2026-11-15", "07:00")
	if calls != 2 || len(sender.messages) != 2 {
		t.Fatal("recorrência não executou no dia seguinte")
	}
}

func TestRoutineWeekdaysAndStaleWindow(t *testing.T) {
	e, sender := routineEngine(t)
	ctx := context.Background()
	e.ExecuteAction = func(context.Context, ActionRoutine) (string, error) { return "conteúdo", nil }
	if _, err := e.db.SaveActionRoutine(ctx, "Liturgia", "07:00", `{"acao":"liturgia","dias_semana":[1,2,3,4,5]}`); err != nil {
		t.Fatal(err)
	}
	e.processProactiveRoutines(ctx, "2026-11-14", "07:00") // Saturday
	e.processProactiveRoutines(ctx, "2026-11-16", "06:59")
	e.processProactiveRoutines(ctx, "2026-11-16", "09:01")
	if len(sender.messages) != 0 {
		t.Fatal("disparo fora do dia/janela")
	}
	e.processProactiveRoutines(ctx, "2026-11-16", "07:05")
	if len(sender.messages) != 1 {
		t.Fatal("dia útil não executou")
	}
}

func TestBriefingUsesFactsAndDeduplicatesLegacySchedules(t *testing.T) {
	e, sender := routineEngine(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if _, err := e.db.InsertRoutine(ctx, fmt.Sprintf("briefing%d", i), "08:00", "briefing", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.db.InsertEvento(ctx, "Dentista", "2026-11-14 10:00:00", "2026-11-14 09:00:00", "2026-11-14 09:30:00"); err != nil {
		t.Fatal(err)
	}
	deadline := "2026-11-20"
	if _, err := e.db.InsertObjetivo(ctx, "Entregar projeto", &deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.InsertRoutine(ctx, "Tomar remédio", "12:00", "custom", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.InsertConta(ctx, "Internet", 14, nil, 100, 1, "outros"); err != nil {
		t.Fatal(err)
	}
	e.processProactiveRoutines(ctx, "2026-11-14", "08:00")
	if len(sender.messages) != 1 {
		t.Fatalf("briefing repetido %d vezes", len(sender.messages))
	}
	for _, fact := range []string{"Dentista", "10:00", "Entregar projeto", "2026-11-20", "Tomar remédio", "Internet", "100.00"} {
		if !strings.Contains(sender.messages[0], fact) {
			t.Errorf("fato ausente: %s", fact)
		}
	}
}

func TestRoutineRetriesGenerationButNotAmbiguousDelivery(t *testing.T) {
	e, sender := routineEngine(t)
	ctx := context.Background()
	id, err := e.db.SaveActionRoutine(ctx, "Liturgia", "07:00", `{"acao":"liturgia"}`)
	if err != nil {
		t.Fatal(err)
	}
	e.ExecuteAction = func(context.Context, ActionRoutine) (string, error) { return "", fmt.Errorf("fonte indisponível") }
	e.processProactiveRoutines(ctx, "2026-11-14", "07:00")
	if len(sender.messages) != 0 {
		t.Fatal("falha virou alerta textual de sucesso")
	}
	e.ExecuteAction = func(context.Context, ActionRoutine) (string, error) { return "conteúdo", nil }
	key := fmt.Sprintf("rotina:%d:2026-11-14", id)
	if _, err := e.db.ExecContext(ctx, `UPDATE execucoes SET criado_em = datetime('now', '-6 minutes') WHERE chave = ?`, key); err != nil {
		t.Fatal(err)
	}
	sender.err = fmt.Errorf("timeout após envio")
	e.processProactiveRoutines(ctx, "2026-11-14", "07:06")
	if _, err := e.db.ExecContext(ctx, `UPDATE execucoes SET criado_em = datetime('now', '-6 minutes') WHERE chave = ?`, key); err != nil {
		t.Fatal(err)
	}
	e.processProactiveRoutines(ctx, "2026-11-14", "07:12")
	if len(sender.messages) != 1 {
		t.Fatal("entrega ambígua foi repetida")
	}
}
