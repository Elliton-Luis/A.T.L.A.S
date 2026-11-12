package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/tursodatabase/libsql-client-go/libsql"
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
	Frequencia          string  // 'pontual', 'diario', 'semanal', 'mensal'
	DiaSemana           *int    // 0 = Domingo, 1 = Segunda, ..., 6 = Sábado
	DiaMes              *int    // 1 a 31 (com validação do último dia do mês)
	HorarioTime         string  // "HH:MM"
	DeslocamentoMin     int
	PreparoMin          int
	UltimaNotificacaoB1 *string // "YYYY-MM-DD"
	UltimaNotificacaoB2 *string // "YYYY-MM-DD"
	UltimaConclusaoData *string // "YYYY-MM-DD"
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
	SumaExecutiva           string
	QuestionamentoSocratico string
	Status                  string
	CriadoEm                string
	AtualizadoEm            string
}

type MemoriaUsuario struct {
	ID           int64
	Chave        string
	Valor        string
	AtualizadoEm string
}

type TokenStats struct {
	TotalHoje  int
	TotalMes   int
	TotalGroq  int
	TotalNv    int
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

// Open initializes the database.
// If tursoURL is non-empty, it connects to Turso (libSQL) in the cloud.
// Otherwise, it opens the local SQLite file in WAL mode.
func Open(dbPath, tursoURL, tursoToken string) (*DB, error) {
	if strings.TrimSpace(tursoURL) != "" {
		dsn := strings.TrimSpace(tursoURL)
		if strings.TrimSpace(tursoToken) != "" && !strings.Contains(dsn, "authToken=") {
			sep := "?"
			if strings.Contains(dsn, "?") {
				sep = "&"
			}
			dsn = fmt.Sprintf("%s%sauthToken=%s", dsn, sep, strings.TrimSpace(tursoToken))
		}

		db, err := sql.Open("libsql", dsn)
		if err != nil {
			return nil, fmt.Errorf("failed to open turso database: %w", err)
		}
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(time.Hour)

		wrapper := &DB{db}
		if err := wrapper.migrate(context.Background(), true); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed turso database migration: %w", err)
		}
		return wrapper, nil
	}

	// Local SQLite mode
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
	if err := wrapper.migrate(context.Background(), false); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed database migration: %w", err)
	}

	return wrapper, nil
}

