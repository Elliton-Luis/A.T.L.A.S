package scheduler

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"atlas/pkg/calculator"
	"atlas/pkg/config"
	"atlas/pkg/database"
	"atlas/pkg/nlu"
	"atlas/pkg/telegram"
)

type Engine struct {
	cfg        *config.Config
	db         *database.DB
	tg         *telegram.Client
	nluClient  *nlu.Client
	lastBackup string
}

func NewEngine(cfg *config.Config, db *database.DB, tg *telegram.Client, nluClient *nlu.Client) *Engine {
	return &Engine{
		cfg:       cfg,
		db:        db,
		tg:        tg,
		nluClient: nluClient,
	}
}

// Start runs the proactive scheduler loops in background goroutines until context is cancelled.
func (e *Engine) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.Tick(ctx)
			}
		}
	}()
	log.Println("[Scheduler] Motor de Proatividade e Buffers iniciado.")
}

// Tick executes a single scheduled evaluation pass (buffers, proactive routines, backups).
// Can be called by internal goroutines or by an external serverless cron webhook (e.g. cron-job.org).
func (e *Engine) Tick(ctx context.Context) {
	loc := e.cfg.Location
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	nowStr := database.FormatDateTime(now)
	todayStr := now.Format("2006-01-02")
	timeHM := now.Format("15:04")

	if e.cfg.TelegramChatID == 0 {
		return
	}

	// 1. Process Event Buffers (+15% margin alerts)
	e.processEventBuffers(ctx, now, nowStr)

	// 2. Process Proactive Routines (User customized schedules)
	e.processProactiveRoutines(ctx, todayStr, timeHM)

	// 3. Process Daily Offsite SQLite Backup
	e.processDailyBackup(ctx, todayStr, timeHM)
}

func (e *Engine) processEventBuffers(ctx context.Context, now time.Time, nowStr string) {
	// --- A) EVENTOS PONTUAIS ---
	// Buffer 1: Prep / Shower Alert
	prepEvents, err := e.db.GetPendingBuffer1Events(ctx, nowStr)
	if err == nil {
		for _, ev := range prepEvents {
			msg := fmt.Sprintf("🛁 *Alerta de Preparo! (+15%% margem)*\n\nHora de começar a se arrumar para:\n👉 *%s*\n⏰ Marcado para: *%s*", ev.Descricao, ev.HorarioOficial)
			if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, msg); err == nil {
				_ = e.db.MarkBuffer1Notified(ctx, ev.ID)
			}
		}
	}

	// Buffer 2: Departure / Leave House Alert
	leaveEvents, err := e.db.GetPendingBuffer2Events(ctx, nowStr)
	if err == nil {
		for _, ev := range leaveEvents {
			msg := fmt.Sprintf("🚗 *Alerta de Saída! (+15%% margem)*\n\nHora de sair de casa para:\n👉 *%s*\n⏰ Marcado para: *%s*", ev.Descricao, ev.HorarioOficial)
			if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, msg); err == nil {
				_ = e.db.MarkBuffer2Notified(ctx, ev.ID)
			}
		}
	}

	// Post-event follow-up (15 minutes after official time)
	thresholdStr := database.FormatDateTime(now.Add(-15 * time.Minute))
	compEvents, err := e.db.GetPendingCompletionEvents(ctx, thresholdStr)
	if err == nil {
		for _, ev := range compEvents {
			msg := fmt.Sprintf("⏱️ *Compromisso Finalizado*\n\nO evento *%s* (agendado para %s) encerrou. Deseja marcar como concluído ou precisa de algum ajuste?", ev.Descricao, ev.HorarioOficial)
			if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, msg); err == nil {
				_ = e.db.MarkCompletionNotified(ctx, ev.ID)
			}
		}
	}

	// --- B) EVENTOS RECORRENTES (DIÁRIOS, SEMANAIS, MENSAIS) COM ZERO TOKENS ---
	recurringEvents, err := e.db.GetActiveRecurringEvents(ctx)
	if err == nil && len(recurringEvents) > 0 {
		todayStr := now.Format("2006-01-02")
		weekday := int(now.Weekday())
		loc := now.Location()
		ultimoDiaMes := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, loc).Day()

		for _, rev := range recurringEvents {
			match := false
			freqLabel := ""
			switch rev.Frequencia {
			case "diario":
				match = true
				freqLabel = "Diário"
			case "semanal":
				if rev.DiaSemana != nil && *rev.DiaSemana == weekday {
					match = true
					freqLabel = "Semanal"
				}
			case "mensal":
				if rev.DiaMes != nil {
					targetDay := *rev.DiaMes
					if targetDay > ultimoDiaMes {
						targetDay = ultimoDiaMes
					}
					if now.Day() == targetDay {
						match = true
						freqLabel = "Mensal"
					}
				}
			}

			if !match || rev.HorarioTime == "" {
				continue
			}

			parts := strings.Split(rev.HorarioTime, ":")
			if len(parts) < 2 {
				continue
			}
			hh, _ := strconv.Atoi(parts[0])
			mm, _ := strconv.Atoi(parts[1])
			oficialToday := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, loc)

			desloc := rev.DeslocamentoMin
			if desloc <= 0 {
				desloc = 25
			}
			prep := rev.PreparoMin
			if prep <= 0 {
				prep = 35
			}

			buffers := calculator.Calculate(oficialToday, desloc, prep, calculator.DefaultDelayMargin)

			// Buffer 1 (Preparo)
			if !now.Before(buffers.BufferPrep) && (rev.UltimaNotificacaoB1 == nil || *rev.UltimaNotificacaoB1 != todayStr) {
				if now.Before(oficialToday.Add(30 * time.Minute)) {
					msg := fmt.Sprintf("🛁 *Alerta de Preparo! (%s • +15%% margem)*\n\nHora de começar a se arrumar para:\n👉 *%s*\n⏰ Marcado para hoje às: *%02d:%02d*", freqLabel, rev.Descricao, hh, mm)
					if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, msg); err == nil {
						_ = e.db.MarkRecurringB1Notified(ctx, rev.ID, todayStr)
					}
				}
			}

			// Buffer 2 (Saída)
			if !now.Before(buffers.BufferLeave) && (rev.UltimaNotificacaoB2 == nil || *rev.UltimaNotificacaoB2 != todayStr) {
				if now.Before(oficialToday.Add(30 * time.Minute)) {
					msg := fmt.Sprintf("🚗 *Alerta de Saída! (%s • +15%% margem)*\n\nHora de sair de casa para:\n👉 *%s*\n⏰ Marcado para hoje às: *%02d:%02d*", freqLabel, rev.Descricao, hh, mm)
					if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, msg); err == nil {
						_ = e.db.MarkRecurringB2Notified(ctx, rev.ID, todayStr)
					}
				}
			}

			// Auditoria Pós-Evento (15 min após o horário oficial)
			if !now.Before(oficialToday.Add(15 * time.Minute)) && (rev.UltimaConclusaoData == nil || *rev.UltimaConclusaoData != todayStr) {
				if now.Before(oficialToday.Add(2 * time.Hour)) {
					msg := fmt.Sprintf("⏱️ *Compromisso Recorrente (%s)*\n\nO evento *%s* (programado para às %02d:%02d) foi concluído com sucesso hoje?", freqLabel, rev.Descricao, hh, mm)
					if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, msg); err == nil {
						_ = e.db.MarkRecurringCompletionNotified(ctx, rev.ID, todayStr)
					}
				}
			}
		}
	}
}

