package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"atlas/pkg/config"
	"atlas/pkg/database"
)

func TestRecurringEventsInScheduler(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "atlas_sched_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.sqlite")
	db, err := database.Open(dbPath, "", "")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Insert daily event
	dID, err := db.InsertEventoCompleto(ctx, "Tomar Remédio", "", "", "", "diario", nil, nil, "10:00", 0, 10)
	if err != nil || dID <= 0 {
		t.Fatalf("failed to insert daily event: %v", err)
	}

	// Verify it shows up in GetActiveRecurringEvents
	recurring, err := db.GetActiveRecurringEvents(ctx)
	if err != nil {
		t.Fatalf("GetActiveRecurringEvents failed: %v", err)
	}
	if len(recurring) != 1 {
		t.Fatalf("expected 1 recurring event, got %d", len(recurring))
	}

	// Mark notifications
	today := time.Now().Format("2006-01-02")
	if err := db.MarkRecurringB1Notified(ctx, dID, today); err != nil {
		t.Fatalf("MarkRecurringB1Notified failed: %v", err)
	}
	if err := db.MarkRecurringB2Notified(ctx, dID, today); err != nil {
		t.Fatalf("MarkRecurringB2Notified failed: %v", err)
	}
	if err := db.MarkRecurringCompletionNotified(ctx, dID, today); err != nil {
		t.Fatalf("MarkRecurringCompletionNotified failed: %v", err)
	}

	// Read back and verify marks
	updatedRec, _ := db.GetActiveRecurringEvents(ctx)
	if updatedRec[0].UltimaNotificacaoB1 == nil || *updatedRec[0].UltimaNotificacaoB1 != today {
		t.Errorf("expected b1 marked for today, got %v", updatedRec[0].UltimaNotificacaoB1)
	}
	if updatedRec[0].UltimaNotificacaoB2 == nil || *updatedRec[0].UltimaNotificacaoB2 != today {
		t.Errorf("expected b2 marked for today, got %v", updatedRec[0].UltimaNotificacaoB2)
	}
	if updatedRec[0].UltimaConclusaoData == nil || *updatedRec[0].UltimaConclusaoData != today {
		t.Errorf("expected completion marked for today, got %v", updatedRec[0].UltimaConclusaoData)
	}

	// Engine tick without panic
	cfg := &config.Config{
		TelegramChatID: 0, // Disabled dispatch
		Location:       time.Local,
	}
	engine := NewEngine(cfg, db, nil, nil)
	engine.Tick(ctx)
}
