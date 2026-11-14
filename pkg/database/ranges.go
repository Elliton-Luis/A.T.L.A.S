package database

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// EventsForRange expands recurrence locally; exports use two queries, not one per day.
func (db *DB) EventsForRange(ctx context.Context, start string, days int, locations ...*time.Location) (map[string][]Evento, error) {
	loc := time.UTC
	if len(locations) > 0 && locations[0] != nil {
		loc = locations[0]
	}
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
		((frequencia IS NULL OR frequencia = 'pontual') AND date(horario_oficial) <= ? AND
		(date(horario_oficial) >= ? OR datetime(horario_oficial, '+' || COALESCE(duracao_min,0) || ' minutes') > datetime(?)))
		OR (status = 'pendente' AND frequencia IN ('diario','semanal','mensal')) ORDER BY id`, columns), end, start, start)
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
	lookbackDays := 0
	for _, event := range events {
		if event.DuracaoMin != nil {
			days := *event.DuracaoMin/1440 + 1
			if days > lookbackDays {
				lookbackDays = days
			}
		}
	}
	completed, err := db.QueryContext(ctx, `SELECT evento_id, data FROM ocorrencias_concluidas WHERE data BETWEEN ? AND ?`, day.AddDate(0, 0, -lookbackDays).Format("2006-01-02"), end)
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
		dayStart := time.Date(current.Year(), current.Month(), current.Day(), 0, 0, 0, 0, loc)
		dayEnd := dayStart.AddDate(0, 0, 1)
		for _, event := range events {
			span := time.Duration(0)
			if event.DuracaoMin != nil {
				span = time.Duration(*event.DuracaoMin) * time.Minute
			}
			if event.Frequencia == "" || event.Frequencia == "pontual" {
				begin, err := parseEventTime(event.HorarioOficial, loc)
				if err != nil {
					continue
				}
				if begin.Format("2006-01-02") == date || span > 0 && begin.Before(dayEnd) && begin.Add(span).After(dayStart) {
					result[date] = append(result[date], event)
				}
				continue
			}
			clock, err := time.Parse("15:04", event.HorarioTime)
			if err != nil {
				continue
			}
			lookback := int(span/(24*time.Hour)) + 1
			for back := 0; back <= lookback; back++ {
				occurrence := dayStart.AddDate(0, 0, -back)
				if !event.OccursOn(occurrence) {
					continue
				}
				begin := time.Date(occurrence.Year(), occurrence.Month(), occurrence.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
				if back > 0 && (span <= 0 || !begin.Add(span).After(dayStart)) {
					continue
				}
				if !begin.Before(dayEnd) {
					continue
				}
				copy := event
				copy.HorarioOficial = FormatDateTime(begin)
				if completion[fmt.Sprintf("%d:%s", event.ID, begin.Format("2006-01-02"))] {
					copy.Status = "concluido"
				}
				result[date] = append(result[date], copy)
			}
		}
		sort.SliceStable(result[date], func(i, j int) bool { return result[date][i].HorarioOficial < result[date][j].HorarioOficial })
	}
	return result, nil
}
