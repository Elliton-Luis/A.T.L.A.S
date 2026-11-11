package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

type Evento struct {
	ID                  int64
	Descricao           string
	HorarioOficial      string
	HorarioBuffer1      string
	HorarioBuffer2      string
	NotificadoBuffer1   int
	NotificadoBuffer2   int
	NotificadoConclusao int
	Status              string
	CriadoEm            string
}

type Objetivo struct {
	ID           int64
	Titulo       string
	Prazo        *string
	ProgressoPct int
	Status       string
	CriadoEm     string
}

type Ideia struct {
	ID                      int64
	Titulo                  string
	Conteudo                string
	QuestionamentoSocratico string
	Status                  string
	CriadoEm                string
}

type ChatMessage struct {
	ID       int64
	Role     string
	Content  string
	CriadoEm string
}

type RotinaProativa struct {
	ID             int64
	Nome           string
	Horario        string
	Tipo           string
	PromptCustom   *string
	Ativo          int
	UltimaExecucao *string
	CriadoEm       string
}

// Open initializes the SQLite database with WAL mode and strict file permissions.
func Open(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// SQLite connection limits for file locking safety
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Ensure file permissions are restrictive (0600)
	if fileInfo, err := os.Stat(dbPath); err == nil {
		if fileInfo.Mode().Perm() != 0600 {
			_ = os.Chmod(dbPath, 0600)
		}
	}

	wrapper := &DB{db}
	if err := wrapper.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed database migration: %w", err)
	}

	return wrapper, nil
}

func (db *DB) migrate(ctx context.Context) error {
	schema := `
	PRAGMA journal_mode = WAL;

	CREATE TABLE IF NOT EXISTS eventos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		descricao TEXT NOT NULL,
		horario_oficial DATETIME NOT NULL,
		horario_buffer_1 DATETIME,
		horario_buffer_2 DATETIME,
		notificado_buffer_1 INTEGER DEFAULT 0,
		notificado_buffer_2 INTEGER DEFAULT 0,
		notificado_conclusao INTEGER DEFAULT 0,
		status TEXT CHECK(status IN ('pendente', 'em_andamento', 'concluido', 'esquecido')) DEFAULT 'pendente',
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS objetivos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		titulo TEXT NOT NULL,
		prazo DATETIME,
		progresso_pct INTEGER DEFAULT 0,
		status TEXT CHECK(status IN ('ativo', 'concluido', 'pausado')) DEFAULT 'ativo',
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS ideias (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		titulo TEXT NOT NULL,
		conteudo TEXT NOT NULL,
		questionamento_socratico TEXT,
		status TEXT CHECK(status IN ('bruta', 'refinada', 'descartada', 'em_execucao')) DEFAULT 'bruta',
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS historico_chat (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		role TEXT CHECK(role IN ('user', 'assistant')) NOT NULL,
		content TEXT NOT NULL,
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS rotinas_proativas (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nome TEXT NOT NULL,
		horario TEXT NOT NULL, -- HH:MM
		tipo TEXT NOT NULL,    -- briefing, checkin, review, custom
		prompt_custom TEXT,
		ativo INTEGER DEFAULT 1,
		ultima_execucao TEXT,
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_eventos_notificacao ON eventos(
		notificado_buffer_1,
		notificado_buffer_2,
		horario_buffer_1,
		horario_buffer_2
	);

	CREATE INDEX IF NOT EXISTS idx_rotinas_ativo ON rotinas_proativas(ativo, horario);
	`

	if _, err := db.ExecContext(ctx, schema); err != nil {
		return err
	}

	// Seed default routines if table is empty
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM rotinas_proativas").Scan(&count); err == nil && count == 0 {
		defaultRoutines := []struct {
			Nome    string
			Horario string
			Tipo    string
		}{
			{"briefing_matinal", "08:00", "briefing"},
			{"checkin_tarde", "14:00", "checkin"},
			{"review_noturno", "21:30", "review"},
		}
		for _, r := range defaultRoutines {
			_, _ = db.ExecContext(ctx, `
				INSERT INTO rotinas_proativas (nome, horario, tipo, ativo)
				VALUES (?, ?, ?, 1)
			`, r.Nome, r.Horario, r.Tipo)
		}
	}

	return nil
}

// RecordHistory stores conversation context for LLM sliding window.
func (db *DB) RecordHistory(ctx context.Context, role, content string) error {
	_, err := db.ExecContext(ctx, "INSERT INTO historico_chat (role, content) VALUES (?, ?)", role, content)
	return err
}

// GetRecentHistory fetches the last N messages for LLM context.
func (db *DB) GetRecentHistory(ctx context.Context, limit int) ([]ChatMessage, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, role, content, criado_em FROM historico_chat ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.CriadoEm); err != nil {
			return nil, err
		}
		list = append(list, m)
	}

	// Reverse so oldest is first
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
}

