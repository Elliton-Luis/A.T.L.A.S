package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"atlas/pkg/calculator"
	"atlas/pkg/database"
)

// BuildBriefing renders stored facts without requiring an AI provider.
func (e *Engine) BuildBriefing(ctx context.Context, date string) (string, error) {
	day, err := time.ParseInLocation("2006-01-02", date, e.cfg.Location)
	if err != nil {
		return "", err
	}
	events, err := e.db.GetEventsForDay(ctx, date)
	if err != nil {
		return "", err
	}
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
	fmt.Fprintf(&b, "☀️ *ATLAS — Briefing Matinal*\n📅 %s\n\n*Agenda*\n", day.Format("02/01/2006"))
	if len(events) == 0 {
		b.WriteString("Nenhum compromisso registrado.\n")
	}
	for _, event := range events {
		hour := event.HorarioTime
		if hour == "" && len(event.HorarioOficial) >= 16 {
			hour = event.HorarioOficial[11:16]
		}
		status := ""
		if event.Status == "concluido" {
			status = " (concluído)"
		}
		fmt.Fprintf(&b, "• %s — %s%s\n", hour, event.Descricao, status)
		if official, err := time.ParseInLocation("2006-01-02 15:04:05", event.HorarioOficial, e.cfg.Location); err == nil && (event.PreparoMin > 0 || event.DeslocamentoMin > 0) {
			buffers := calculator.Calculate(official, event.DeslocamentoMin, event.PreparoMin, calculator.DefaultDelayMargin)
			if event.PreparoMin > 0 {
				fmt.Fprintf(&b, "  Preparo: %s\n", buffers.BufferPrep.Format("15:04"))
			}
			if event.DeslocamentoMin > 0 {
				fmt.Fprintf(&b, "  Saída: %s\n", buffers.BufferLeave.Format("15:04"))
			}
		}
	}
	b.WriteString("\n*Rotinas de hoje*\n")
	for _, routine := range routines {
		action, _ := decodeAction(routine)
		if routine.Tipo == "briefing" || action.Action == "briefing" || !routineMatchesDay(routine, day) {
			continue
		}
		fmt.Fprintf(&b, "• %s — %s\n", routine.Horario, routine.Nome)
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