func (db *DB) migrate(ctx context.Context, isRemote bool) error {
	if !isRemote {
		_, _ = db.ExecContext(ctx, "PRAGMA journal_mode = WAL;")
	}

	schema := `
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
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP,
		frequencia TEXT DEFAULT 'pontual',
		dia_semana INTEGER,
		dia_mes INTEGER,
		horario_time TEXT,
		deslocamento_min INTEGER DEFAULT 25,
		preparo_min INTEGER DEFAULT 35,
		ultima_notificacao_b1 TEXT,
		ultima_notificacao_b2 TEXT,
		ultima_conclusao_data TEXT
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
		suma_executiva TEXT NOT NULL DEFAULT '',
		questionamento_socratico TEXT,
		status TEXT CHECK(status IN ('bruta', 'refinada', 'descartada', 'em_execucao')) DEFAULT 'bruta',
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP,
		atualizado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS memoria_usuario (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chave TEXT UNIQUE NOT NULL,
		valor TEXT NOT NULL,
		atualizado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS uso_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provedor TEXT NOT NULL,
		modelo TEXT NOT NULL,
		tokens_entrada INTEGER DEFAULT 0,
		tokens_saida INTEGER DEFAULT 0,
		total_tokens INTEGER DEFAULT 0,
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

	CREATE TABLE IF NOT EXISTS confirmacoes_pendentes (
		chat_id INTEGER PRIMARY KEY,
		tipo TEXT NOT NULL,
		evento_id INTEGER NOT NULL,
		novo_evento_json TEXT NOT NULL,
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

	// Safe column additions for existing tables
	_, _ = db.ExecContext(ctx, "ALTER TABLE ideias ADD COLUMN suma_executiva TEXT NOT NULL DEFAULT '';")
	_, _ = db.ExecContext(ctx, "ALTER TABLE ideias ADD COLUMN atualizado_em DATETIME DEFAULT CURRENT_TIMESTAMP;")
	_, _ = db.ExecContext(ctx, "ALTER TABLE eventos ADD COLUMN frequencia TEXT DEFAULT 'pontual';")
	_, _ = db.ExecContext(ctx, "ALTER TABLE eventos ADD COLUMN dia_semana INTEGER;")
	_, _ = db.ExecContext(ctx, "ALTER TABLE eventos ADD COLUMN dia_mes INTEGER;")
	_, _ = db.ExecContext(ctx, "ALTER TABLE eventos ADD COLUMN horario_time TEXT;")
	_, _ = db.ExecContext(ctx, "ALTER TABLE eventos ADD COLUMN deslocamento_min INTEGER DEFAULT 25;")
	_, _ = db.ExecContext(ctx, "ALTER TABLE eventos ADD COLUMN preparo_min INTEGER DEFAULT 35;")
	_, _ = db.ExecContext(ctx, "ALTER TABLE eventos ADD COLUMN ultima_notificacao_b1 TEXT;")
	_, _ = db.ExecContext(ctx, "ALTER TABLE eventos ADD COLUMN ultima_notificacao_b2 TEXT;")
	_, _ = db.ExecContext(ctx, "ALTER TABLE eventos ADD COLUMN ultima_conclusao_data TEXT;")

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

// InsertEventoCompleto saves an event with full recurrence parameters.
func (db *DB) InsertEventoCompleto(ctx context.Context, desc, officialTime, b1, b2, freq string, diaSemana, diaMes *int, horarioTime string, desloc, prep int) (int64, error) {
	if freq == "" {
		freq = "pontual"
	}
	if desloc <= 0 {
		desloc = 25
	}
	if prep <= 0 {
		prep = 35
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO eventos (
			descricao, horario_oficial, horario_buffer_1, horario_buffer_2,
			frequencia, dia_semana, dia_mes, horario_time, deslocamento_min, preparo_min
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, desc, officialTime, b1, b2, freq, diaSemana, diaMes, horarioTime, desloc, prep)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertEvento saves an event with its calculated buffers (pontual backward compatible).
func (db *DB) InsertEvento(ctx context.Context, desc, officialTime, b1, b2 string) (int64, error) {
	return db.InsertEventoCompleto(ctx, desc, officialTime, b1, b2, "pontual", nil, nil, "", 25, 35)
}

// InsertIdeia saves an idea with socratic questioning and executive summary.
func (db *DB) InsertIdeia(ctx context.Context, titulo, conteudo, suma, socraticQuestion string) (int64, error) {
	if suma == "" {
		suma = conteudo
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO ideias (titulo, conteudo, suma_executiva, questionamento_socratico)
		VALUES (?, ?, ?, ?)
	`, titulo, conteudo, suma, socraticQuestion)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AppendToIdeia concatenates a new thought bullet and updates the accumulated executive summary.
func (db *DB) AppendToIdeia(ctx context.Context, id int64, novoPonto, novaSuma string) error {
	var currentConteudo string
	_ = db.QueryRowContext(ctx, "SELECT conteudo FROM ideias WHERE id = ?", id).Scan(&currentConteudo)

	updatedConteudo := currentConteudo
	if updatedConteudo != "" {
		updatedConteudo += "\n"
	}
	updatedConteudo += "• " + novoPonto

	_, err := db.ExecContext(ctx, `
		UPDATE ideias
		SET conteudo = ?, suma_executiva = ?, atualizado_em = CURRENT_TIMESTAMP
		WHERE id = ?
	`, updatedConteudo, novaSuma, id)
	return err
}

// GetLatestActiveIdeia returns the most recently updated idea.
func (db *DB) GetLatestActiveIdeia(ctx context.Context) (*Ideia, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, titulo, conteudo, COALESCE(suma_executiva, ''), questionamento_socratico, status, criado_em, COALESCE(atualizado_em, criado_em)
		FROM ideias
		WHERE status != 'descartada'
		ORDER BY id DESC
		LIMIT 1
	`)
	var i Ideia
	if err := row.Scan(&i.ID, &i.Titulo, &i.Conteudo, &i.SumaExecutiva, &i.QuestionamentoSocratico, &i.Status, &i.CriadoEm, &i.AtualizadoEm); err != nil {
		return nil, err
	}
	return &i, nil
}

// GetIdeiaByID returns an idea by its numeric ID.
func (db *DB) GetIdeiaByID(ctx context.Context, id int64) (*Ideia, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, titulo, conteudo, COALESCE(suma_executiva, ''), questionamento_socratico, status, criado_em, COALESCE(atualizado_em, criado_em)
		FROM ideias
		WHERE id = ?
	`, id)
	var i Ideia
	if err := row.Scan(&i.ID, &i.Titulo, &i.Conteudo, &i.SumaExecutiva, &i.QuestionamentoSocratico, &i.Status, &i.CriadoEm, &i.AtualizadoEm); err != nil {
		return nil, err
	}
	return &i, nil
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

