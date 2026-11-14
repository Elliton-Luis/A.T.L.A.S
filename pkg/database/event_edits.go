package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"atlas/pkg/calculator"
)

// EventEdit is a partial update. Missing fields never become inferred defaults.
type EventEdit struct {
	ID             int64  `json:"id"`
	Descricao      string `json:"descricao,omitempty"`
	Horario        string `json:"horario,omitempty"`
	HorarioOficial string `json:"horario_oficial,omitempty"`
	Data           string `json:"data,omitempty"`
	Frequencia     string `json:"frequencia,omitempty"`
	DiaSemana      *int   `json:"dia_semana,omitempty"`
	DiaMes         *int   `json:"dia_mes,omitempty"`
	DiaOrigem      *int   `json:"dia_origem,omitempty"`
	HorarioOrigem  string `json:"horario_origem,omitempty"`
	Deslocamento   *int   `json:"estimativa_deslocamento_min,omitempty"`
	Preparo        *int   `json:"estimativa_preparo_min,omitempty"`
	Duracao        *int   `json:"duracao_min,omitempty"`
}

func (e EventEdit) HasChanges() bool {
	return e.Descricao != "" || e.Horario != "" || e.HorarioOficial != "" || e.Data != "" || e.Frequencia != "" || e.DiaSemana != nil || e.DiaMes != nil || e.Deslocamento != nil || e.Preparo != nil || e.Duracao != nil
}

