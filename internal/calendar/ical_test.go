package calendar

import (
	"strings"
	"testing"
	"time"

	"atlas/internal/database"
)

func TestGenerateICS(t *testing.T) {
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	events := []database.Evento{
		{
			ID:             1,
			Descricao:      "Reunião com Investidor",
			HorarioOficial: "2026-11-15 14:00:00",
			HorarioBuffer1: "2026-11-15 13:00:00",
			HorarioBuffer2: "2026-11-15 13:30:00",
			Status:         "pendente",
		},
	}

	ics := GenerateICS(events, loc)
	if !strings.Contains(ics, "BEGIN:VCALENDAR") {
		t.Fatalf("ICS missing VCALENDAR header")
	}
	if !strings.Contains(ics, "SUMMARY:Reunião com Investidor") {
		t.Fatalf("ICS missing SUMMARY")
	}
	if !strings.Contains(ics, "BEGIN:VALARM") {
		t.Fatalf("ICS missing VALARM alarm buffers")
	}
	if !strings.Contains(ics, "END:VCALENDAR") {
		t.Fatalf("ICS missing VCALENDAR footer")
	}
}
