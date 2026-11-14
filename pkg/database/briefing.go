package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (db *DB) GetBriefingConfig(ctx context.Context) (string, error) {
	var body string
	err := db.QueryRowContext(ctx, `SELECT itens_json FROM briefing_config WHERE id = 1`).Scan(&body)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return body, err
}

// SaveBriefingConfig atomically persists composition and optional daily schedule.
func (db *DB) SaveBriefingConfig(ctx context.Context, body, hour string) error {
	if !json.Valid([]byte(body)) {
		return fmt.Errorf("configuração de briefing inválida")
	}
	if hour != "" {
		if _, err := time.Parse("15:04", hour); err != nil {
			return fmt.Errorf("horário inválido: %s", hour)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO briefing_config (id, itens_json) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET itens_json = excluded.itens_json`, body); err != nil {
		return err
	}
	if hour != "" {
		if _, err := saveActionRoutineTx(ctx, tx, "Briefing matinal", hour, `{"acao":"briefing"}`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) GetBriefingCache(ctx context.Context, date, key string) (string, error) {
	var content string
	err := db.QueryRowContext(ctx, `SELECT conteudo FROM briefing_cache WHERE data = ? AND chave = ? AND expira_em > datetime('now')`, date, key).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return content, err
}

func (db *DB) SaveBriefingCache(ctx context.Context, date, key, content string, expires time.Time) error {
	_, err := db.ExecContext(ctx, `INSERT INTO briefing_cache (data, chave, conteudo, expira_em) VALUES (?, ?, ?, ?)
		ON CONFLICT(data, chave) DO UPDATE SET conteudo = excluded.conteudo, expira_em = excluded.expira_em`, date, key, content, FormatDateTime(expires.UTC()))
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `DELETE FROM briefing_cache WHERE expira_em < datetime('now', '-2 days')`)
	return err
}