// DeleteRoutineByName searches and removes a routine by matching text.
func (db *DB) DeleteRoutineByName(ctx context.Context, term string) (string, error) {
	var id int64
	var nome string
	err := db.QueryRowContext(ctx, "SELECT id, nome FROM rotinas_proativas WHERE nome LIKE ? OR prompt_custom LIKE ? LIMIT 1", "%"+term+"%", "%"+term+"%").Scan(&id, &nome)
	if err != nil {
		return "", err
	}
	_, err = db.ExecContext(ctx, "DELETE FROM rotinas_proativas WHERE id = ?", id)
	return nome, err
}

// DeleteEvento removes an event by ID.
func (db *DB) DeleteEvento(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, "DELETE FROM eventos WHERE id = ?", id)
	return err
}

// FindEventoByDesc searches an active event by description term.
func (db *DB) FindEventoByDesc(ctx context.Context, term string) (int64, string, error) {
	var id int64
	var desc string
	err := db.QueryRowContext(ctx, "SELECT id, descricao FROM eventos WHERE descricao LIKE ? AND status = 'pendente' ORDER BY id DESC LIMIT 1", "%"+term+"%").Scan(&id, &desc)
	return id, desc, err
}

// FindGoalByTitle searches an active goal by title term.
func (db *DB) FindGoalByTitle(ctx context.Context, term string) (int64, string, error) {
	var id int64
	var title string
	err := db.QueryRowContext(ctx, "SELECT id, titulo FROM objetivos WHERE titulo LIKE ? AND status = 'ativo' ORDER BY id DESC LIMIT 1", "%"+term+"%").Scan(&id, &title)
	return id, title, err
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

const selectEventoCols = `id, descricao, horario_oficial, COALESCE(horario_buffer_1, ''), COALESCE(horario_buffer_2, ''),
       notificado_buffer_1, notificado_buffer_2, notificado_conclusao, status, criado_em,
       COALESCE(frequencia, 'pontual'), dia_semana, dia_mes, COALESCE(horario_time, ''),
       COALESCE(deslocamento_min, 25), COALESCE(preparo_min, 35),
       ultima_notificacao_b1, ultima_notificacao_b2, ultima_conclusao_data`

func scanEvento(rows *sql.Rows, e *Evento) error {
	return rows.Scan(
		&e.ID, &e.Descricao, &e.HorarioOficial, &e.HorarioBuffer1, &e.HorarioBuffer2,
		&e.NotificadoBuffer1, &e.NotificadoBuffer2, &e.NotificadoConclusao, &e.Status, &e.CriadoEm,
		&e.Frequencia, &e.DiaSemana, &e.DiaMes, &e.HorarioTime,
		&e.DeslocamentoMin, &e.PreparoMin,
		&e.UltimaNotificacaoB1, &e.UltimaNotificacaoB2, &e.UltimaConclusaoData,
	)
}

// GetActiveRecurringEvents returns all active recurring events (diario, semanal, mensal).
func (db *DB) GetActiveRecurringEvents(ctx context.Context) ([]Evento, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM eventos
		WHERE status = 'pendente' AND frequencia IN ('diario', 'semanal', 'mensal')
		ORDER BY id ASC
	`, selectEventoCols))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := scanEvento(rows, &e); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, nil
}

// MarkRecurringB1Notified records today's date for recurring prep alert.
func (db *DB) MarkRecurringB1Notified(ctx context.Context, id int64, todayStr string) error {
	_, err := db.ExecContext(ctx, "UPDATE eventos SET ultima_notificacao_b1 = ? WHERE id = ?", todayStr, id)
	return err
}

// MarkRecurringB2Notified records today's date for recurring departure alert.
func (db *DB) MarkRecurringB2Notified(ctx context.Context, id int64, todayStr string) error {
	_, err := db.ExecContext(ctx, "UPDATE eventos SET ultima_notificacao_b2 = ? WHERE id = ?", todayStr, id)
	return err
}

// MarkRecurringCompletionNotified records today's date for recurring post-event check.
func (db *DB) MarkRecurringCompletionNotified(ctx context.Context, id int64, todayStr string) error {
	_, err := db.ExecContext(ctx, "UPDATE eventos SET ultima_conclusao_data = ? WHERE id = ?", todayStr, id)
	return err
}

// GetEventsForDay retrieves events scheduled for a specific date (YYYY-MM-DD),
// combining punctual events and active recurring events (daily, weekly, monthly) matching the date.
func (db *DB) GetEventsForDay(ctx context.Context, dateStr string) ([]Evento, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM eventos
		WHERE date(horario_oficial) = date(?) AND (frequencia IS NULL OR frequencia = 'pontual')
		ORDER BY horario_oficial ASC
	`, selectEventoCols), dateStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := scanEvento(rows, &e); err != nil {
			return nil, err
		}
		list = append(list, e)
	}

	// Now check recurring events matching this date
	recurring, err := db.GetActiveRecurringEvents(ctx)
	if err == nil && len(recurring) > 0 {
		parsedDate, parseErr := time.Parse("2006-01-02", dateStr)
		if parseErr == nil {
			weekday := int(parsedDate.Weekday())
			// Deterministic last day of this month (e.g. 28/29 Feb, 30 Apr, 31 Dec)
			ultimoDiaMes := time.Date(parsedDate.Year(), parsedDate.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()

			for _, rev := range recurring {
				match := false
				switch rev.Frequencia {
				case "diario":
					match = true
				case "semanal":
					if rev.DiaSemana != nil && *rev.DiaSemana == weekday {
						match = true
					}
				case "mensal":
					if rev.DiaMes != nil {
						targetDay := *rev.DiaMes
						if targetDay > ultimoDiaMes {
							targetDay = ultimoDiaMes
						}
						if parsedDate.Day() == targetDay {
							match = true
						}
					}
				}

				if match {
					revCopy := rev
					if revCopy.HorarioTime != "" {
						revCopy.HorarioOficial = fmt.Sprintf("%s %s:00", dateStr, revCopy.HorarioTime)
					}
					list = append(list, revCopy)
				}
			}
		}
	}

	// Sort unified list by HorarioOficial ASC
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].HorarioOficial < list[j].HorarioOficial
	})

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
		SELECT id, titulo, conteudo, COALESCE(suma_executiva, ''), questionamento_socratico, status, criado_em, COALESCE(atualizado_em, criado_em)
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
		if err := rows.Scan(&i.ID, &i.Titulo, &i.Conteudo, &i.SumaExecutiva, &i.QuestionamentoSocratico, &i.Status, &i.CriadoEm, &i.AtualizadoEm); err != nil {
			return nil, err
		}
		list = append(list, i)
	}
	return list, nil
}

