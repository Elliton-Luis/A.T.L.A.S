package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"atlas/pkg/synonyms"
)

type DailyTask struct {
	ID          int64
	Date        string
	Description string
	Done        bool
	CompletedAt *string
}

var ErrTaskNotFound = errors.New("tarefa não encontrada no checklist desse dia")

func taskKey(text string) string {
	text = synonyms.RemoveAccents(strings.ToLower(text))
	return strings.Join(strings.Fields(strings.Trim(text, " .!?")), " ")
}

// AddDailyTasks is atomic; repeating a task does not reset its completion.
func (db *DB) AddDailyTasks(ctx context.Context, date string, descriptions []string) error {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return err
	}
	if len(descriptions) == 0 || len(descriptions) > 100 {
		return fmt.Errorf("informe de 1 a 100 tarefas")
	}
	for _, description := range descriptions {
		if taskKey(description) == "" {
			return fmt.Errorf("tarefa vazia")
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, description := range descriptions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tarefas_diarias (data, descricao, chave) VALUES (?, ?, ?) ON CONFLICT(data, chave) DO NOTHING`, date, strings.TrimSpace(description), taskKey(description)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) DailyTasks(ctx context.Context, date string) ([]DailyTask, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, data, descricao, concluida, concluido_em FROM tarefas_diarias WHERE data = ? ORDER BY id`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []DailyTask
	for rows.Next() {
		var task DailyTask
		if err := rows.Scan(&task.ID, &task.Date, &task.Description, &task.Done, &task.CompletedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// CompleteDailyTask never guesses between ambiguous names or changes another day.
func (db *DB) CompleteDailyTask(ctx context.Context, date string, id int64, term string) (DailyTask, error) {
	tasks, err := db.DailyTasks(ctx, date)
	if err != nil {
		return DailyTask{}, err
	}
	key := taskKey(term)
	var matches []DailyTask
	for _, task := range tasks {
		if id > 0 && task.ID == id || id == 0 && key != "" && taskKey(task.Description) == key {
			matches = []DailyTask{task}
			break
		}
	}
	if len(matches) == 0 && id == 0 && key != "" {
		for _, task := range tasks {
			if strings.Contains(taskKey(task.Description), key) {
				matches = append(matches, task)
			}
		}
	}
	if len(matches) == 0 {
		return DailyTask{}, ErrTaskNotFound
	}
	if len(matches) != 1 {
		return DailyTask{}, fmt.Errorf("encontrei %d tarefas para esse nome; informe o nome completo ou ID de /tarefas", len(matches))
	}
	task := matches[0]
	if _, err := db.ExecContext(ctx, `UPDATE tarefas_diarias SET concluida = 1, concluido_em = COALESCE(concluido_em, CURRENT_TIMESTAMP) WHERE id = ? AND data = ?`, task.ID, date); err != nil {
		return DailyTask{}, err
	}
	task.Done = true
	return task, nil
}
