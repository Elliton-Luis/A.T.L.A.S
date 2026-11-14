package server

import (
	"context"
	"strings"
	"testing"

	"atlas/pkg/nlu"
	"atlas/pkg/telegram"
)

const reportedLatinRequest = "Eu penso em estudar latim. Eu queria pensar em formas eficientes de estudar latim sem perder muito tempo, já que eu só tenho uma hora livre por dia, aproximadamente."

func TestReportedLatinStudyUsesOnePlanningCallAndSixtyMinutes(t *testing.T) {
	s := automationServer(t)
	s.nluClient = nlu.NewClient("test", "", "", 450, 350, 500)
	calls := mockPersonalAPI(t, `{"dificuldade":"moderada","passos":[{"acao":"Leia um parágrafo curto em latim","minutos":15,"conclusao":"5 palavras anotadas","gratuito":true,"pratico":true},{"acao":"Escreva cinco frases usando as palavras","minutos":25,"conclusao":"5 frases escritas","gratuito":true,"pratico":true},{"acao":"Revise as frases com o ATLAS","minutos":20,"conclusao":"5 frases revisadas","gratuito":true,"pratico":true}]}`)
	s.processIncomingMessage(context.Background(), &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: reportedLatinRequest})
	plan, err := s.db.DailyPlan(context.Background(), s.today(), "estudar latim")
	if err != nil || plan == nil || plan.Minutes != 60 || len(plan.Steps) != 3 || *calls != 1 {
		t.Fatalf("pedido não gerou plano: %+v (%v), chamadas=%d", plan, err, *calls)
	}
	tasks, _ := s.db.DailyTasks(context.Background(), s.today())
	total := 0
	for _, task := range tasks {
		total += task.Minutes
	}
	if total != 60 {
		t.Fatal("tempo explícito perdido", total)
	}
}

func TestNaturalStudyRoutingPreservesQueriesAndMissingDuration(t *testing.T) {
	for _, text := range []string{"quero ler meus emails por uma hora", "quero um resumo do que estudei", "o que significa estudar latim?", "quero ver o briefing", "eu não quero estudar latim", "quero saber se vale a pena estudar latim por uma hora"} {
		if command, ok := planningFastPath(text); ok {
			t.Errorf("consulta virou plano: %s => %s", text, command)
		}
	}
	if command, ok := planningFastPath("Quero estudar latim por uma hora"); !ok || !strings.Contains(command, "60 min") {
		t.Fatal(command)
	}
	if command, ok := planningFastPath("Penso em estudar latim"); !ok || strings.Contains(command, "60 min") {
		t.Fatal("duração ausente foi inventada", command)
	}
}
