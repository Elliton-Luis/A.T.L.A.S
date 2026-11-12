package calendar

import (
	"fmt"
	"strings"
	"time"
)

var mesesPT = []string{
	"", "Janeiro", "Fevereiro", "Março", "Abril", "Maio", "Junho",
	"Julho", "Agosto", "Setembro", "Outubro", "Novembro", "Dezembro",
}

// RenderEmojiCalendar generates a text/emoji visual monthly calendar grid for Telegram.
// eventsByDay maps day of month (1-31) to count of events on that day.
func RenderEmojiCalendar(year int, month time.Month, today time.Time, eventsByDay map[int]int) string {
	var sb strings.Builder

	nomeMes := "Mês"
	if int(month) >= 1 && int(month) <= 12 {
		nomeMes = mesesPT[int(month)]
	}

	sb.WriteString(fmt.Sprintf("🗓️ *Calendário ATLAS — %s de %d*\n\n", nomeMes, year))
	sb.WriteString("`Seg  Ter  Qua  Qui  Sex  Sab  Dom`\n")

	loc := today.Location()
	firstDay := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	startWeekday := (int(firstDay.Weekday()) + 6) % 7 // Monday = 0, Sunday = 6

	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()

	currentCol := 0
	for i := 0; i < startWeekday; i++ {
		sb.WriteString("     ")
		currentCol++
	}

	daysWithEvents := 0
	totalEvents := 0
	isSameMonthYear := today.Year() == year && today.Month() == month
	todayDay := today.Day()

	for day := 1; day <= lastDay; day++ {
		evtCount := eventsByDay[day]
		if evtCount > 0 {
			daysWithEvents++
			totalEvents += evtCount
		}

		isToday := isSameMonthYear && day == todayDay

		var cell string
		if isToday && evtCount > 0 {
			cell = fmt.Sprintf("🔴%02d ", day)
		} else if isToday {
			cell = fmt.Sprintf("📍%02d ", day)
		} else if evtCount > 0 {
			cell = fmt.Sprintf("📌%02d ", day)
		} else {
			cell = fmt.Sprintf(" %02d  ", day)
		}

		sb.WriteString(cell)
		currentCol++

		if currentCol%7 == 0 {
			sb.WriteString("\n")
		}
	}

	if currentCol%7 != 0 {
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString("📍 = Hoje  |  📌 = Com evento  |  🔴 = Hoje com evento\n\n")

	freeDays := lastDay - daysWithEvents
	sb.WriteString(fmt.Sprintf("📊 *Resumo:* *%d* compromisso(s) em *%d* dia(s) • *%d* dias livres ✨",
		totalEvents, daysWithEvents, freeDays))

	return sb.String()
}
