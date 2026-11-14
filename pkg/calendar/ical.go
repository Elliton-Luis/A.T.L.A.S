package calendar

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"atlas/pkg/calculator"
	"atlas/pkg/database"
)

// GenerateICS builds a standard RFC 5545 iCalendar (.ics) string for ATLAS events.
func GenerateICS(events []database.Evento, timeLoc *time.Location) string {
	if timeLoc == nil {
		timeLoc = time.Local
	}
	var buf bytes.Buffer

	buf.WriteString("BEGIN:VCALENDAR\r\n")
	buf.WriteString("VERSION:2.0\r\n")
	buf.WriteString("PRODID:-//ATLAS//Assistente Executivo Socratico//PT\r\n")
	buf.WriteString("CALSCALE:GREGORIAN\r\n")
	buf.WriteString("METHOD:PUBLISH\r\n")
	buf.WriteString("X-WR-CALNAME:ATLAS — Agenda Executiva\r\n")
	buf.WriteString("X-WR-TIMEZONE:" + timeLoc.String() + "\r\n")

	for _, ev := range events {
		tOficial, rule, err := eventStart(ev, timeLoc)
		if err != nil {
			continue
		}

		uid := fmt.Sprintf("atlas-event-%d@atlas.local", ev.ID)
		dtStamp := time.Now().UTC().Format("20060102T150405Z")
		dtStart := tOficial.UTC().Format("20060102T150405Z")

		desc := fmt.Sprintf("Compromisso ATLAS.\\nPreparo: %s\\nSaida: %s\\nStatus: %s",
			ev.HorarioBuffer1, ev.HorarioBuffer2, ev.Status)

		buf.WriteString("BEGIN:VEVENT\r\n")
		buf.WriteString(fmt.Sprintf("UID:%s\r\n", uid))
		buf.WriteString(fmt.Sprintf("DTSTAMP:%s\r\n", dtStamp))
		buf.WriteString(fmt.Sprintf("DTSTART:%s\r\n", dtStart))
		if ev.DuracaoMin != nil {
			buf.WriteString(fmt.Sprintf("DTEND:%s\r\n", tOficial.Add(time.Duration(*ev.DuracaoMin)*time.Minute).UTC().Format("20060102T150405Z")))
		} else {
			desc += "\\nDuracao nao informada"
		}
		if rule != "" {
			buf.WriteString("RRULE:" + rule + "\r\n")
		}
		buf.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", escapeICS(ev.Descricao)))
		buf.WriteString(fmt.Sprintf("DESCRIPTION:%s\r\n", desc))
		buf.WriteString("STATUS:CONFIRMED\r\n")

		buffers := calculator.Calculate(tOficial, ev.DeslocamentoMin, ev.PreparoMin, calculator.DefaultDelayMargin)
		// Preserve stored punctual buffers; recurring alarms use the occurrence date.
		if ev.HorarioBuffer1 != "" || ev.PreparoMin > 0 {
			tB1, err := time.ParseInLocation("2006-01-02 15:04:05", ev.HorarioBuffer1, timeLoc)
			if err != nil && ev.PreparoMin > 0 {
				tB1 = buffers.BufferPrep
				err = nil
			}
			if err == nil {
				diffMin := int(tOficial.Sub(tB1).Minutes())
				if diffMin > 0 {
					buf.WriteString("BEGIN:VALARM\r\n")
					buf.WriteString(fmt.Sprintf("TRIGGER:-PT%dM\r\n", diffMin))
					buf.WriteString("ACTION:DISPLAY\r\n")
					buf.WriteString(fmt.Sprintf("DESCRIPTION:Preparo: %s\r\n", escapeICS(ev.Descricao)))
					buf.WriteString("END:VALARM\r\n")
				}
			}
		}

		// Alarme determinístico para hora de sair (Buffer 2) se houver
		if ev.HorarioBuffer2 != "" || ev.DeslocamentoMin > 0 {
			tB2, err := time.ParseInLocation("2006-01-02 15:04:05", ev.HorarioBuffer2, timeLoc)
			if err != nil && ev.DeslocamentoMin > 0 {
				tB2 = buffers.BufferLeave
				err = nil
			}
			if err == nil {
				diffMin := int(tOficial.Sub(tB2).Minutes())
				if diffMin > 0 {
					buf.WriteString("BEGIN:VALARM\r\n")
					buf.WriteString(fmt.Sprintf("TRIGGER:-PT%dM\r\n", diffMin))
					buf.WriteString("ACTION:DISPLAY\r\n")
					buf.WriteString(fmt.Sprintf("DESCRIPTION:Hora de Sair: %s\r\n", escapeICS(ev.Descricao)))
					buf.WriteString("END:VALARM\r\n")
				}
			}
		}

		buf.WriteString("END:VEVENT\r\n")
	}

	buf.WriteString("END:VCALENDAR\r\n")
	return buf.String()
}

func eventStart(event database.Evento, loc *time.Location) (time.Time, string, error) {
	if event.Frequencia == "" || event.Frequencia == "pontual" {
		for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", time.RFC3339} {
			if parsed, err := time.ParseInLocation(layout, event.HorarioOficial, loc); err == nil {
				return parsed, "", nil
			}
		}
		return time.Time{}, "", fmt.Errorf("data inválida")
	}
	clock, err := time.Parse("15:04", event.HorarioTime)
	if err != nil {
		return time.Time{}, "", err
	}
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
	for i := 0; i < 32; i++ {
		if event.OccursOn(start) {
			break
		}
		start = start.AddDate(0, 0, 1)
	}
	rule := ""
	switch event.Frequencia {
	case "diario":
		rule = "FREQ=DAILY"
	case "semanal":
		if event.DiaSemana == nil || *event.DiaSemana < 0 || *event.DiaSemana > 6 {
			return time.Time{}, "", fmt.Errorf("dia não informado ou inválido")
		}
		rule = "FREQ=WEEKLY;BYDAY=" + []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}[*event.DiaSemana]
	case "mensal":
		if event.DiaMes == nil || *event.DiaMes < 1 || *event.DiaMes > 31 {
			return time.Time{}, "", fmt.Errorf("dia não informado ou inválido")
		}
		rule = fmt.Sprintf("FREQ=MONTHLY;BYMONTHDAY=%d,-1;BYSETPOS=1", *event.DiaMes)
	default:
		return time.Time{}, "", fmt.Errorf("frequência inválida")
	}
	return start, rule, nil
}

func escapeICS(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