// SaveUserMemory persists a fact or preference about the user into long-term memory.
func (db *DB) SaveUserMemory(ctx context.Context, chave, valor string) error {
	chave = strings.ToLower(strings.TrimSpace(chave))
	valor = strings.TrimSpace(valor)
	if chave == "" || valor == "" {
		return fmt.Errorf("chave e valor de memoria nao podem ser vazios")
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO memoria_usuario (chave, valor, atualizado_em)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(chave) DO UPDATE SET valor = excluded.valor, atualizado_em = CURRENT_TIMESTAMP
	`, chave, valor)
	return err
}

// GetAllUserMemories returns all stored facts about the user.
func (db *DB) GetAllUserMemories(ctx context.Context) ([]MemoriaUsuario, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, chave, valor, atualizado_em
		FROM memoria_usuario
		ORDER BY chave ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []MemoriaUsuario
	for rows.Next() {
		var m MemoriaUsuario
		if err := rows.Scan(&m.ID, &m.Chave, &m.Valor, &m.AtualizadoEm); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, nil
}

// DeleteUserMemory removes a stored memory key.
func (db *DB) DeleteUserMemory(ctx context.Context, chave string) error {
	chave = strings.ToLower(strings.TrimSpace(chave))
	_, err := db.ExecContext(ctx, "DELETE FROM memoria_usuario WHERE chave = ?", chave)
	return err
}

// RecordTokenUsage logs tokens consumed by a request.
func (db *DB) RecordTokenUsage(ctx context.Context, provedor, modelo string, inTok, outTok, totalTok int) error {
	if totalTok <= 0 {
		totalTok = inTok + outTok
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO uso_tokens (provedor, modelo, tokens_entrada, tokens_saida, total_tokens)
		VALUES (?, ?, ?, ?, ?)
	`, provedor, modelo, inTok, outTok, totalTok)
	return err
}

