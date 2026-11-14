package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"atlas/pkg/planner"
)

func storedPlan() planner.Plan {
	return planner.Plan{Date: "2026-11-14", Objective: "Estudar inglês", Minutes: 30, Difficulty: "facil", Steps: []planner.Step{{Action: "Leia uma história infantil", Minutes: 15, Completion: "5 palavras anotadas", Free: true, Practical: true}, {Action: "Pratique frases com o ATLAS", Minutes: 15, Completion: "5 frases escritas", Free: true, Practical: true}}}
}

func TestPlanBecomesTimedChecklistWithoutDoubleCountingParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.db")
	db, err := Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plan := storedPlan()
	if err := db.AddDailyTasks(ctx, plan.Date, []string{plan.Objective}); err != nil {
		t.Fatal(err)
	}
	parent, _ := db.DailyTasks(ctx, plan.Date)
	if _, err := db.SaveDailyPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveDailyPlan(ctx, plan); !errors.Is(err, ErrPlanExists) {
		t.Fatalf("duplicação não rejeitada: %v", err)
	}
	tasks, err := db.DailyTasks(ctx, plan.Date)
	if err != nil || len(tasks) != 2 || tasks[0].Minutes != 15 || tasks[0].Completion == "" {
		t.Fatalf("metadados ou agrupamento perdidos: %+v (%v)", tasks, err)
	}
	for _, task := range tasks {
		if _, err := db.CompleteDailyTask(ctx, plan.Date, task.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	var done bool
	if err := db.QueryRowContext(ctx, `SELECT concluida FROM tarefas_diarias WHERE id = ?`, parent[0].ID).Scan(&done); err != nil || !done {
		t.Fatal("conclusão do plano não chegou à tarefa original", err)
	}
	db.Close()
	db, err = Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	saved, err := db.DailyPlan(ctx, plan.Date, plan.Objective)
	if err != nil || saved == nil || len(saved.Steps) != 2 || !saved.Steps[0].Done {
		t.Fatal("histórico do plano perdido", saved, err)
	}
}

func TestInvalidPlanNeverPartiallyWritesTasks(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "plans.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	plan := storedPlan()
	plan.Steps[0].Free = false
	if _, err := db.SaveDailyPlan(context.Background(), plan); err == nil {
		t.Fatal("plano pago aceito")
	}
	tasks, _ := db.DailyTasks(context.Background(), plan.Date)
	if len(tasks) != 0 {
		t.Fatal("tarefas inválidas foram salvas")
	}
}
