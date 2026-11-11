package calendar

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"atlas/internal/database"
)

// GenerateICS builds a standard RFC 5545 iCalendar (.ics) string for ATLAS events.
func GenerateICS(events []database.Evento, timeLoc *time.Location) string {
	var buf bytes.Buffer

	buf.WriteString("BEGIN:VCALENDAR\r\n")
	buf.WriteString("VERSION:2.0\r\n")
	buf.WriteString("PRODID:-//ATLAS//Assistente Executivo Socratico//PT\r\n")
	buf.WriteString("CALSCALE:GREGORIAN\r\n")
	buf.WriteString("METHOD:PUBLISH\r\n")
	buf.WriteString("X-WR-CALNAME:ATLAS — Agenda Executiva\r\n")
	buf.WriteString("X-WR-TIMEZONE:America/Sao_Paulo\r\n")

	for _, ev := range events {
		tOficial, err := time.ParseInLocation("2006-01-02 15:04:05", ev.HorarioOficial, timeLoc)
		if err != nil {
			tOficial, err = time.ParseInLocation("2006-01-02 15:04", ev.HorarioOficial, timeLoc)
			if err != nil {
				continue
			}
		}

		// Event duration default: 1 hour
		tFim := tOficial.Add(1 * time.Hour)

		uid := fmt.Sprintf("atlas-event-%d@atlas.local", ev.ID)
		dtStamp := time.Now().UTC().Format("20060102T150405Z")
		dtStart := tOficial.UTC().Format("20060102T150405Z")
		dtEnd := tFim.UTC().Format("20060102T150405Z")

		desc := fmt.Sprintf("Compromisso ATLAS.\\nPreparo: %s\\nSaida: %s\\nStatus: %s",
			ev.HorarioBuffer1, ev.HorarioBuffer2, ev.Status)

		buf.WriteString("BEGIN:VEVENT\r\n")
		buf.WriteString(fmt.Sprintf("UID:%s\r\n", uid))
		buf.WriteString(fmt.Sprintf("DTSTAMP:%s\r\n", dtStamp))
		buf.WriteString(fmt.Sprintf("DTSTART:%s\r\n", dtStart))
		buf.WriteString(fmt.Sprintf("DTEND:%s\r\n", dtEnd))
		buf.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", escapeICS(ev.Descricao)))
		buf.WriteString(fmt.Sprintf("DESCRIPTION:%s\r\n", desc))
		buf.WriteString("STATUS:CONFIRMED\r\n")

		// Alarme determinístico para hora de se arrumar (Buffer 1) se houver
		if ev.HorarioBuffer1 != "" {
			if tB1, err := time.ParseInLocation("2006-01-02 15:04:05", ev.HorarioBuffer1, timeLoc); err == nil {
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
		if ev.HorarioBuffer2 != "" {
			if tB2, err := time.ParseInLocation("2006-01-02 15:04:05", ev.HorarioBuffer2, timeLoc); err == nil {
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

func escapeICS(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
