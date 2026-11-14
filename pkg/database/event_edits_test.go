package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRecurringPartialEditPreservesDaysIDsZerosAndDuration(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "events.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	duration := 180
	ids := map[int]int64{}
	for _, day := range []int{1, 3, 4, 5} {
		value := day
		id, err := db.InsertEventoCompleto(ctx, "Aula", "", "18:00", "18:00", "semanal", &value, nil, "18:00", 0, 0, &duration)
		if err != nil {
			t.Fatal(err)
		}
		ids[day] = id
	}
	friday := 5
	if _, err := db.ApplyEventEdits(ctx, []EventEdit{{ID: ids[5], DiaOrigem: &friday, Horario: "08:30"}}, time.UTC); err != nil {
		t.Fatal(err)
	}
	events, err := db.ActiveEvents(ctx)
	if err != nil || len(events) != 4 {
		t.Fatal(events, err)
	}
	for _, event := range events {
		want := "18:00"
		if *event.DiaSemana == 5 {
			want = "08:30"
		}
		if event.HorarioTime != want || event.ID != ids[*event.DiaSemana] || event.PreparoMin != 0 || event.DeslocamentoMin != 0 || event.DuracaoMin == nil || *event.DuracaoMin != 180 {
			t.Fatalf("características perdidas: %+v", event)
		}
	}
	if _, err := db.ApplyEventEdits(ctx, []EventEdit{{ID: ids[1], Horario: "19:00"}, {ID: ids[3], Horario: "25:90"}}, time.UTC); err == nil {
		t.Fatal("lote inválido aceito")
	}
	old, _ := db.GetEventoByID(ctx, ids[1])
	if old.HorarioTime != "18:00" {
		t.Fatal("lote aplicado parcialmente")
	}
	monday := 1
	if _, err := db.ApplyEventEdits(ctx, []EventEdit{{ID: ids[3], DiaSemana: &monday}}, time.UTC); err == nil {
		t.Fatal("mudança criou duplicata no destino")
	}
	wednesday, _ := db.GetEventoByID(ctx, ids[3])
	if *wednesday.DiaSemana != 3 {
		t.Fatal("conflito não foi revertido")
	}
	id, err := db.InsertEventoCompleto(ctx, "Aula", "", "18:00", "18:00", "semanal", old.DiaSemana, nil, "18:00", 0, 0, &duration)
	if err != nil || id != ids[1] {
		t.Fatal("cadastro repetido duplicou a linha", id, err)
	}
}

func TestCompletionOnlyAfterKnownEnd(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "events.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	duration := 180
	if _, err := db.InsertEventoCompleto(ctx, "Consulta", "2026-11-14 18:00:00", "", "", "pontual", nil, nil, "18:00", 0, 0, &duration); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertEvento(ctx, "Legado sem duração", "2026-11-14 18:00:00", "", ""); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetPendingCompletionEvents(ctx, "2026-11-14 18:30:00")
	if err != nil || len(before) != 0 {
		t.Fatal("cobrou conclusão antes do término", before, err)
	}
	after, err := db.GetPendingCompletionEvents(ctx, "2026-11-14 21:00:00")
	if err != nil || len(after) != 1 || after[0].Descricao != "Consulta" {
		t.Fatal("não respeitou duração ou inventou duração legada", after, err)
	}
}

func TestOvernightRecurringOccurrenceAppearsNextDay(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "events.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	duration := 180
	friday := 5
	id, err := db.InsertEventoCompleto(ctx, "Plantão", "", "", "", "semanal", &friday, nil, "23:00", 0, 0, &duration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteEvent(ctx, id, "2026-11-13"); err != nil {
		t.Fatal(err)
	}
	events, err := db.EventsForRange(ctx, "2026-11-14", 1, time.UTC)
	if err != nil || len(events["2026-11-14"]) != 1 || events["2026-11-14"][0].Status != "concluido" {
		t.Fatal("ocorrência da véspera desapareceu", events, err)
	}
}

func TestLegacyDurationMigrationPreservesRowsAndUnknownDuration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := db.InsertEvento(ctx, "Compromisso antigo", "2026-11-20 18:00:00", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Build a pre-duration fixture; the production migration only adds a column.
	if _, err := db.ExecContext(ctx, "ALTER TABLE eventos DROP COLUMN duracao_min"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	event, err := db.GetEventoByID(ctx, id)
	if err != nil || event.Descricao != "Compromisso antigo" || event.DuracaoMin != nil {
		t.Fatal("migração alterou ou inventou dados", event, err)
	}
}
