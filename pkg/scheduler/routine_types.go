package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas/pkg/database"
)

type Reminder struct {
	Message string `json:"mensagem"`
	Days    []int  `json:"dias_semana,omitempty"`
}

func ScheduledForDay(r database.RotinaProativa, day time.Time) (bool, error) {
	if r.Ativo == 0 {
		return false, nil
	}
	action, err := decodeAction(r)
	if err != nil {
		return false, err
	}
	if len(action.Days) == 0 {
		return true, nil
	}
	for _, weekday := range action.Days {
		if weekday == int(day.Weekday()) {
			return true, nil
		}
	}
	return false, nil
}

// RoutineKind follows the executor, including legacy custom tool labels.
func RoutineKind(r database.RotinaProativa) string {
	if r.Tipo == "lembrete" {
		return "lembrete"
	}
	if r.Tipo == "custom" {
		action, _ := decodeAction(r)
		if action.Action == "" && !strings.Contains(r.Nome, "Liturgia das Horas") {
			return "lembrete"
		}
	}
	return "automacao"
}

func DecodeReminder(r database.RotinaProativa) (Reminder, error) {
	if r.PromptCustom == nil {
		return Reminder{}, fmt.Errorf("lembrete sem mensagem")
	}
	if r.Tipo != "lembrete" {
		return Reminder{Message: *r.PromptCustom}, nil
	}
	var reminder Reminder
	if err := json.Unmarshal([]byte(*r.PromptCustom), &reminder); err != nil {
		return reminder, err
	}
	if strings.TrimSpace(reminder.Message) == "" {
		return reminder, fmt.Errorf("mensagem vazia")
	}
	return reminder, nil
}