// InsertEvento saves an event with its calculated buffers.
func (db *DB) InsertEvento(ctx context.Context, desc, officialTime, b1, b2 string) (int64, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO eventos (descricao, horario_oficial, horario_buffer_1, horario_buffer_2)
		VALUES (?, ?, ?, ?)
	`, desc, officialTime, b1, b2)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertIdeia saves an idea with socratic questioning.
func (db *DB) InsertIdeia(ctx context.Context, titulo, conteudo, socraticQuestion string) (int64, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO ideias (titulo, conteudo, questionamento_socratico)
		VALUES (?, ?, ?)
	`, titulo, conteudo, socraticQuestion)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertObjetivo saves a goal.
func (db *DB) InsertObjetivo(ctx context.Context, titulo string, prazo *string) (int64, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO objetivos (titulo, prazo)
		VALUES (?, ?)
	`, titulo, prazo)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetActiveRoutines returns all active proactive routines.
func (db *DB) GetActiveRoutines(ctx context.Context) ([]RotinaProativa, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, nome, horario, tipo, prompt_custom, ativo, ultima_execucao, criado_em
		FROM rotinas_proativas
		WHERE ativo = 1
		ORDER BY horario ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []RotinaProativa
	for rows.Next() {
		var r RotinaProativa
		if err := rows.Scan(&r.ID, &r.Nome, &r.Horario, &r.Tipo, &r.PromptCustom, &r.Ativo, &r.UltimaExecucao, &r.CriadoEm); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, nil
}

// GetAllRoutines returns all proactive routines (active or inactive).
func (db *DB) GetAllRoutines(ctx context.Context) ([]RotinaProativa, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, nome, horario, tipo, prompt_custom, ativo, ultima_execucao, criado_em
		FROM rotinas_proativas
		ORDER BY horario ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []RotinaProativa
	for rows.Next() {
		var r RotinaProativa
		if err := rows.Scan(&r.ID, &r.Nome, &r.Horario, &r.Tipo, &r.PromptCustom, &r.Ativo, &r.UltimaExecucao, &r.CriadoEm); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, nil
}

// InsertRoutine adds a new daily routine / custom reminder.
func (db *DB) InsertRoutine(ctx context.Context, nome, horario, tipo string, promptCustom *string) (int64, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO rotinas_proativas (nome, horario, tipo, prompt_custom, ativo)
		VALUES (?, ?, ?, ?, 1)
	`, nome, horario, tipo, promptCustom)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteRoutine deletes a routine by ID.
func (db *DB) DeleteRoutine(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, "DELETE FROM rotinas_proativas WHERE id = ?", id)
	return err
}

// UpdateRoutineTime changes the scheduled hour of a routine.
func (db *DB) UpdateRoutineTime(ctx context.Context, id int64, newTime string) error {
	_, err := db.ExecContext(ctx, "UPDATE rotinas_proativas SET horario = ? WHERE id = ?", newTime, id)
	return err
}

// ToggleRoutine enables or disables a routine.
func (db *DB) ToggleRoutine(ctx context.Context, id int64) (bool, error) {
	var current int
	if err := db.QueryRowContext(ctx, "SELECT ativo FROM rotinas_proativas WHERE id = ?", id).Scan(&current); err != nil {
		return false, err
	}
	newStatus := 1
	if current == 1 {
		newStatus = 0
	}
	_, err := db.ExecContext(ctx, "UPDATE rotinas_proativas SET ativo = ? WHERE id = ?", newStatus, id)
	return newStatus == 1, err
}

// MarkRoutineExecuted sets the last execution date for today to prevent multiple executions.
func (db *DB) MarkRoutineExecuted(ctx context.Context, id int64, execDate string) error {
	_, err := db.ExecContext(ctx, "UPDATE rotinas_proativas SET ultima_execucao = ? WHERE id = ?", execDate, id)
	return err
}

// GetEventsForDay retrieves events scheduled for a specific date (YYYY-MM-DD).
func (db *DB) GetEventsForDay(ctx context.Context, dateStr string) ([]Evento, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, descricao, horario_oficial, horario_buffer_1, horario_buffer_2, notificado_buffer_1, notificado_buffer_2, notificado_conclusao, status, criado_em
		FROM eventos
		WHERE date(horario_oficial) = date(?)
		ORDER BY horario_oficial ASC
	`, dateStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := rows.Scan(&e.ID, &e.Descricao, &e.HorarioOficial, &e.HorarioBuffer1, &e.HorarioBuffer2, &e.NotificadoBuffer1, &e.NotificadoBuffer2, &e.NotificadoConclusao, &e.Status, &e.CriadoEm); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, nil
}

// GetActiveGoals returns active goals.
func (db *DB) GetActiveGoals(ctx context.Context, limit int) ([]Objetivo, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, titulo, prazo, progresso_pct, status, criado_em
		FROM objetivos
		WHERE status = 'ativo'
		ORDER BY prazo ASC NULLS LAST
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Objetivo
	for rows.Next() {
		var g Objetivo
		if err := rows.Scan(&g.ID, &g.Titulo, &g.Prazo, &g.ProgressoPct, &g.Status, &g.CriadoEm); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, nil
}

// GetRecentIdeas returns recent ideas.
func (db *DB) GetRecentIdeas(ctx context.Context, limit int) ([]Ideia, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, titulo, conteudo, questionamento_socratico, status, criado_em
		FROM ideias
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Ideia
	for rows.Next() {
		var i Ideia
		if err := rows.Scan(&i.ID, &i.Titulo, &i.Conteudo, &i.QuestionamentoSocratico, &i.Status, &i.CriadoEm); err != nil {
			return nil, err
		}
		list = append(list, i)
	}
	return list, nil
}

// GetPendingBuffer1Events returns events whose prep time has arrived and not yet notified.
func (db *DB) GetPendingBuffer1Events(ctx context.Context, nowStr string) ([]Evento, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, descricao, horario_oficial, horario_buffer_1, horario_buffer_2, notificado_buffer_1, notificado_buffer_2, notificado_conclusao, status, criado_em
		FROM eventos
		WHERE notificado_buffer_1 = 0
		  AND horario_buffer_1 IS NOT NULL
		  AND horario_buffer_1 <= ?
		  AND status = 'pendente'
	`, nowStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := rows.Scan(&e.ID, &e.Descricao, &e.HorarioOficial, &e.HorarioBuffer1, &e.HorarioBuffer2, &e.NotificadoBuffer1, &e.NotificadoBuffer2, &e.NotificadoConclusao, &e.Status, &e.CriadoEm); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, nil
}

