package database

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRangeMatchesDailyRecurrenceAndCompletion(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "ranges.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	monthDay := 31
	weekday := 1
	for _, event := range []struct {
		name, freq   string
		weekday, day *int
	}{{"Treino", "diario", nil, nil}, {"Reunião", "semanal", &weekday, nil}, {"Pagamento", "mensal", nil, &monthDay}} {
		if _, err := db.InsertEventoCompleto(ctx, event.name, "", "", "", event.freq, event.weekday, event.day, "07:00", 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	events, err := db.GetActiveRecurringEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteEvent(ctx, events[0].ID, "2026-02-14"); err != nil {
		t.Fatal(err)
	}
	rangeEvents, err := db.EventsForRange(ctx, "2026-02-01", 28)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 28; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		daily, err := db.GetEventsForDay(ctx, date)
		if err != nil || !reflect.DeepEqual(daily, rangeEvents[date]) {
			t.Fatalf("recorrência divergente em %s: %+v != %+v (%v)", date, daily, rangeEvents[date], err)
		}
	}
}
