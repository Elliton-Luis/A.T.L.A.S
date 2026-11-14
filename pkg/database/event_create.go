package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrEventExists = errors.New("essa linha já existe com características diferentes; altere pelo ID de /agenda")

func (db *DB) InsertEventSet(ctx context.Context, events []Evento) ([]int64, error) {
	if len(events) == 0 || len(events) > 100 {
		return nil, fmt.Errorf("quantidade inválida de ocorrências")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var ids []int64
	for _, event := range events {
		if strings.TrimSpace(event.Descricao) == "" {
			return nil, fmt.Errorf("descrição vazia")
		}
		if event.DuracaoMin != nil && (*event.DuracaoMin < 1 || *event.DuracaoMin > 366*1440) {
			return nil, fmt.Errorf("duração inválida")
		}
		var id int64
		var previousDuration *int
		var previousTravel, previousPrep int
		err := tx.QueryRowContext(ctx, `SELECT id, duracao_min, deslocamento_min, preparo_min FROM eventos WHERE descricao = ? COLLATE NOCASE AND status IN ('pendente','em_andamento') AND COALESCE(frequencia,'pontual') = ? AND dia_semana IS ? AND dia_mes IS ? AND
		((? = 'pontual' AND datetime(horario_oficial) = datetime(?)) OR (? != 'pontual' AND horario_time = ?)) ORDER BY id LIMIT 1`, event.Descricao, event.Frequencia, event.DiaSemana, event.DiaMes, event.Frequencia, event.HorarioOficial, event.Frequencia, event.HorarioTime).Scan(&id, &previousDuration, &previousTravel, &previousPrep)
		if err == nil {
			if previousTravel != event.DeslocamentoMin || previousPrep != event.PreparoMin || previousDuration != nil && event.DuracaoMin != nil && !sameInt(previousDuration, event.DuracaoMin) {
				return nil, fmt.Errorf("%w (ID %d)", ErrEventExists, id)
			}
			if previousDuration == nil && event.DuracaoMin != nil {
				if _, err := tx.ExecContext(ctx, `UPDATE eventos SET duracao_min = ? WHERE id = ?`, event.DuracaoMin, id); err != nil {
					return nil, err
				}
			}
			ids = append(ids, id)
			continue
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO eventos (descricao, horario_oficial, horario_buffer_1, horario_buffer_2, frequencia, dia_semana, dia_mes, horario_time, deslocamento_min, preparo_min, duracao_min) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.Descricao, event.HorarioOficial, event.HorarioBuffer1, event.HorarioBuffer2, event.Frequencia, event.DiaSemana, event.DiaMes, event.HorarioTime, event.DeslocamentoMin, event.PreparoMin, event.DuracaoMin)
		if err != nil {
			return nil, err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, tx.Commit()
}
