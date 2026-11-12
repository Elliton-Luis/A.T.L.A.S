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

	// 8. Test Recurring Events (Daily, Weekly, Monthly with end-of-month validation)
	// Daily event
	_, err = db.InsertEventoCompleto(ctx, "Academia Diária", "", "", "", "diario", nil, nil, "07:00", 15, 20)
	if err != nil {
		t.Fatalf("InsertEventoCompleto diario failed: %v", err)
	}

	// Weekly event (Segunda-feira = 1)
	segunda := 1
	_, err = db.InsertEventoCompleto(ctx, "Alinhamento Semanal", "", "", "", "semanal", &segunda, nil, "09:00", 10, 15)
	if err != nil {
		t.Fatalf("InsertEventoCompleto semanal failed: %v", err)
	}

	// Monthly event (Dia 31 com validação)
	dia31 := 31
	_, err = db.InsertEventoCompleto(ctx, "Fechamento Contábil", "", "", "", "mensal", nil, &dia31, "18:00", 0, 10)
	if err != nil {
		t.Fatalf("InsertEventoCompleto mensal failed: %v", err)
	}

	// 2026-11-10 is a Tuesday (Weekday = 2)
	// Should have: "Reunião de Diretoria" (pontual) + "Academia Diária" (diario) = 2 events
	eventsTuesday, err := db.GetEventsForDay(ctx, "2026-11-10")
	if err != nil {
		t.Fatalf("GetEventsForDay failed: %v", err)
	}
	if len(eventsTuesday) != 2 {
		t.Fatalf("expected 2 events on Tuesday (pontual + diario), got %d", len(eventsTuesday))
	}

	// 2026-11-09 is a Monday (Weekday = 1)
	// Should have: "Academia Diária" + "Alinhamento Semanal" = 2 events
	eventsMonday, err := db.GetEventsForDay(ctx, "2026-11-09")
	if err != nil {
		t.Fatalf("GetEventsForDay failed: %v", err)
	}
	if len(eventsMonday) != 2 {
		t.Fatalf("expected 2 events on Monday, got %d", len(eventsMonday))
	}

	// Test Monthly validation: November only has 30 days!
	// 2026-11-30 should trigger the day 31 event because 30 is the last day of November.
	eventsNov30, err := db.GetEventsForDay(ctx, "2026-11-30")
	if err != nil {
		t.Fatalf("GetEventsForDay failed: %v", err)
	}
	foundFechamento := false
	for _, ev := range eventsNov30 {
		if ev.Descricao == "Fechamento Contábil" {
			foundFechamento = true
			break
		}
	}
	if !foundFechamento {
		t.Fatalf("expected 'Fechamento Contábil' to trigger on Nov 30 due to month end clamp")
	}

	// February in a non-leap year (e.g. 2027-02-28)
	eventsFeb28, err := db.GetEventsForDay(ctx, "2027-02-28")
	if err != nil {
		t.Fatalf("GetEventsForDay failed: %v", err)
	}
	foundFeb := false
	for _, ev := range eventsFeb28 {
		if ev.Descricao == "Fechamento Contábil" {
			foundFeb = true
			break
		}
	}
	if !foundFeb {
		t.Fatalf("expected 'Fechamento Contábil' to trigger on Feb 28 due to February month end clamp")
	}
}
