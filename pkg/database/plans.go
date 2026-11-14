package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"atlas/pkg/planner"
)

var ErrPlanExists = errors.New("já existe um plano para essa atividade nesse dia")

// SaveDailyPlan keeps plan metadata and its checklist steps in one transaction.
func (db *DB) SaveDailyPlan(ctx context.Context, plan planner.Plan) (int64, error) {
	if err := planner.Validate(plan); err != nil {
		return 0, err
	}
	if _, err := time.Parse("2006-01-02", plan.Date); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM planos_diarios WHERE data = ? AND chave = ?`, plan.Date, taskKey(plan.Objective)).Scan(&existing)
	if err == nil {
		return existing, ErrPlanExists
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	if plan.ParentTaskID == 0 {
		// A previously captured broad task becomes a group, not an extra checkbox.
		err = tx.QueryRowContext(ctx, `SELECT id FROM tarefas_diarias WHERE data = ? AND chave = ?`, plan.Date, taskKey(plan.Objective)).Scan(&plan.ParentTaskID)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
	}
	if plan.ParentTaskID > 0 {
		var description string
		var done, isStep bool
		if err := tx.QueryRowContext(ctx, `SELECT descricao, concluida, EXISTS(SELECT 1 FROM passos_plano WHERE tarefa_id = tarefas_diarias.id) FROM tarefas_diarias WHERE id = ? AND data = ?`, plan.ParentTaskID, plan.Date).Scan(&description, &done, &isStep); err != nil {
			return 0, err
		}
		if isStep {
			return 0, fmt.Errorf("a tarefa já é um passo de outro plano")
		}
		if done {
			return 0, fmt.Errorf("a tarefa escolhida já está concluída")
		}
		if taskKey(description) != taskKey(plan.Objective) {
			return 0, fmt.Errorf("o plano precisa corresponder à tarefa escolhida")
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO planos_diarios (data, objetivo, chave, minutos, dificuldade, base_tarefa_id) VALUES (?, ?, ?, ?, ?, NULLIF(?, 0))`, plan.Date, strings.TrimSpace(plan.Objective), taskKey(plan.Objective), plan.Minutes, plan.Difficulty, plan.ParentTaskID)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for i, step := range plan.Steps {
		description := fmt.Sprintf("%s — %s", plan.Objective, step.Action)
		key := taskKey(description)
		var taskID int64
		err := tx.QueryRowContext(ctx, `INSERT INTO tarefas_diarias (data, descricao, chave) VALUES (?, ?, ?) ON CONFLICT(data,chave) DO UPDATE SET descricao = excluded.descricao RETURNING id`, plan.Date, description, key).Scan(&taskID)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO passos_plano (plano_id, tarefa_id, ordem, minutos, conclusao, recurso) VALUES (?, ?, ?, ?, ?, ?)`, id, taskID, i+1, step.Minutes, step.Completion, step.Resource); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func (db *DB) DailyPlan(ctx context.Context, date, objective string) (*planner.Plan, error) {
	var plan planner.Plan
	err := db.QueryRowContext(ctx, `SELECT id, data, objetivo, minutos, dificuldade, COALESCE(base_tarefa_id,0) FROM planos_diarios WHERE data = ? AND chave = ?`, date, taskKey(objective)).Scan(&plan.ID, &plan.Date, &plan.Objective, &plan.Minutes, &plan.Difficulty, &plan.ParentTaskID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT t.id, t.descricao, p.minutos, p.conclusao, p.recurso, t.concluida FROM passos_plano p JOIN tarefas_diarias t ON t.id = p.tarefa_id WHERE p.plano_id = ? ORDER BY p.ordem`, plan.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var step planner.Step
		if err := rows.Scan(&step.TaskID, &step.Action, &step.Minutes, &step.Completion, &step.Resource, &step.Done); err != nil {
			return nil, err
		}
		step.Action = strings.TrimPrefix(step.Action, plan.Objective+" — ")
		step.Free = true
		step.Practical = true
		plan.Steps = append(plan.Steps, step)
	}
	return &plan, rows.Err()
}
