package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDailyChecklistRetainsHistoryAndCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	db, err := Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.AddDailyTasks(ctx, "2026-11-14", []string{"Estudar Go", "Treinar"}); err != nil {
		t.Fatal(err)
	}
	task, err := db.CompleteDailyTask(ctx, "2026-11-14", 0, "estudar go")
	if err != nil || !task.Done {
		t.Fatalf("conclusão falhou: %+v (%v)", task, err)
	}
	if err := db.AddDailyTasks(ctx, "2026-11-14", []string{"estudar go"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AddDailyTasks(ctx, "2026-11-15", []string{"Estudar Go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompleteDailyTask(ctx, "2026-11-15", task.ID, ""); err == nil {
		t.Fatal("ID de outro dia foi concluído")
	}
	db.Close()
	db, err = Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	yesterday, err := db.DailyTasks(ctx, "2026-11-14")
	if err != nil || len(yesterday) != 2 || !yesterday[0].Done || yesterday[0].CompletedAt == nil {
		t.Fatalf("histórico perdido: %+v (%v)", yesterday, err)
	}
	today, err := db.DailyTasks(ctx, "2026-11-15")
	if err != nil || len(today) != 1 || today[0].Done {
		t.Fatalf("conclusão vazou para outro dia: %+v (%v)", today, err)
	}
	empty, err := db.DailyTasks(ctx, "2026-11-16")
	if err != nil || len(empty) != 0 {
		t.Fatal("novo dia não começou vazio")
	}
}

func TestAmbiguousTaskNeverMarksEither(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "tasks.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.AddDailyTasks(ctx, "2026-11-14", []string{"Estudar Go", "Estudar inglês"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompleteDailyTask(ctx, "2026-11-14", 0, "estudar"); err == nil {
		t.Fatal("nome ambíguo foi aceito")
	}
	tasks, _ := db.DailyTasks(ctx, "2026-11-14")
	for _, task := range tasks {
		if task.Done {
			t.Fatal("tarefa ambígua foi marcada")
		}
	}
}
