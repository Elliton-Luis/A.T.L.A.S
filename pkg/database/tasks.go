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
	Minutes     int
	Difficulty  string
	Completion  string
	Resource    string
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
	return db.DailyTasksRange(ctx, date, date)
}

func (db *DB) DailyTasksRange(ctx context.Context, from, to string) ([]DailyTask, error) {
	date := from
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, err
	}
	if _, err := time.Parse("2006-01-02", to); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT t.id, t.data, t.descricao, t.concluida, t.concluido_em,
		COALESCE(p.minutos,0), COALESCE(plan.dificuldade,''), COALESCE(p.conclusao,''), COALESCE(p.recurso,'')
		FROM tarefas_diarias t LEFT JOIN passos_plano p ON p.tarefa_id = t.id LEFT JOIN planos_diarios plan ON plan.id = p.plano_id
		WHERE t.data BETWEEN ? AND ? AND NOT EXISTS (SELECT 1 FROM planos_diarios parent WHERE parent.base_tarefa_id = t.id)
		ORDER BY t.data, t.id`, date, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []DailyTask
	for rows.Next() {
		var task DailyTask
		if err := rows.Scan(&task.ID, &task.Date, &task.Description, &task.Done, &task.CompletedAt, &task.Minutes, &task.Difficulty, &task.Completion, &task.Resource); err != nil {
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
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return DailyTask{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE tarefas_diarias SET concluida = 1, concluido_em = COALESCE(concluido_em, CURRENT_TIMESTAMP) WHERE id = ? AND data = ?`, task.ID, date); err != nil {
		return DailyTask{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tarefas_diarias SET concluida = 1, concluido_em = COALESCE(concluido_em,CURRENT_TIMESTAMP)
		WHERE data = ? AND id IN (SELECT base_tarefa_id FROM planos_diarios plan WHERE plan.data = ? AND plan.base_tarefa_id IS NOT NULL
		AND plan.id = (SELECT plano_id FROM passos_plano WHERE tarefa_id = ?)
		AND EXISTS (SELECT 1 FROM passos_plano s WHERE s.plano_id = plan.id)
		AND NOT EXISTS (SELECT 1 FROM passos_plano s JOIN tarefas_diarias child ON child.id = s.tarefa_id WHERE s.plano_id = plan.id AND child.concluida = 0))`, date, date, task.ID); err != nil {
		return DailyTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return DailyTask{}, err
	}
	task.Done = true
	return task, nil
}