func (db *DB) ActiveEvents(ctx context.Context) ([]Evento, error) {
	columns := strings.Replace(selectEventoCols, "horario_oficial,", "CAST(horario_oficial AS TEXT),", 1)
	rows, err := db.QueryContext(ctx, `SELECT `+columns+` FROM eventos WHERE status IN ('pendente','em_andamento') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Evento
	for rows.Next() {
		var event Evento
		if err := scanEvento(rows, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func parseEventTime(value string, loc *time.Location) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if parsed, err := time.ParseInLocation(layout, value, loc); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("data e horário inválidos: %s", value)
}

// ApplyEventEdits updates selected IDs atomically, preserving the other occurrences.
func (db *DB) ApplyEventEdits(ctx context.Context, edits []EventEdit, loc *time.Location) ([]Evento, error) {
	if len(edits) == 0 || len(edits) > 100 {
		return nil, fmt.Errorf("identifique de 1 a 100 ocorrências para alterar")
	}
	if loc == nil {
		loc = time.Local
	}
	seen := map[int64]bool{}
	for _, edit := range edits {
		if edit.ID <= 0 || seen[edit.ID] || !edit.HasChanges() {
			return nil, fmt.Errorf("informe IDs distintos e alterações concretas")
		}
		seen[edit.ID] = true
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var updated []Evento
	identityChanged := map[int64]bool{}
	for _, edit := range edits {
		columns := strings.Replace(selectEventoCols, "horario_oficial,", "CAST(horario_oficial AS TEXT),", 1)
		rows, err := tx.QueryContext(ctx, `SELECT `+columns+` FROM eventos WHERE id = ? AND status IN ('pendente','em_andamento')`, edit.ID)
		if err != nil {
			return nil, err
		}
		if !rows.Next() {
			rows.Close()
			return nil, fmt.Errorf("ocorrência %d não está ativa", edit.ID)
		}
		var event Evento
		err = scanEvento(rows, &event)
		rows.Close()
		if err != nil {
			return nil, err
		}
		old := event
		if edit.DiaOrigem != nil && (event.DiaSemana == nil || *event.DiaSemana != *edit.DiaOrigem) {
			return nil, fmt.Errorf("o ID %d não corresponde ao dia indicado; consulte /agenda", edit.ID)
		}
		if edit.HorarioOrigem != "" && edit.HorarioOrigem != event.HorarioTime {
			return nil, fmt.Errorf("o ID %d não corresponde ao horário de origem", edit.ID)
		}
		if edit.Descricao != "" {
			event.Descricao = strings.TrimSpace(edit.Descricao)
			if event.Descricao == "" {
				return nil, fmt.Errorf("descrição vazia")
			}
		}
		if edit.Frequencia != "" {
			event.Frequencia = edit.Frequencia
		}
		if edit.DiaSemana != nil {
			if *edit.DiaSemana < 0 || *edit.DiaSemana > 6 {
				return nil, fmt.Errorf("dia da semana inválido")
			}
			event.DiaSemana = edit.DiaSemana
		}
		if edit.DiaMes != nil {
			if *edit.DiaMes < 1 || *edit.DiaMes > 31 {
				return nil, fmt.Errorf("dia do mês inválido")
			}
			event.DiaMes = edit.DiaMes
		}
		if edit.Deslocamento != nil {
			if *edit.Deslocamento < 0 {
				return nil, fmt.Errorf("deslocamento não pode ser negativo")
			}
			event.DeslocamentoMin = *edit.Deslocamento
		}
		if edit.Preparo != nil {
			if *edit.Preparo < 0 {
				return nil, fmt.Errorf("preparo não pode ser negativo")
			}
			event.PreparoMin = *edit.Preparo
		}
		if edit.Duracao != nil {
			if *edit.Duracao < 1 || *edit.Duracao > 366*1440 {
				return nil, fmt.Errorf("duração inválida")
			}
			event.DuracaoMin = edit.Duracao
		}
		if edit.Horario != "" {
			event.HorarioTime = edit.Horario
		}
		reference := time.Now().In(loc)
		if event.Frequencia == "pontual" || event.Frequencia == "" {
			value := event.HorarioOficial
			if edit.HorarioOficial != "" {
				value = edit.HorarioOficial
			}
			reference, err = parseEventTime(value, loc)
			if err != nil {
				return nil, err
			}
			if edit.Data != "" {
				day, e := time.ParseInLocation("2006-01-02", edit.Data, loc)
				if e != nil {
					return nil, e
				}
				reference = time.Date(day.Year(), day.Month(), day.Day(), reference.Hour(), reference.Minute(), 0, 0, loc)
			}
			if edit.Horario == "" {
				event.HorarioTime = reference.Format("15:04")
			}
		} else if edit.HorarioOficial != "" || edit.Data != "" {
			return nil, fmt.Errorf("para recorrências, altere dia e horário; não use uma data pontual")
		}
		clock, err := time.Parse("15:04", event.HorarioTime)
		if err != nil {
			return nil, fmt.Errorf("horário inválido no ID %d", event.ID)
		}
		reference = time.Date(reference.Year(), reference.Month(), reference.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
		switch event.Frequencia {
		case "", "pontual":
			event.Frequencia = "pontual"
			event.HorarioOficial = FormatDateTime(reference)
			event.DiaSemana = nil
			event.DiaMes = nil
		case "diario":
			event.HorarioOficial = ""
			event.DiaSemana = nil
			event.DiaMes = nil
		case "semanal":
			if event.DiaSemana == nil {
				return nil, fmt.Errorf("informe o dia da semana")
			}
			event.HorarioOficial = ""
			event.DiaMes = nil
		case "mensal":
			if event.DiaMes == nil {
				return nil, fmt.Errorf("informe o dia do mês")
			}
			event.HorarioOficial = ""
			event.DiaSemana = nil
		default:
			return nil, fmt.Errorf("frequência inválida")
		}
		buffers := calculator.Calculate(reference, event.DeslocamentoMin, event.PreparoMin, calculator.DefaultDelayMargin)
		layout := "15:04"
		if event.Frequencia == "pontual" {
			layout = "2006-01-02 15:04:05"
		}
		event.HorarioBuffer1 = buffers.BufferPrep.Format(layout)
		event.HorarioBuffer2 = buffers.BufferLeave.Format(layout)
		changed := old.Frequencia != event.Frequencia || old.HorarioOficial != event.HorarioOficial || old.HorarioTime != event.HorarioTime || old.PreparoMin != event.PreparoMin || old.DeslocamentoMin != event.DeslocamentoMin || !sameInt(old.DiaSemana, event.DiaSemana) || !sameInt(old.DiaMes, event.DiaMes) || !sameInt(old.DuracaoMin, event.DuracaoMin)
		if changed {
			event.NotificadoBuffer1 = 0
			event.NotificadoBuffer2 = 0
			event.NotificadoConclusao = 0
			event.UltimaNotificacaoB1 = nil
			event.UltimaNotificacaoB2 = nil
			event.UltimaConclusaoData = nil
		}
		identityChanged[event.ID] = old.Descricao != event.Descricao || old.Frequencia != event.Frequencia || old.HorarioTime != event.HorarioTime || old.HorarioOficial != event.HorarioOficial || !sameInt(old.DiaSemana, event.DiaSemana) || !sameInt(old.DiaMes, event.DiaMes)
		_, err = tx.ExecContext(ctx, `UPDATE eventos SET descricao=?, horario_oficial=?, horario_buffer_1=?, horario_buffer_2=?, frequencia=?, dia_semana=?, dia_mes=?, horario_time=?, deslocamento_min=?, preparo_min=?, duracao_min=?, notificado_buffer_1=?, notificado_buffer_2=?, notificado_conclusao=?, ultima_notificacao_b1=?, ultima_notificacao_b2=?, ultima_conclusao_data=? WHERE id=?`, event.Descricao, event.HorarioOficial, event.HorarioBuffer1, event.HorarioBuffer2, event.Frequencia, event.DiaSemana, event.DiaMes, event.HorarioTime, event.DeslocamentoMin, event.PreparoMin, event.DuracaoMin, event.NotificadoBuffer1, event.NotificadoBuffer2, event.NotificadoConclusao, event.UltimaNotificacaoB1, event.UltimaNotificacaoB2, event.UltimaConclusaoData, event.ID)
		if err != nil {
			return nil, err
		}
		updated = append(updated, event)
	}
	for _, event := range updated {
		if !identityChanged[event.ID] {
			continue
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM eventos WHERE id != ? AND status IN ('pendente','em_andamento') AND descricao = ? COLLATE NOCASE AND COALESCE(frequencia,'pontual') = ? AND dia_semana IS ? AND dia_mes IS ? AND ((? = 'pontual' AND datetime(horario_oficial) = datetime(?)) OR (? != 'pontual' AND horario_time = ?))`, event.ID, event.Descricao, event.Frequencia, event.DiaSemana, event.DiaMes, event.Frequencia, event.HorarioOficial, event.Frequencia, event.HorarioTime).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, fmt.Errorf("já existe outra linha de %s nesse dia e horário; nenhum registro foi alterado", event.Descricao)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func sameInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
