package database

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// EventsForRange expands recurrence locally; exports use two queries, not one per day.
func (db *DB) EventsForRange(ctx context.Context, start string, days int) (map[string][]Evento, error) {
	day, err := time.Parse("2006-01-02", start)
	if err != nil {
		return nil, err
	}
	if days < 1 || days > 31 {
		return nil, fmt.Errorf("período deve ter entre 1 e 31 dias")
	}
	end := day.AddDate(0, 0, days-1).Format("2006-01-02")
	// SQLite's driver may turn a naive DATETIME into a synthetic UTC RFC3339
	// string. Keep the stored wall-clock text for the user's configured timezone.
	columns := strings.Replace(selectEventoCols, "horario_oficial,", "CAST(horario_oficial AS TEXT),", 1)
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM eventos WHERE
		((frequencia IS NULL OR frequencia = 'pontual') AND date(horario_oficial) BETWEEN ? AND ?)
		OR (status = 'pendente' AND frequencia IN ('diario','semanal','mensal')) ORDER BY id`, columns), start, end)
	if err != nil {
		return nil, err
	}
	var events []Evento
	for rows.Next() {
		var event Evento
		if err := scanEvento(rows, &event); err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	completed, err := db.QueryContext(ctx, `SELECT evento_id, data FROM ocorrencias_concluidas WHERE data BETWEEN ? AND ?`, start, end)
	if err != nil {
		return nil, err
	}
	completion := make(map[string]bool)
	for completed.Next() {
		var id int64
		var date string
		if err := completed.Scan(&id, &date); err != nil {
			completed.Close()
			return nil, err
		}
		completion[fmt.Sprintf("%d:%s", id, date)] = true
	}
	err = completed.Err()
	completed.Close()
	if err != nil {
		return nil, err
	}
	result := make(map[string][]Evento, days)
	for i := 0; i < days; i++ {
		current := day.AddDate(0, 0, i)
		date := current.Format("2006-01-02")
		for _, event := range events {
			match := false
			switch event.Frequencia {
			case "", "pontual":
				match = len(event.HorarioOficial) >= 10 && event.HorarioOficial[:10] == date
			case "diario":
				match = true
			case "semanal":
				match = event.DiaSemana != nil && *event.DiaSemana == int(current.Weekday())
			case "mensal":
				if event.DiaMes != nil {
					target := *event.DiaMes
					last := daysInMonth(current.Year(), current.Month())
					if target > last {
						target = last
					}
					match = target == current.Day()
				}
			}
			if !match {
				continue
			}
			if event.Frequencia != "" && event.Frequencia != "pontual" && event.HorarioTime != "" {
				event.HorarioOficial = date + " " + event.HorarioTime + ":00"
				if completion[fmt.Sprintf("%d:%s", event.ID, date)] {
					event.Status = "concluido"
				}
			}
			result[date] = append(result[date], event)
		}
		sort.SliceStable(result[date], func(i, j int) bool { return result[date][i].HorarioOficial < result[date][j].HorarioOficial })
	}
	return result, nil
}
