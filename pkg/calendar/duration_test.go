package calendar

import (
	"strings"
	"testing"
	"time"

	"atlas/pkg/database"
)

func TestCalendarExportsActualDurationAndRecurringRules(t *testing.T) {
	duration := 180
	friday := 5
	monthDay := 31
	events := []database.Evento{{ID: 1, Descricao: "Consulta", Frequencia: "pontual", HorarioOficial: "2026-11-14 23:00:00", DuracaoMin: &duration}, {ID: 2, Descricao: "Aula", Frequencia: "semanal", DiaSemana: &friday, HorarioTime: "08:30", DuracaoMin: &duration}, {ID: 3, Descricao: "Mensal", Frequencia: "mensal", DiaMes: &monthDay, HorarioTime: "10:00", DuracaoMin: &duration}}
	text := GenerateICS(events, time.UTC)
	for _, expected := range []string{"DTSTART:20261114T230000Z", "DTEND:20261115T020000Z", "RRULE:FREQ=WEEKLY;BYDAY=FR", "RRULE:FREQ=MONTHLY;BYMONTHDAY=31,-1;BYSETPOS=1"} {
		if !strings.Contains(text, expected) {
			t.Errorf("calendário não contém %s\n%s", expected, text)
		}
	}
	unknown := GenerateICS([]database.Evento{{ID: 4, Descricao: "Legado", HorarioOficial: "2026-11-14 12:00:00"}}, time.UTC)
	if strings.Contains(unknown, "DTEND:") {
		t.Fatal("inventou duração para registro antigo")
	}
}
