package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"atlas/pkg/nlu"
	"atlas/pkg/telegram"
)

func TestAgendaGroupsClassAndEditingFridayKeepsOtherLines(t *testing.T) {
	s := automationServer(t)
	ctx := context.Background()
	duration := 180
	ids := map[int]int64{}
	for _, day := range []int{1, 3, 4, 5} {
		value := day
		id, err := s.db.InsertEventoCompleto(ctx, "Aula", "", "18:00", "18:00", "semanal", &value, nil, "18:00", 0, 0, &duration)
		if err != nil {
			t.Fatal(err)
		}
		ids[day] = id
	}
	view := s.agendaView(ctx, "")
	if strings.Count(view, "*Aula*") != 1 || !strings.Contains(view, "Sexta-feira") || !strings.Contains(view, "Quinta-feira") {
		t.Fatal(view)
	}
	result := s.alterEvents(ctx, &nlu.IntentResult{Acao: "alterar_evento", ID: ids[1], Termo: "Aula", Horario: "08:30"}, "mude a Aula de sexta para 08:30")
	if !strings.HasPrefix(result, "✅") {
		t.Fatal(result)
	}
	for day, id := range ids {
		event, err := s.db.GetEventoByID(ctx, id)
		want := "18:00"
		if day == 5 {
			want = "08:30"
		}
		if err != nil || event.HorarioTime != want || *event.DiaSemana != day {
			t.Fatal("outra linha foi alterada", event, err)
		}
	}
	before, _ := s.db.ActiveEvents(ctx)
	result = s.alterEvents(ctx, &nlu.IntentResult{Acao: "alterar_evento", Termo: "Aula", Horario: "19:00"}, "")
	after, _ := s.db.ActiveEvents(ctx)
	if strings.HasPrefix(result, "✅") || len(before) != len(after) {
		t.Fatal("alteração ambígua aplicada", result)
	}
}

func TestEventCreationWaitsForDurationAndTakesUpperBound(t *testing.T) {
	s := automationServer(t)
	s.nluClient = nlu.NewClient("test", "", "", 450, 350, 500)
	calls := mockPersonalAPI(t, `{"tipo":"evento","descricao":"Aula","frequencia":"semanal","dias_semana":[1,3,4,5],"horario":"18:00","duracao_min":60,"estimativa_deslocamento_min":0,"estimativa_preparo_min":0}`)
	ctx := context.Background()
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "Marque Aula segunda, quarta, quinta e sexta às 18h, sem deslocamento e preparo"})
	before, _ := s.db.ActiveEvents(ctx)
	pending, err := s.db.GetPendingConfirmation(ctx, 1)
	if len(before) != 0 || err != nil || pending.Tipo != "duracao_evento" {
		t.Fatal("duração inventada ou cadastro antecipado", before, pending, err)
	}
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "2-3 horas"})
	after, err := s.db.ActiveEvents(ctx)
	if err != nil || len(after) != 4 || *calls != 1 {
		t.Fatal(after, err, *calls)
	}
	for _, event := range after {
		if event.DuracaoMin == nil || *event.DuracaoMin != 180 || event.PreparoMin != 0 || event.DeslocamentoMin != 0 {
			t.Fatal("intervalo ou zero explícito perdido", event)
		}
	}
	if command, ok := matchFastPath("veja todos os meus compromissos"); !ok || command != "/agenda" {
		t.Fatal(fmt.Sprintf("consulta total virou hoje: %s", command))
	}
}
