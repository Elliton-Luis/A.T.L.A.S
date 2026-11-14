package server

import (
	"context"
	"strings"
	"testing"

	"atlas/pkg/nlu"
	"atlas/pkg/telegram"
)

const validDayPlan = `{"dificuldade":"facil","passos":[{"acao":"Leia uma história infantil curta","minutos":15,"conclusao":"5 palavras anotadas","gratuito":true,"pratico":true},{"acao":"Converse em inglês com o ATLAS","minutos":15,"conclusao":"5 frases trocadas","gratuito":true,"pratico":true}]}`

func TestPlanningWaitsForUserTimeThenCreatesActionableSteps(t *testing.T) {
	s := automationServer(t)
	s.nluClient = nlu.NewClient("test", "", "", 450, 350, 500)
	ctx := context.Background()
	calls := mockPersonalAPI(t, validDayPlan)
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "Meu plano é estudar inglês"})
	pending, err := s.db.GetPendingConfirmation(ctx, 1)
	if err != nil || pending.Tipo != "planejar_dia" || *calls != 0 {
		t.Fatal("tempo não foi solicitado antes da IA", pending, err, *calls)
	}
	tasks, _ := s.db.DailyTasks(ctx, s.today())
	if len(tasks) != 0 {
		t.Fatal("tarefas criadas sem duração")
	}
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "30 minutos"})
	tasks, err = s.db.DailyTasks(ctx, s.today())
	if err != nil || len(tasks) != 2 || tasks[0].Minutes+tasks[1].Minutes != 30 || *calls != 1 {
		t.Fatalf("planejamento incorreto: %+v (%v), IA=%d", tasks, err, *calls)
	}
	if _, err := s.db.GetPendingConfirmation(ctx, 1); err == nil {
		t.Fatal("espera de duração não foi encerrada")
	}
	result := s.planningCommand(ctx, 1, "estudar inglês por 30min")
	if *calls != 1 || !strings.Contains(result, "sem duplicar") {
		t.Fatal("plano repetido regenerado", result, *calls)
	}
}

func TestPlannerRejectsInvalidOutputAndIgnoresInventedNLUTime(t *testing.T) {
	t.Run("invalid steps", func(t *testing.T) {
		s := automationServer(t)
		s.nluClient = nlu.NewClient("test", "", "", 450, 350, 500)
		mockPersonalAPI(t, strings.Replace(validDayPlan, `"gratuito":true`, `"gratuito":false`, 1))
		result := s.planningCommand(context.Background(), 1, "ler inglês por 30 min")
		tasks, _ := s.db.DailyTasks(context.Background(), s.today())
		if len(tasks) != 0 || !strings.Contains(result, "Nenhuma tarefa") {
			t.Fatal(result, tasks)
		}
	})
	t.Run("invented duration", func(t *testing.T) {
		s := automationServer(t)
		s.nluClient = nlu.NewClient("test", "", "", 450, 350, 500)
		calls := mockPersonalAPI(t, `{"tipo":"acao","acao":"planejar_dia","termo":"Estudar inglês","duracao_min":30}`)
		s.processIncomingMessage(context.Background(), &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "Torne o estudo de inglês prático"})
		tasks, _ := s.db.DailyTasks(context.Background(), s.today())
		if len(tasks) != 0 || *calls != 1 {
			t.Fatal("duração do modelo foi usada sem o usuário definir", tasks, *calls)
		}
		if _, err := s.db.GetPendingConfirmation(context.Background(), 1); err != nil {
			t.Fatal("duração não solicitada", err)
		}
	})
}

func TestHelpIsAnIndexAndSectorsAreSeparate(t *testing.T) {
	index := sectorHelp("")
	if len(strings.Split(index, "\n")) > 10 {
		t.Fatal("índice voltou a listar tudo")
	}
	for _, leaf := range []string{"/sonar", "/liturgia", "/conta ", "/tarefa "} {
		if strings.Contains(index, leaf) {
			t.Error("comando detalhado na raiz", leaf)
		}
	}
	if !strings.Contains(sectorHelp("organizar"), "/planejar") || !strings.Contains(sectorHelp("ferramentas"), "/sonar") {
		t.Fatal("setor não abre comandos locais")
	}
	if command, ok := matchFastPath("quais comandos de linux?"); !ok || command != "/ferramentas" {
		t.Fatal(command)
	}
	if command, ok := matchFastPath("ajuda com emails"); !ok || command != "/correio" {
		t.Fatal(command)
	}
	if command, ok := helpFastPath("quero criar um menu em Python"); ok {
		t.Fatal("criação virou navegação", command)
	}
}

func TestCancellingPlanningNeverFallsThroughToOtherActions(t *testing.T) {
	s := automationServer(t)
	s.nluClient = nlu.NewClient("test", "", "", 450, 350, 500)
	calls := mockPersonalAPI(t, validDayPlan)
	ctx := context.Background()
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "Planeje estudar inglês"})
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "Cancele!"})
	if _, err := s.db.GetPendingConfirmation(ctx, 1); err == nil {
		t.Fatal("planejamento permaneceu pendente")
	}
	if *calls != 0 {
		t.Fatal("cancelamento saiu para NLU", *calls)
	}
	routines, err := s.db.GetAllRoutines(ctx)
	if err != nil || len(routines) != 3 {
		t.Fatal("cancelamento alterou outra área", routines, err)
	}
}