// GetTokenStats calculates token consumption for today, this month, and per provider.
func (db *DB) GetTokenStats(ctx context.Context, todayStr, monthStr string) (*TokenStats, error) {
	stats := &TokenStats{}

	// Total today
	_ = db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total_tokens), 0)
		FROM uso_tokens
		WHERE DATE(criado_em) = DATE(?)
	`, todayStr).Scan(&stats.TotalHoje)

	// Total this month
	_ = db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total_tokens), 0)
		FROM uso_tokens
		WHERE strftime('%Y-%m', criado_em) = ?
	`, monthStr).Scan(&stats.TotalMes)

	// Groq total
	_ = db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total_tokens), 0)
		FROM uso_tokens
		WHERE provedor = 'groq'
	`).Scan(&stats.TotalGroq)

	// NVIDIA total
	_ = db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total_tokens), 0)
		FROM uso_tokens
		WHERE provedor = 'nvidia'
	`).Scan(&stats.TotalNv)

	return stats, nil
}

// GetPendingBuffer1Events returns events whose prep time has arrived and not yet notified.
func (db *DB) GetPendingBuffer1Events(ctx context.Context, nowStr string) ([]Evento, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM eventos
		WHERE notificado_buffer_1 = 0
		  AND horario_buffer_1 IS NOT NULL
		  AND horario_buffer_1 <= ?
		  AND status = 'pendente'
		  AND (frequencia IS NULL OR frequencia = 'pontual')
	`, selectEventoCols), nowStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := scanEvento(rows, &e); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, nil
}

// GetPendingBuffer2Events returns events whose departure time has arrived and not yet notified.
func (db *DB) GetPendingBuffer2Events(ctx context.Context, nowStr string) ([]Evento, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM eventos
		WHERE notificado_buffer_2 = 0
		  AND horario_buffer_2 IS NOT NULL
		  AND horario_buffer_2 <= ?
		  AND status = 'pendente'
		  AND (frequencia IS NULL OR frequencia = 'pontual')
	`, selectEventoCols), nowStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := scanEvento(rows, &e); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, nil
}

// GetPendingCompletionEvents returns events that finished >= 15 min ago to ask for confirmation.
func (db *DB) GetPendingCompletionEvents(ctx context.Context, thresholdStr string) ([]Evento, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM eventos
		WHERE notificado_conclusao = 0
		  AND horario_oficial <= ?
		  AND status = 'pendente'
		  AND (frequencia IS NULL OR frequencia = 'pontual')
		LIMIT 5
	`, selectEventoCols), thresholdStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := scanEvento(rows, &e); err != nil {
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
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM eventos
		WHERE status != 'concluido'
		ORDER BY horario_oficial ASC
		LIMIT ?
	`, selectEventoCols), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Evento
	for rows.Next() {
		var e Evento
		if err := scanEvento(rows, &e); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, nil
}

