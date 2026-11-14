package database

import (
	"context"
	"fmt"
	"time"
)

// ClaimExecution reserves an occurrence across processes sharing the database.
// Completed occurrences remain reserved. Failed work can be explicitly released.
func (db *DB) ClaimExecution(ctx context.Context, key string) (bool, error) {
	result, err := db.ExecContext(ctx, `INSERT INTO execucoes (chave, estado) VALUES (?, 'processando') ON CONFLICT(chave) DO NOTHING`, key)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (db *DB) FinishExecution(ctx context.Context, key string) error {
	_, err := db.ExecContext(ctx, `UPDATE execucoes SET estado = 'enviado' WHERE chave = ?`, key)
	return err
}

// ClaimRoutineExecution may recover abandoned generation, but never an ambiguous send.
func (db *DB) ClaimRoutineExecution(ctx context.Context, key string) (bool, error) {
	result, err := db.ExecContext(ctx, `INSERT INTO execucoes (chave, estado) VALUES (?, 'processando')
		ON CONFLICT(chave) DO UPDATE SET estado = 'processando', criado_em = CURRENT_TIMESTAMP
		WHERE execucoes.estado IN ('processando', 'falhou') AND execucoes.criado_em < datetime('now', '-5 minutes')`, key)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (db *DB) SetExecutionState(ctx context.Context, key, state string) error {
	_, err := db.ExecContext(ctx, `UPDATE execucoes SET estado = ?, criado_em = CURRENT_TIMESTAMP WHERE chave = ?`, state, key)
	return err
}

func (db *DB) ReleaseExecution(ctx context.Context, key string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM execucoes WHERE chave = ? AND estado = 'processando'`, key)
	return err
}

// CompleteEvent completes today's occurrence without disabling a recurring series.
func (db *DB) CompleteEvent(ctx context.Context, id int64, date string) error {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return err
	}
	var frequency string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(frequencia, 'pontual') FROM eventos WHERE id = ?`, id).Scan(&frequency); err != nil {
		return err
	}
	if frequency == "pontual" {
		return db.MarkEventStatus(ctx, id, "concluido")
	}
	_, err := db.ExecContext(ctx, `INSERT INTO ocorrencias_concluidas (evento_id, data) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, date)
	return err
}

func (db *DB) CompletedEventIDs(ctx context.Context, date string) (map[int64]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT evento_id FROM ocorrencias_concluidas WHERE data = ?`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	completed := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		completed[id] = true
	}
	return completed, rows.Err()
}

// SaveActionRoutine updates an existing action schedule rather than duplicating it.
func (db *DB) SaveActionRoutine(ctx context.Context, name, hour, payload string) (int64, error) {
	if _, err := time.Parse("15:04", hour); err != nil {
		return 0, fmt.Errorf("horário inválido: %s", hour)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO rotinas_proativas (nome, horario, tipo, prompt_custom, ativo)
		VALUES (?, ?, 'action', ?, 1)
		ON CONFLICT(nome) WHERE tipo = 'action' DO UPDATE SET horario = excluded.horario, prompt_custom = excluded.prompt_custom, ativo = 1
		RETURNING id`, name, hour, payload).Scan(&id)
	if err != nil {
		return 0, err
	}
	if name == "Briefing matinal" {
		if _, err := tx.ExecContext(ctx, `UPDATE rotinas_proativas SET ativo = 0 WHERE tipo = 'briefing'`); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}
