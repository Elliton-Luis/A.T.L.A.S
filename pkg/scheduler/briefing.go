package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"atlas/pkg/database"
	"atlas/pkg/schedule"
)

// BuildBriefing composes configured sources, retaining their order and isolating failures.
func (e *Engine) BuildBriefing(ctx context.Context, date string) (string, error) {
	body, err := e.db.GetBriefingConfig(ctx)
	if err != nil {
		return "", err
	}
	if body == "" {
		return e.BuildDefaultBriefing(ctx, date)
	}
	var items []ActionRoutine
	if err := json.Unmarshal([]byte(body), &items); err != nil {
		return "", err
	}
	if len(items) == 0 || len(items) > 12 {
		return "", fmt.Errorf("composição de briefing inválida")
	}
	day, err := time.ParseInLocation("2006-01-02", date, e.cfg.Location)
	if err != nil {
		return "", err
	}
	sections := make([]string, len(items))
	semaphore := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func(i int, item ActionRoutine) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				sections[i] = "⚠️ " + item.Title + ": tempo de consulta esgotado."
				return
			}
			defer func() { <-semaphore }()
			sectionCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			content, err := e.briefingSection(sectionCtx, day, item)
			if err != nil || strings.TrimSpace(content) == "" {
				log.Printf("[Briefing] Falha em %s: %v", item.Action, err)
				content = "⚠️ Fonte indisponível neste envio."
			}
			sections[i] = "*" + item.Title + "*\n" + content
		}(i, item)
	}
	wg.Wait()
	return fmt.Sprintf("☀️ *ATLAS — Briefing Pessoal*\n📅 %s\n\n%s", day.Format("02/01/2006"), strings.Join(sections, "\n\n")), nil
}

func (e *Engine) briefingSection(ctx context.Context, day time.Time, item ActionRoutine) (string, error) {
	if item.Action == "pensamento" {
		return item.Term, nil
	}
	if item.Action == "agenda_completa" {
		text, err := e.BuildDefaultBriefing(ctx, day.Format("2006-01-02"))
		if _, body, ok := strings.Cut(text, "\n\n"); ok {
			text = body
		}
		return text, err
	}
	cacheable := item.Action == "liturgia" || item.Action == "santo_do_dia" || item.Action == "versiculo_biblico" || item.Action == "noticias_resumo"
	key := item.Action + ":" + item.Term
	date := day.Format("2006-01-02")
	if cacheable {
		cached, err := e.db.GetBriefingCache(ctx, date, key)
		if err == nil && cached != "" {
			return cached, nil
		}
	}
	if e.ExecuteAction == nil {
		return "", fmt.Errorf("executor de briefing indisponível")
	}
	content, err := e.ExecuteAction(ctx, item)
	if err != nil {
		return "", err
	}
	if cacheable && strings.TrimSpace(content) != "" && !strings.HasPrefix(content, "⚠️") {
		expires := day.AddDate(0, 0, 1)
		if item.Action == "noticias_resumo" && time.Now().Add(30*time.Minute).Before(expires) {
			expires = time.Now().Add(30 * time.Minute)
		}
		if err := e.db.SaveBriefingCache(ctx, date, key, content, expires); err != nil {
			log.Printf("[Briefing] Cache %s: %v", item.Action, err)
		}
	}
	return content, nil
}

// BuildDefaultBriefing renders stored facts without requiring an AI provider.
func (e *Engine) BuildDefaultBriefing(ctx context.Context, date string) (string, error) {
	day, err := time.ParseInLocation("2006-01-02", date, e.cfg.Location)
	if err != nil {
		return "", err
	}
	byDay, err := e.db.EventsForRange(ctx, date, 1, e.cfg.Location)
	if err != nil {
		return "", err
	}
	events := byDay[date]
	goals, err := e.db.GetActiveGoals(ctx, 100)
	if err != nil {
		return "", err
	}
	routines, err := e.db.GetActiveRoutines(ctx)
	if err != nil {
		return "", err
	}
	bills, err := e.db.GetActiveContas(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "☀️ *ATLAS — Briefing Matinal*\n📅 %s\n\n", day.Format("02/01/2006"))
	b.WriteString(schedule.Format(events, "Agenda", e.cfg.Location))
	tasks, err := e.db.DailyTasks(ctx, date)
	if err != nil {
		return "", err
	}
	b.WriteString("\n*Checklist diário*\n")
	if len(tasks) == 0 {
		b.WriteString("Nenhuma tarefa anotada.\n")
	}
	for _, task := range tasks {
		mark := "☐"
		if task.Done {
			mark = "☑"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, task.Description)
		if task.Minutes > 0 {
			fmt.Fprintf(&b, "  %d min | %s | Resultado: %s\n", task.Minutes, task.Difficulty, task.Completion)
		}
	}
	for _, kind := range []string{"lembrete", "automacao"} {
		if kind == "lembrete" {
			b.WriteString("\n*Lembretes de hoje — mensagens fixas*\n")
		} else {
			b.WriteString("\n*Automações de hoje — ações programadas*\n")
		}
		for _, routine := range routines {
			if RoutineKind(routine) != kind || !routineMatchesDay(routine, day) {
				continue
			}
			fmt.Fprintf(&b, "• %s — %s\n", routine.Horario, routine.Nome)
		}
	}
	b.WriteString("\n*Metas ativas*\n")
	if len(goals) == 0 {
		b.WriteString("Nenhuma meta registrada.\n")
	}
	for _, goal := range goals {
		fmt.Fprintf(&b, "• %s — %d%%", goal.Titulo, goal.ProgressoPct)
		if goal.Prazo != nil {
			fmt.Fprintf(&b, " | Prazo: %s", *goal.Prazo)
		}
		b.WriteByte('\n')
	}
	b.WriteString("\n*Contas próximas do vencimento*\n")
	for _, bill := range bills {
		if bill.UltimoMesPago != nil && *bill.UltimoMesPago == day.Format("2006-01") {
			continue
		}
		last := time.Date(day.Year(), day.Month()+1, 0, 0, 0, 0, 0, day.Location()).Day()
		dueDay := bill.DiaVencimento
		if dueDay > last {
			dueDay = last
		}
		due := time.Date(day.Year(), day.Month(), dueDay, 0, 0, 0, 0, day.Location())
		if bill.DataEspecifica != nil {
			parsed, err := time.ParseInLocation("2006-01-02", *bill.DataEspecifica, day.Location())
			if err != nil {
				continue
			}
			due = parsed
		}
		if !due.After(day.AddDate(0, 0, 3)) {
			fmt.Fprintf(&b, "• %s — %s | R$ %.2f\n", bill.Descricao, due.Format("02/01"), bill.Valor)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

// ActionRoutine is persisted as JSON; it is interpreted once, when created.
type ActionRoutine struct {
	Action string `json:"acao"`
	Term   string `json:"termo,omitempty"`
	Days   []int  `json:"dias_semana,omitempty"`
	Title  string `json:"titulo,omitempty"`
}

func routineMatchesDay(r database.RotinaProativa, day time.Time) bool {
	action, err := decodeAction(r)
	if err != nil {
		return true
	} // Execution reports malformed payloads.
	if len(action.Days) == 0 {
		return true
	}
	for _, d := range action.Days {
		if d == int(day.Weekday()) {
			return true
		}
	}
	return false
}
