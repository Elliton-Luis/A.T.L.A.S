package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCompleteRecurringOccurrencePreservesSeries(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "atlas.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	id, err := db.InsertEventoCompleto(ctx, "Treino em casa", "", "", "", "diario", nil, nil, "07:00", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteEvent(ctx, id, "2026-11-14"); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteEvent(ctx, id, "2026-11-14"); err != nil {
		t.Fatal(err)
	}
	for date, status := range map[string]string{"2026-11-14": "concluido", "2026-11-15": "pendente"} {
		events, err := db.GetEventsForDay(ctx, date)
		if err != nil || len(events) != 1 || events[0].Status != status {
			t.Fatalf("%s: %+v (%v)", date, events, err)
		}
	}
}

func TestClearedRoutinesStayClearedAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "atlas.db")
	db, err := Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClearAllRoutines(context.Background()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	count, err := db.CountAllRoutines(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("rotinas canceladas reapareceram: %d (%v)", count, err)
	}
}

func TestUpdateRoutineRejectsInvalidTime(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "atlas.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpdateRoutineTime(context.Background(), 1, "25:90"); err == nil {
		t.Fatal("horário inválido aceito")
	}
}
