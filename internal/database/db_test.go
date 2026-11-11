package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseMigrationAndOperations(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "atlas_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.sqlite")
	db, err := Open(dbPath, "", "")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// 1. Verify default seeded routines
	routines, err := db.GetAllRoutines(ctx)
	if err != nil {
		t.Fatalf("GetAllRoutines failed: %v", err)
	}
	if len(routines) != 3 {
		t.Fatalf("expected 3 default routines, got %d", len(routines))
	}

	// 2. Toggle routine
	active, err := db.ToggleRoutine(ctx, routines[0].ID)
	if err != nil {
		t.Fatalf("ToggleRoutine failed: %v", err)
	}
	if active {
		t.Fatalf("expected routine to be inactive (false), got %v", active)
	}

	// 3. Update routine time
	if err := db.UpdateRoutineTime(ctx, routines[0].ID, "07:45"); err != nil {
		t.Fatalf("UpdateRoutineTime failed: %v", err)
	}

	updated, err := db.GetAllRoutines(ctx)
	if err != nil {
		t.Fatalf("GetAllRoutines failed: %v", err)
	}
	if updated[0].Horario != "07:45" {
		t.Fatalf("expected horario 07:45, got %s", updated[0].Horario)
	}

	// 4. Test Event Insertion
	evID, err := db.InsertEvento(ctx, "Reunião de Diretoria", "2026-11-10 15:00:00", "2026-11-10 13:50:00", "2026-11-10 14:31:00")
	if err != nil {
		t.Fatalf("InsertEvento failed: %v", err)
	}
	if evID <= 0 {
		t.Fatalf("expected valid event id, got %d", evID)
	}

	events, err := db.GetEventsForDay(ctx, "2026-11-10")
	if err != nil {
		t.Fatalf("GetEventsForDay failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Descricao != "Reunião de Diretoria" {
		t.Fatalf("unexpected event description: %s", events[0].Descricao)
	}
}
