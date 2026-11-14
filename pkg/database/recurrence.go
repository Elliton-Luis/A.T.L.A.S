package database

import (
	"context"
	"time"
)

func (event Evento) OccursOn(day time.Time) bool {
	switch event.Frequencia {
	case "diario":
		return true
	case "semanal":
		return event.DiaSemana != nil && *event.DiaSemana == int(day.Weekday())
	case "mensal":
		if event.DiaMes == nil {
			return false
		}
		target := *event.DiaMes
		last := daysInMonth(day.Year(), day.Month())
		if target > last {
			target = last
		}
		return target == day.Day()
	default:
		return len(event.HorarioOficial) >= 10 && event.HorarioOficial[:10] == day.Format("2006-01-02")
	}
}

func (db *DB) OccurrenceCompleted(ctx context.Context, id int64, date string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ocorrencias_concluidas WHERE evento_id = ? AND data = ?`, id, date).Scan(&count)
	return count > 0, err
}
