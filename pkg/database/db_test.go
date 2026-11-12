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

	// 5. Test Ideas with Continuous Suma
	idID, err := db.InsertIdeia(ctx, "SaaS Barbeiros", "Plano mensal de R$ 49", "Plano mensal R$ 49 para barbearias", "Qual o custo de aquisição?")
	if err != nil {
		t.Fatalf("InsertIdeia failed: %v", err)
	}
	if err := db.AppendToIdeia(ctx, idID, "Adicionar módulo de agendamento por WhatsApp", "Plano R$ 49 + módulo WhatsApp"); err != nil {
		t.Fatalf("AppendToIdeia failed: %v", err)
	}
	latestIdea, err := db.GetLatestActiveIdeia(ctx)
	if err != nil || latestIdea == nil {
		t.Fatalf("GetLatestActiveIdeia failed: %v", err)
	}
	if latestIdea.SumaExecutiva != "Plano R$ 49 + módulo WhatsApp" {
		t.Errorf("expected updated suma, got %s", latestIdea.SumaExecutiva)
	}

	// 6. Test User Memory
	if err := db.SaveUserMemory(ctx, "profissao", "Engenheiro de Software"); err != nil {
		t.Fatalf("SaveUserMemory failed: %v", err)
	}
	mems, err := db.GetAllUserMemories(ctx)
	if err != nil || len(mems) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(mems))
	}
	if mems[0].Chave != "profissao" || mems[0].Valor != "Engenheiro de Software" {
		t.Errorf("unexpected memory: %v", mems[0])
	}

	// 7. Test Token Telemetry
	if err := db.RecordTokenUsage(ctx, "groq", "qwen", 50, 20, 70); err != nil {
		t.Fatalf("RecordTokenUsage failed: %v", err)
	}
	stats, err := db.GetTokenStats(ctx, "2026-11-12", "2026-11")
	if err != nil || stats.TotalGroq != 70 {
		t.Errorf("expected 70 groq tokens, got %v", stats)
	}
}
