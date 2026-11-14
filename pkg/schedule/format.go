// Package schedule gives every agenda entry the same day/time/ID representation.
package schedule

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"atlas/pkg/calculator"
	"atlas/pkg/database"
)

var Weekdays = []string{"Domingo", "Segunda-feira", "Terça-feira", "Quarta-feira", "Quinta-feira", "Sexta-feira", "Sábado"}

func groupKey(name string) string { return strings.ToLower(strings.Join(strings.Fields(name), " ")) }

func Format(events []database.Evento, title string, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	var out strings.Builder
	fmt.Fprintf(&out, "*%s*\n", title)
	if len(events) == 0 {
		return out.String() + "Nenhum compromisso registrado para esta consulta."
	}
	groups := map[string][]database.Evento{}
	var names []string
	for _, event := range events {
		key := groupKey(event.Descricao)
		if _, ok := groups[key]; !ok {
			names = append(names, key)
		}
		groups[key] = append(groups[key], event)
	}
	sort.Strings(names)
	for _, name := range names {
		group := groups[name]
		sort.SliceStable(group, func(i, j int) bool { return order(group[i]) < order(group[j]) })
		fmt.Fprintf(&out, "\n*%s*\n", group[0].Descricao)
		for _, event := range group {
			fmt.Fprintf(&out, "• [%d] %s\n", event.ID, Line(event, loc))
		}
	}
	out.WriteString("\nPara alterar, indique atividade e dia/horário ou o ID. Exemplo: mude a Aula de sexta para 08:30.")
	return out.String()
}

func order(event database.Evento) string {
	day := 0
	if event.DiaSemana != nil {
		day = (*event.DiaSemana + 6) % 7
	}
	return fmt.Sprintf("%s:%02d:%s:%s:%09d", event.Frequencia, day, event.HorarioOficial, event.HorarioTime, event.ID)
}

func Line(event database.Evento, loc *time.Location) string {
	hour := event.HorarioTime
	if hour == "" && len(event.HorarioOficial) >= 16 {
		hour = event.HorarioOficial[11:16]
	}
	day := "Pontual"
	reference := time.Date(2000, 1, 3, 0, 0, 0, 0, loc)
	switch event.Frequencia {
	case "diario":
		day = "Todos os dias"
	case "semanal":
		if event.DiaSemana != nil && *event.DiaSemana >= 0 && *event.DiaSemana < 7 {
			day = Weekdays[*event.DiaSemana]
			reference = reference.AddDate(0, 0, (*event.DiaSemana+6)%7)
		} else {
			day = "Semanal (dia não informado)"
		}
	case "mensal":
		if event.DiaMes != nil {
			day = fmt.Sprintf("Mensal, dia %d", *event.DiaMes)
		} else {
			day = "Mensal (dia não informado)"
		}
	default:
		if len(event.HorarioOficial) >= 10 {
			if date, err := time.ParseInLocation("2006-01-02", event.HorarioOficial[:10], loc); err == nil {
				day = date.Format("02/01/2006")
				reference = date
			}
		}
	}
	text := day + " | " + hour
	if event.DuracaoMin == nil {
		text += " | duração não informada"
	} else {
		text += fmt.Sprintf(" | duração %d min", *event.DuracaoMin)
	}
	if clock, err := time.Parse("15:04", hour); err == nil {
		if len(event.HorarioOficial) >= 10 {
			if date, err := time.ParseInLocation("2006-01-02", event.HorarioOficial[:10], loc); err == nil {
				reference = date
			}
		}
		reference = time.Date(reference.Year(), reference.Month(), reference.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
		if event.DuracaoMin != nil {
			end := reference.Add(time.Duration(*event.DuracaoMin) * time.Minute)
			endLabel := end.Format("15:04")
			if end.Format("2006-01-02") != reference.Format("2006-01-02") {
				startDate := time.Date(reference.Year(), reference.Month(), reference.Day(), 0, 0, 0, 0, time.UTC)
				endDate := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
				endLabel = fmt.Sprintf("+%d dia(s) %s", int(endDate.Sub(startDate).Hours()/24), endLabel)
			}
			text += " | término " + endLabel
		}
		buffers := calculator.Calculate(reference, event.DeslocamentoMin, event.PreparoMin, calculator.DefaultDelayMargin)
		format := func(value time.Time) string {
			if value.Format("2006-01-02") != reference.Format("2006-01-02") {
				return "dia anterior " + value.Format("15:04")
			}
			return value.Format("15:04")
		}
		if event.PreparoMin > 0 {
			text += " | preparo " + format(buffers.BufferPrep)
		}
		if event.DeslocamentoMin > 0 {
			text += " | saída " + format(buffers.BufferLeave)
		}
		if event.PreparoMin == 0 && event.DeslocamentoMin == 0 {
			text += " | sem preparo/deslocamento"
		}
	}
	if event.Status == "concluido" {
		text += " | concluído"
	}
	return text
}
