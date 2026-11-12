package calendar

import (
	"strings"
	"testing"
	"time"
)

func TestRenderEmojiCalendar(t *testing.T) {
	today := time.Date(2026, time.November, 12, 10, 0, 0, 0, time.UTC)
	events := map[int]int{
		10: 2,
		12: 1, // Today with event -> 🔴12
		20: 1,
	}

	result := RenderEmojiCalendar(2026, time.November, today, events)

	if !strings.Contains(result, "Novembro de 2026") {
		t.Errorf("expected Novembro de 2026 in output, got: %s", result)
	}
	if !strings.Contains(result, "🔴12") {
		t.Errorf("expected 🔴12 for today with events, got: %s", result)
	}
	if !strings.Contains(result, "📌10") {
		t.Errorf("expected 📌10 for day with events, got: %s", result)
	}
	if !strings.Contains(result, "*4* compromisso(s)") {
		t.Errorf("expected *4* compromisso(s) in summary, got: %s", result)
	}
}