// GetMonthEventCounts calculates event count for every day of a given month.
func (db *DB) GetMonthEventCounts(ctx context.Context, year int, month time.Month) (map[int]int, error) {
	counts := make(map[int]int)
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()

	for d := 1; d <= lastDay; d++ {
		dateStr := fmt.Sprintf("%04d-%02d-%02d", year, int(month), d)
		evts, err := db.GetEventsForDay(ctx, dateStr)
		if err == nil && len(evts) > 0 {
			counts[d] = len(evts)
		}
	}
	return counts, nil
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

// ConfirmacaoPendente guarda uma confirmação pendente de substituição ou ação sensível.
type ConfirmacaoPendente struct {
	ChatID         int64  `json:"chat_id"`
	Tipo           string `json:"tipo"`
	EventoID       int64  `json:"evento_id"`
	NovoEventoJSON string `json:"novo_evento_json"`
	CriadoEm       string `json:"criado_em"`
}

// SavePendingConfirmation grava ou atualiza uma confirmação pendente para o chat.
func (db *DB) SavePendingConfirmation(ctx context.Context, chatID int64, tipo string, eventoID int64, novoEventoJSON string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO confirmacoes_pendentes (chat_id, tipo, evento_id, novo_evento_json, criado_em)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(chat_id) DO UPDATE SET
			tipo = excluded.tipo,
			evento_id = excluded.evento_id,
			novo_evento_json = excluded.novo_evento_json,
			criado_em = CURRENT_TIMESTAMP
	`, chatID, tipo, eventoID, novoEventoJSON)
	return err
}

// GetPendingConfirmation recupera a confirmação pendente do chat.
func (db *DB) GetPendingConfirmation(ctx context.Context, chatID int64) (*ConfirmacaoPendente, error) {
	var c ConfirmacaoPendente
	err := db.QueryRowContext(ctx, `
		SELECT chat_id, tipo, evento_id, novo_evento_json, criado_em
		FROM confirmacoes_pendentes
		WHERE chat_id = ?
	`, chatID).Scan(&c.ChatID, &c.Tipo, &c.EventoID, &c.NovoEventoJSON, &c.CriadoEm)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// DeletePendingConfirmation remove a confirmação pendente do chat.
func (db *DB) DeletePendingConfirmation(ctx context.Context, chatID int64) error {
	_, err := db.ExecContext(ctx, "DELETE FROM confirmacoes_pendentes WHERE chat_id = ?", chatID)
	return err
}

// GetEventoByID recupera um evento pelo ID.
func (db *DB) GetEventoByID(ctx context.Context, id int64) (*Evento, error) {
	row := db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM eventos
		WHERE id = ?
	`, selectEventoCols), id)

	var e Evento
	if err := row.Scan(
		&e.ID, &e.Descricao, &e.HorarioOficial, &e.HorarioBuffer1, &e.HorarioBuffer2,
		&e.NotificadoBuffer1, &e.NotificadoBuffer2, &e.NotificadoConclusao, &e.Status, &e.CriadoEm,
		&e.Frequencia, &e.DiaSemana, &e.DiaMes, &e.HorarioTime,
		&e.DeslocamentoMin, &e.PreparoMin,
		&e.UltimaNotificacaoB1, &e.UltimaNotificacaoB2, &e.UltimaConclusaoData,
	); err != nil {
		return nil, err
	}
	return &e, nil
}

// GetLatestPendingEvento recupera o evento pendente mais recentemente criado.
func (db *DB) GetLatestPendingEvento(ctx context.Context) (*Evento, error) {
	row := db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM eventos
		WHERE status = 'pendente'
		ORDER BY id DESC
		LIMIT 1
	`, selectEventoCols))

	var e Evento
	if err := row.Scan(
		&e.ID, &e.Descricao, &e.HorarioOficial, &e.HorarioBuffer1, &e.HorarioBuffer2,
		&e.NotificadoBuffer1, &e.NotificadoBuffer2, &e.NotificadoConclusao, &e.Status, &e.CriadoEm,
		&e.Frequencia, &e.DiaSemana, &e.DiaMes, &e.HorarioTime,
		&e.DeslocamentoMin, &e.PreparoMin,
		&e.UltimaNotificacaoB1, &e.UltimaNotificacaoB2, &e.UltimaConclusaoData,
	); err != nil {
		return nil, err
	}
	return &e, nil
}

// FindSimilarActiveEvento busca um evento pendente cuja descrição seja similar ao termo.
func (db *DB) FindSimilarActiveEvento(ctx context.Context, desc string) (*Evento, error) {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return nil, nil
	}
	events, err := db.GetUpcomingEvents(ctx, 50)
	if err != nil {
		return nil, err
	}
	lowerDesc := strings.ToLower(desc)
	for _, ev := range events {
		lowerEv := strings.ToLower(ev.Descricao)
		if lowerEv == lowerDesc || strings.Contains(lowerEv, lowerDesc) || strings.Contains(lowerDesc, lowerEv) {
			return &ev, nil
		}
	}
	return nil, nil
}
