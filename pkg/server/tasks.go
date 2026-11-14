package server

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
	"atlas/pkg/synonyms"
)

var completedTask = regexp.MustCompile(`(?i)^(?:(?:anote|anota|registre|registra)\s+)?(?:que\s+)?(?:eu\s+)?(?:fiz|conclu[ií]|terminei|finalizei)\s+(.+?)[.!?]*$`)

func checklistFastPath(text string) (string, bool) {
	clean := synonyms.RemoveAccents(strings.ToLower(strings.TrimSpace(text)))
	tokens := synonyms.TokenizeAndCanonicalize(clean)
	if match := completedTask.FindStringSubmatch(strings.TrimSpace(text)); len(match) > 1 && (strings.HasPrefix(clean, "anote que fiz ") || strings.HasPrefix(clean, "anota que fiz ") || synonyms.MatchCanonical(tokens, "tarefa")) {
		term := strings.TrimSpace(match[1])
		for _, prefix := range []string{"a tarefa ", "o task ", "tarefa ", "task "} {
			term = strings.TrimPrefix(term, prefix)
		}
		return "/feito " + term, true
	}
	if synonyms.MatchCanonical(tokens, "apagar") || synonyms.MatchCanonical(tokens, "planejar") {
		return "", false
	}
	for _, verb := range []string{"anote", "anotar", "crie", "criar", "adicione", "adicionar", "registre", "registrar", "marque", "marcar", "mude", "mudar", "altere", "alterar", "defina", "definir"} {
		if synonyms.MatchCanonical(tokens, synonyms.CanonicalizeWord(verb)) {
			return "", false
		}
	}
	if synonyms.MatchCanonical(tokens, "tarefa") && (len(tokens) <= 4 || synonyms.MatchCanonical(tokens, "ver")) {
		if strings.Contains(clean, "ontem") {
			return "/tarefas ontem", true
		}
		if strings.Contains(clean, "amanha") {
			return "/tarefas amanha", true
		}
		return "/tarefas", true
	}
	if synonyms.MatchCanonical(tokens, "tarefa") && synonyms.MatchCanonical(tokens, "ver") {
		return "/tarefas", true
	}
	if synonyms.MatchCanonical(tokens, "meta") && (strings.Contains(clean, "diari") || strings.Contains(clean, "hoje") || strings.Contains(clean, "do dia")) && (len(tokens) <= 4 || synonyms.MatchCanonical(tokens, "ver")) {
		return "/tarefas", true
	}
	if clean == "o que fiz hoje" || clean == "o que eu fiz hoje" || clean == "tarefas" || clean == "minhas tarefas" || clean == "metas diarias" || clean == "checklist" || clean == "check list" || clean == "check-list" {
		return "/tarefas", true
	}
	if clean == "o que fiz ontem" || clean == "o que eu fiz ontem" || clean == "tarefas de ontem" {
		return "/tarefas ontem", true
	}
	return "", false
}

func (s *Server) taskDate(text string) (string, error) {
	text = synonyms.RemoveAccents(strings.ToLower(strings.TrimSpace(text)))
	now := time.Now().In(s.cfg.Location)
	switch text {
	case "", "hoje":
		return now.Format("2006-01-02"), nil
	case "ontem":
		return now.AddDate(0, 0, -1).Format("2006-01-02"), nil
	case "amanha":
		return now.AddDate(0, 0, 1).Format("2006-01-02"), nil
	}
	day, err := time.ParseInLocation("2006-01-02", text, s.cfg.Location)
	if err != nil {
		return "", fmt.Errorf("use hoje, ontem ou uma data como 2026-11-14")
	}
	return day.Format("2006-01-02"), nil
}

func (s *Server) taskList(ctx context.Context, date string) string {
	tasks, err := s.db.DailyTasks(ctx, date)
	if err != nil {
		return "⚠️ Não foi possível consultar o checklist."
	}
	var out strings.Builder
	fmt.Fprintf(&out, "*Checklist diário — %s*\n", date)
	if len(tasks) == 0 {
		out.WriteString("Nenhuma tarefa anotada para esse dia.\n")
	}
	done := 0
	for _, task := range tasks {
		mark := "☐"
		if task.Done {
			mark = "☑"
			done++
		}
		fmt.Fprintf(&out, "%s [%d] %s\n", mark, task.ID, task.Description)
		if task.Minutes > 0 {
			fmt.Fprintf(&out, "   %d min | %s | Concluir: %s\n", task.Minutes, task.Difficulty, task.Completion)
			if task.Resource != "" {
				fmt.Fprintf(&out, "   %s\n", task.Resource)
			}
		}
	}
	fmt.Fprintf(&out, "\n%d/%d concluídas. Histórico preservado por data.", done, len(tasks))
	return out.String()
}

func (s *Server) taskAction(ctx context.Context, intent *nlu.IntentResult) string {
	date, err := s.taskDate(intent.Data)
	if err != nil {
		return "⚠️ " + err.Error()
	}
	switch intent.Acao {
	case "listar_tarefas":
		return s.taskList(ctx, date)
	case "anotar_tarefas":
		if len(intent.Tarefas) == 0 && strings.TrimSpace(intent.Termo) != "" {
			intent.Tarefas = []string{intent.Termo}
		}
		if err := s.db.AddDailyTasks(ctx, date, intent.Tarefas); err != nil {
			return "⚠️ Não foi possível anotar tarefas: " + err.Error()
		}
		return "✅ Tarefas anotadas.\n\n" + s.taskList(ctx, date)
	case "concluir_tarefa":
		// Spoken completion always refers to today's checklist, unless a date was explicit.
		result, err := s.completeTask(ctx, date, intent.ID, intent.Termo)
		if err != nil {
			return "⚠️ " + err.Error()
		}
		return result
	}
	return "⚠️ Ação de checklist desconhecida."
}

func (s *Server) completeTask(ctx context.Context, date string, id int64, term string) (string, error) {
	task, err := s.db.CompleteDailyTask(ctx, date, id, term)
	if err != nil {
		return "", err
	}
	return "✅ Concluída: " + task.Description + "\n\n" + s.taskList(ctx, date), nil
}

func (s *Server) spokenTaskCompletion(ctx context.Context, term string) (string, bool) {
	result, err := s.completeTask(ctx, s.today(), 0, term)
	// Let NLU map paraphrases such as "o treino" to "Treinar" using real IDs.
	if errors.Is(err, database.ErrTaskNotFound) {
		return "", false
	}
	if err != nil {
		return "⚠️ " + err.Error(), true
	}
	return result, true
}

func (s *Server) taskCommand(ctx context.Context, command, argument string) string {
	switch command {
	case "/tarefas", "/checklist", "/metas_hoje":
		return s.taskAction(ctx, &nlu.IntentResult{Acao: "listar_tarefas", Data: argument})
	case "/tarefa":
		items := strings.FieldsFunc(argument, func(r rune) bool { return r == ';' || r == '\n' })
		return s.taskAction(ctx, &nlu.IntentResult{Acao: "anotar_tarefas", Tarefas: items})
	case "/feito":
		intent := &nlu.IntentResult{Acao: "concluir_tarefa", Termo: argument}
		if id, err := strconv.ParseInt(argument, 10, 64); err == nil {
			intent.ID = id
		}
		return s.taskAction(ctx, intent)
	}
	return "⚠️ Comando de checklist desconhecido."
}
