package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
	"atlas/pkg/planner"
	"atlas/pkg/synonyms"
)

var planningPrefix = regexp.MustCompile(`(?i)^(?:(?:quero|preciso|me ajude a)\s+)?(?:planejar|planeje|planeja|planejamento|meu plano(?: é| e)?)\s*(?:(?:o|meu)\s+dia\b)?\s*[:,-]?\s*`)
var planningTail = regexp.MustCompile(`(?i)(?:\s+(?:por|durante|em|com|para|hoje|e))+$`)
var planningTask = regexp.MustCompile(`(?i)^tarefa\s+([0-9]+)$`)
var naturalStudyGoal = regexp.MustCompile(`(?i)\b(?:estudar|aprender|praticar|treinar|revisar|ler)\s+[^.,;\n!?]+`)

type pendingPlan struct {
	Objective    string
	Minutes      int
	Date         string
	TaskID       int64
	ExplicitDate bool
}

func planningFastPath(text string) (string, bool) {
	if !planningPrefix.MatchString(strings.TrimSpace(text)) {
		clean := synonyms.RemoveAccents(strings.ToLower(text))
		if synonyms.MatchCanonical(synonyms.TokenizeAndCanonicalize(clean), "nao") {
			return "", false
		}
		for _, question := range []string{"quero saber", "o que significa", "vale a pena", "voce acha"} {
			if strings.Contains(clean, question) {
				return "", false
			}
		}
		for _, tool := range []string{"briefing", "email", "e-mail", "liturgia", "santo do dia", "noticias", "pdf", "agenda", "calendario", "resumo", "resuma", "sintese"} {
			if strings.Contains(clean, tool) {
				return "", false
			}
		}
		goalIntent := false
		for _, phrase := range []string{"quero", "queria", "pretendo", "penso em", "gostaria de", "me ajude a", "formas eficientes", "como estudar"} {
			if strings.Contains(clean, phrase) {
				goalIntent = true
				break
			}
		}
		if !goalIntent {
			return "", false
		}
		minutes, rest, _, err := planner.ParseDuration(text)
		if err != nil {
			return "", false
		}
		goal := strings.TrimSpace(naturalStudyGoal.FindString(rest))
		if goal == "" {
			return "", false
		}
		goal = strings.Trim(planningTail.ReplaceAllString(" "+goal, ""), " ,;:-")
		if minutes > 0 {
			return fmt.Sprintf("/planejar %s por %d min", goal, minutes), true
		}
		return "/planejar " + goal, true
	}
	if strings.Contains(synonyms.RemoveAccents(strings.ToLower(text)), "briefing") {
		return "", false
	}
	return "/planejar " + text, true
}

func (s *Server) planningCommand(ctx context.Context, chatID int64, text string) string {
	text = strings.TrimSpace(planningPrefix.ReplaceAllString(strings.TrimSpace(text), ""))
	if text == "" || text == "ajuda" {
		return "*Planejar o dia*\nDiga a atividade e quanto tempo tem. Exemplo: /planejar estudar inglês por 30 min.\nCrio poucos passos práticos e gratuitos, com duração, dificuldade e resultado verificável. Eles entram no checklist.\nPara uma tarefa existente: /planejar tarefa 12 por 30 min."
	}
	minutes, objective, _, err := planner.ParseDuration(text)
	if err != nil {
		return "⚠️ " + err.Error()
	}
	objective = strings.Trim(planningTail.ReplaceAllString(" "+objective, ""), " ,;:-")
	intent := &nlu.IntentResult{Acao: "planejar_dia", Termo: objective, DuracaoMin: minutes}
	if m := planningTask.FindStringSubmatch(objective); len(m) > 1 {
		intent.ID, _ = strconv.ParseInt(m[1], 10, 64)
	}
	return s.startDayPlan(ctx, chatID, intent)
}