// GetPendingBuffer2Events returns events whose departure time has arrived and not yet notified.
func (db *DB) GetPendingBuffer2Events(ctx context.Context, nowStr string) ([]Evento, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, descricao, horario_oficial, horario_buffer_1, horario_buffer_2, notificado_buffer_1, notificado_buffer_2, notificado_conclusao, status, criado_em
		FROM eventos
		WHERE notificado_buffer_2 = 0
		  AND horario_buffer_2 IS NOT NULL
		  AND horario_buffer_2 <= ?
		  AND status = 'pendente'
	`, nowStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := rows.Scan(&e.ID, &e.Descricao, &e.HorarioOficial, &e.HorarioBuffer1, &e.HorarioBuffer2, &e.NotificadoBuffer1, &e.NotificadoBuffer2, &e.NotificadoConclusao, &e.Status, &e.CriadoEm); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, nil
}

// GetPendingCompletionEvents returns events that finished >= 15 min ago to ask for confirmation.
func (db *DB) GetPendingCompletionEvents(ctx context.Context, thresholdStr string) ([]Evento, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, descricao, horario_oficial, horario_buffer_1, horario_buffer_2, notificado_buffer_1, notificado_buffer_2, notificado_conclusao, status, criado_em
		FROM eventos
		WHERE notificado_conclusao = 0
		  AND horario_oficial <= ?
		  AND status = 'pendente'
		LIMIT 5
	`, thresholdStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := rows.Scan(&e.ID, &e.Descricao, &e.HorarioOficial, &e.HorarioBuffer1, &e.HorarioBuffer2, &e.NotificadoBuffer1, &e.NotificadoBuffer2, &e.NotificadoConclusao, &e.Status, &e.CriadoEm); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, nil
}

func (db *DB) MarkBuffer1Notified(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, "UPDATE eventos SET notificado_buffer_1 = 1 WHERE id = ?", id)
	return err
}

func (db *DB) MarkBuffer2Notified(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, "UPDATE eventos SET notificado_buffer_2 = 1 WHERE id = ?", id)
	return err
}

func (db *DB) MarkCompletionNotified(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, "UPDATE eventos SET notificado_conclusao = 1 WHERE id = ?", id)
	return err
}

// GetUpcomingEvents fetches pending and upcoming events for the calendar.
func (db *DB) GetUpcomingEvents(ctx context.Context, limit int) ([]Evento, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, descricao, horario_oficial, horario_buffer_1, horario_buffer_2, notificado_buffer_1, notificado_buffer_2, notificado_conclusao, status, criado_em
		FROM eventos
		WHERE status != 'concluido'
		ORDER BY horario_oficial ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := rows.Scan(&e.ID, &e.Descricao, &e.HorarioOficial, &e.HorarioBuffer1, &e.HorarioBuffer2, &e.NotificadoBuffer1, &e.NotificadoBuffer2, &e.NotificadoConclusao, &e.Status, &e.CriadoEm); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, nil
}

// MarkEventStatus updates an event's status (e.g. 'concluido', 'esquecido').
func (db *DB) MarkEventStatus(ctx context.Context, id int64, status string) error {
	_, err := db.ExecContext(ctx, "UPDATE eventos SET status = ? WHERE id = ?", status, id)
	return err
}

// UpdateGoalProgress updates a goal's completion percentage.
func (db *DB) UpdateGoalProgress(ctx context.Context, id int64, progress int) error {
	if progress > 100 {
		progress = 100
	}
	if progress < 0 {
		progress = 0
	}
	status := "ativo"
	if progress == 100 {
		status = "concluido"
	}
	_, err := db.ExecContext(ctx, "UPDATE objetivos SET progresso_pct = ?, status = ? WHERE id = ?", progress, status, id)
	return err
}

func CleanWhitespace(s string) string {
	return strings.TrimSpace(s)
}

func FormatDateTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}
