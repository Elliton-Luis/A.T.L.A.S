package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"atlas/pkg/nlu"
	"atlas/pkg/scheduler"
)

func TestScheduledCategoriesAreSeparateAndCannotCrossEdit(t *testing.T) {
	s := automationServer(t)
	ctx := context.Background()
	result := s.createReminder(ctx, &nlu.IntentResult{Mensagem: "liturgia", Horario: "07:00"})
	if !strings.HasPrefix(result, "✅") {
		t.Fatal(result)
	}
	routines, _ := s.db.GetAllRoutines(ctx)
	var reminderID, automationID int64
	for _, r := range routines {
		if scheduler.RoutineKind(r) == "lembrete" {
			reminderID = r.ID
		} else {
			automationID = r.ID
		}
	}
	if reminderID == 0 || automationID == 0 {
		t.Fatal("categorias não identificadas")
	}
	result = s.executeAppAction(ctx, &nlu.IntentResult{Acao: "apagar_evento", Termo: "liturgia"})
	still, _ := s.db.GetAllRoutines(ctx)
	if len(still) != len(routines) {
		t.Fatal("exclusão de compromisso atingiu lembrete", result)
	}
	for text, want := range map[string]string{"minhas tarefas hoje": "/tarefas", "meus lembretes hoje": "/lembretes", "minhas automações": "/automacoes", "minha rotina": "/dia"} {
		command, ok := matchFastPath(text)
		if !ok || command != want {
			t.Errorf("conceito misturado: %s => %s", text, command)
		}
	}
	if strings.Contains(s.scheduledList(ctx, "lembrete"), "briefing_matinal") || strings.Contains(s.scheduledList(ctx, "automacao"), fmt.Sprintf("[%d]", reminderID)) {
		t.Fatal("listagens misturadas")
	}
	result = s.scheduledCommand(ctx, "lembrete", fmt.Sprintf("%d excluir", automationID))
	if !strings.Contains(result, "não lembrete") {
		t.Fatal(result)
	}
	after, _ := s.db.GetAllRoutines(ctx)
	if len(after) != len(routines) {
		t.Fatal("comando de categoria errada excluiu item")
	}
	ids, _ := s.scheduledIDs(ctx, "lembrete")
	payload, _ := json.Marshal(ids)
	s.createReminder(ctx, &nlu.IntentResult{Mensagem: "Novo lembrete posterior à confirmação", Horario: "08:00"})
	result = s.clearScheduledConfirmation(ctx, "lembrete", string(payload))
	if !strings.Contains(result, "1 item") {
		t.Fatal(result)
	}
	remaining, _ := s.db.GetAllRoutines(ctx)
	if len(remaining) != len(routines) {
		t.Fatal("limpeza excluiu automação ou lembrete criado depois", remaining)
	}
}