func (s *Server) startDayPlan(ctx context.Context, chatID int64, intent *nlu.IntentResult) string {
	date, err := s.taskDate(intent.Data)
	if err != nil {
		return "⚠️ " + err.Error()
	}
	goal := strings.TrimSpace(intent.Termo)
	if goal == "" {
		goal = strings.TrimSpace(intent.Titulo)
	}
	if intent.ID > 0 {
		tasks, err := s.db.DailyTasks(ctx, date)
		if err != nil {
			return "⚠️ Não foi possível consultar a tarefa."
		}
		found := false
		for _, task := range tasks {
			if task.ID == intent.ID {
				if task.Minutes > 0 {
					return "Essa tarefa já é um passo de um plano. Veja os passos e tempos em /tarefas."
				}
				if task.Done {
					return "Essa tarefa já está concluída. Escolha outra atividade para planejar."
				}
				goal = task.Description
				found = true
				break
			}
		}
		if !found {
			return "⚠️ Tarefa não encontrada no checklist desse dia."
		}
	}
	request := pendingPlan{Objective: goal, Minutes: intent.DuracaoMin, Date: date, TaskID: intent.ID, ExplicitDate: intent.Data != ""}
	if request.Minutes < 0 || request.Minutes > 480 {
		return "Escolha um bloco entre 1 e 480 minutos para essa atividade."
	}
	if goal == "" || request.Minutes == 0 {
		body, _ := json.Marshal(request)
		if err := s.db.SavePendingConfirmation(ctx, chatID, "planejar_dia", 0, string(body)); err != nil {
			return "⚠️ Não foi possível guardar o planejamento."
		}
		if goal == "" {
			return "Qual atividade, hábito ou estudo deseja transformar em passos práticos?"
		}
		return fmt.Sprintf("Quanto tempo você quer dedicar a *%s*? Exemplo: 20 min, meia hora ou 1h30. Você define a duração antes de eu criar os passos.", goal)
	}
	existing, err := s.db.DailyPlan(ctx, date, goal)
	if err != nil {
		return "⚠️ Não foi possível consultar os planos existentes."
	}
	if existing != nil {
		return "Esse plano já está no seu checklist, sem duplicar tarefas.\n\n" + formatDayPlan(*existing)
	}
	body, _ := json.Marshal(request)
	if err := s.db.SavePendingConfirmation(ctx, chatID, "planejar_dia", 0, string(body)); err != nil {
		return "⚠️ Não foi possível guardar o planejamento."
	}
	plan, err := s.nluClient.GenerateDailyPlan(ctx, goal, request.Minutes)
	if err != nil {
		if nlu.IsProviderFailure(err) {
			return nlu.FailureMessage(err) + " A atividade e o tempo ficaram guardados. Diga ‘tente novamente’."
		}
		return "⚠️ Não consegui gerar um plano que respeite o tempo, a prática e a gratuidade. Nenhuma tarefa foi criada. Diga ‘tente novamente’ para repetir com a mesma atividade e duração."
	}
	plan.Date = date
	plan.ParentTaskID = request.TaskID
	_, err = s.db.SaveDailyPlan(ctx, plan)
	if err != nil && !errors.Is(err, database.ErrPlanExists) {
		return fmt.Sprintf("⚠️ Não foi possível salvar o plano: %v", err)
	}
	saved, err := s.db.DailyPlan(ctx, date, goal)
	if err != nil || saved == nil {
		return "⚠️ Plano salvo, mas não foi possível consultar seus passos. Veja /tarefas."
	}
	if err := s.db.DeletePendingConfirmation(ctx, chatID); err != nil {
		return "✅ Plano salvo.\n\n" + formatDayPlan(*saved)
	}
	return "✅ Plano adicionado ao checklist.\n\n" + formatDayPlan(*saved)
}

func (s *Server) handlePendingPlan(ctx context.Context, chatID int64, text string, pending *database.ConfirmacaoPendente) (bool, string) {
	var request pendingPlan
	if json.Unmarshal([]byte(pending.NovoEventoJSON), &request) != nil {
		return true, "⚠️ Não foi possível recuperar o planejamento. Use /planejar novamente."
	}
	clean := strings.Trim(synonyms.RemoveAccents(strings.ToLower(text)), " .!?")
	if clean == "cancelar" || clean == "cancela" || clean == "cancele" || clean == "cancele isso" || clean == "cancelar planejamento" || clean == "nao quero mais" || clean == "nao" || clean == "deixa" {
		_ = s.db.DeletePendingConfirmation(ctx, chatID)
		return true, "Planejamento cancelado. Nenhuma tarefa foi criada."
	}
	if _, newRequest := planningFastPath(text); newRequest {
		return false, ""
	}
	if created, err := time.ParseInLocation("2006-01-02 15:04:05", pending.CriadoEm, time.UTC); err == nil && time.Since(created) > 2*time.Hour {
		_ = s.db.DeletePendingConfirmation(ctx, chatID)
		return false, ""
	}
	if !request.ExplicitDate {
		request.Date = s.today()
	}
	minutes, _, found, err := planner.ParseDuration(text)
	if !found && request.Minutes == 0 {
		if value, e := strconv.Atoi(clean); e == nil {
			minutes = value
			found = true
		}
	}
	if err != nil {
		return true, "⚠️ " + err.Error()
	}
	if found {
		request.Minutes = minutes
	} else if request.Objective == "" {
		request.Objective = strings.TrimSpace(text)
	} else if clean != "tente novamente" && clean != "de novo" && clean != "tentar novamente" {
		return false, ""
	}
	return true, s.startDayPlan(ctx, chatID, &nlu.IntentResult{Acao: "planejar_dia", Termo: request.Objective, DuracaoMin: request.Minutes, Data: request.Date, ID: request.TaskID})
}

func formatDayPlan(plan planner.Plan) string {
	var out strings.Builder
	fmt.Fprintf(&out, "*Planejar o dia — %s*\nAtividade: %s\nTempo: %d min | Dificuldade: %s\nPrática gratuita, sem compras ou assinatura.\n\n", plan.Date, plan.Objective, plan.Minutes, plan.Difficulty)
	for i, step := range plan.Steps {
		mark := "☐"
		if step.Done {
			mark = "☑"
		}
		fmt.Fprintf(&out, "%d. %s [%d] %s — %d min\n   Concluído quando: %s\n", i+1, mark, step.TaskID, step.Action, step.Minutes, step.Completion)
		if step.Resource != "" {
			fmt.Fprintf(&out, "   %s\n", step.Resource)
		}
	}
	out.WriteString("\nMarque cada passo com /feito <ID> ou diga o que fez. /tarefas mostra o progresso.")
	return out.String()
}
