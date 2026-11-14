package scheduler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRecurringEndFollowUpWaitsAndSurvivesMidnight(t *testing.T) {
	e, sender := routineEngine(t)
	ctx := context.Background()
	friday := 5
	duration := 180
	id, err := e.db.InsertEventoCompleto(ctx, "Plantão", "", "23:00", "23:00", "semanal", &friday, nil, "23:00", 0, 0, &duration)
	if err != nil {
		t.Fatal(err)
	}
	e.processEventBuffers(ctx, time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC), "2026-11-14 01:00:00")
	if len(sender.messages) != 0 {
		t.Fatal("perguntou antes de acabar", sender.messages)
	}
	e.processEventBuffers(ctx, time.Date(2026, 11, 14, 2, 15, 0, 0, time.UTC), "2026-11-14 02:15:00")
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "2026-11-13") || !strings.Contains(sender.messages[0], "02:00") {
		t.Fatal("fim/ocorrência incorretos", sender.messages)
	}
	event, _ := e.db.GetEventoByID(ctx, id)
	if event.UltimaConclusaoData == nil || *event.UltimaConclusaoData != "2026-11-13" {
		t.Fatal("marcou dia errado", event)
	}
	e.processEventBuffers(ctx, time.Date(2026, 11, 14, 2, 16, 0, 0, time.UTC), "2026-11-14 02:16:00")
	if len(sender.messages) != 1 {
		t.Fatal("follow-up repetido")
	}
}

func TestLiteralReminderNeverBecomesLiturgyAutomation(t *testing.T) {
	e, sender := routineEngine(t)
	ctx := context.Background()
	reminder, _ := json.Marshal(Reminder{Message: "liturgia", Days: []int{1}})
	payload := string(reminder)
	if _, err := e.db.InsertRoutine(ctx, "Liturgia das Horas", "07:00", "lembrete", &payload); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e.ExecuteAction = func(context.Context, ActionRoutine) (string, error) {
		calls++
		return "conteúdo que não deve ser buscado", nil
	}
	e.processProactiveRoutines(ctx, "2026-11-14", "07:00")
	if len(sender.messages) != 0 {
		t.Fatal("lembrete semanal disparou no sábado")
	}
	e.processProactiveRoutines(ctx, "2026-11-16", "07:00")
	if len(sender.messages) != 1 || sender.messages[0] != "liturgia" || calls != 0 {
		t.Fatal("lembrete executou ferramenta", sender.messages, calls)
	}
}