func (e *Engine) processProactiveRoutines(ctx context.Context, todayStr, timeHM string) {
	routines, err := e.db.GetActiveRoutines(ctx)
	if err != nil {
		return
	}

	for _, r := range routines {
		// Check if scheduled for this minute and hasn't run today
		if r.Horario == timeHM && (r.UltimaExecucao == nil || *r.UltimaExecucao != todayStr) {
			e.executeRoutine(ctx, r, todayStr)
		}
	}
}

func (e *Engine) executeRoutine(ctx context.Context, r database.RotinaProativa, todayStr string) {
	events, _ := e.db.GetEventsForDay(ctx, todayStr)
	goals, _ := e.db.GetActiveGoals(ctx, 5)

	var message string

	switch r.Tipo {
	case "briefing":
		aiBriefing, err := e.nluClient.GenerateBriefing(ctx, todayStr, events, goals)
		if err == nil && aiBriefing != "" {
			message = fmt.Sprintf("☀️ *ATLAS — Briefing Matinal*\n\n%s", aiBriefing)
		} else {
			message = fmt.Sprintf("☀️ *ATLAS — Briefing Matinal*\n\n📅 Compromissos de hoje: %d\n🎯 Metas em andamento: %d\n\nQual é a sua prioridade absoluta hoje?", len(events), len(goals))
		}

	case "checkin":
		message = fmt.Sprintf("⚡ *ATLAS — Ponto de Situação*\n\nAgenda de hoje: %d compromisso(s) e %d meta(s) ativas. Como está o andamento das prioridades?", len(events), len(goals))

	case "review":
		aiReview, err := e.nluClient.GenerateReview(ctx, todayStr, events, goals)
		if err == nil && aiReview != "" {
			message = fmt.Sprintf("🌙 *ATLAS — Fechamento do Dia*\n\n%s", aiReview)
		} else {
			message = fmt.Sprintf("🌙 *ATLAS — Fechamento do Dia*\n\nO dia está encerrando. Suas tarefas foram cumpridas ou você tem justificativas para a procrastinação de hoje?")
		}

	case "custom":
		if r.PromptCustom != nil && *r.PromptCustom != "" {
			message = *r.PromptCustom
		} else {
			message = fmt.Sprintf("🔔 *Alerta de Rotina: %s*", r.Nome)
		}
	}

	if message != "" {
		if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, message); err == nil {
			_ = e.db.MarkRoutineExecuted(ctx, r.ID, todayStr)
			log.Printf("[Scheduler] Rotina executada com sucesso: %s", r.Nome)
		}
	}
}

func (e *Engine) processDailyBackup(ctx context.Context, todayStr, timeHM string) {
	if e.cfg.TursoDatabaseURL != "" {
		return // Cloud database with automated remote snapshots
	}

	backupHour := e.cfg.BackupHour
	if backupHour == "" {
		backupHour = "03:00"
	}

	if timeHM == backupHour && e.lastBackup != todayStr {
		e.lastBackup = todayStr
		caption := fmt.Sprintf("🛡️ *Backup Automático ATLAS*\nData: %s\nBanco: %s", todayStr, filepath.Base(e.cfg.DBPath))
		if err := e.tg.SendDocument(ctx, e.cfg.TelegramChatID, e.cfg.DBPath, caption); err != nil {
			log.Printf("[Backup] Falha ao enviar backup: %v", err)
		} else {
			log.Printf("[Backup] Backup diário enviado via Telegram com sucesso.")
		}
	}
}
