package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPersonalBriefingKeepsOrderAndCachesOnlySuccessfulSources(t *testing.T) {
	e, _ := routineEngine(t)
	ctx := context.Background()
	date := time.Now().UTC().Format("2006-01-02")
	body := `[{"acao":"liturgia","titulo":"Liturgia"},{"acao":"pensamento","titulo":"Meu pensamento","termo":"Focar no essencial"},{"acao":"noticias_resumo","titulo":"Notícias","termo":"tecnologia"}]`
	if err := e.db.SaveBriefingConfig(ctx, body, ""); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	e.ExecuteAction = func(_ context.Context, action ActionRoutine) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[action.Action]++
		if action.Action == "noticias_resumo" && calls[action.Action] == 1 {
			return "", fmt.Errorf("RSS indisponível")
		}
		if action.Action == "liturgia" {
			return "Leituras reais", nil
		}
		return "Manchetes recentes", nil
	}
	first, err := e.BuildBriefing(ctx, date)
	if err != nil || !strings.Contains(first, "Leituras reais") || !strings.Contains(first, "Focar no essencial") || !strings.Contains(first, "Fonte indisponível") {
		t.Fatal(first, err)
	}
	if strings.Index(first, "*Liturgia*") > strings.Index(first, "*Meu pensamento*") || strings.Index(first, "*Meu pensamento*") > strings.Index(first, "*Notícias*") {
		t.Fatal("ordem perdida", first)
	}
	second, err := e.BuildBriefing(ctx, date)
	if err != nil || !strings.Contains(second, "Manchetes recentes") || calls["liturgia"] != 1 || calls["noticias_resumo"] != 2 {
		t.Fatalf("cache incorreto: %v, %s (%v)", calls, second, err)
	}
}

func TestConfiguredAgendaAndChecklistRemainFresh(t *testing.T) {
	e, _ := routineEngine(t)
	ctx := context.Background()
	date := time.Now().UTC().Format("2006-01-02")
	if err := e.db.SaveBriefingConfig(ctx, `[{"acao":"agenda_completa","titulo":"Planejamento"}]`, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.db.AddDailyTasks(ctx, date, []string{"Estudar"}); err != nil {
		t.Fatal(err)
	}
	first, err := e.BuildBriefing(ctx, date)
	if err != nil || !strings.Contains(first, "☐ Estudar") {
		t.Fatal(first, err)
	}
	if _, err := e.db.CompleteDailyTask(ctx, date, 0, "Estudar"); err != nil {
		t.Fatal(err)
	}
	second, err := e.BuildBriefing(ctx, date)
	if err != nil || !strings.Contains(second, "☑ Estudar") {
		t.Fatal("checklist ficou preso em cache", second, err)
	}
}
