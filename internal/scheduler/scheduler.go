package scheduler

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"atlas/internal/config"
	"atlas/internal/database"
	"atlas/internal/nlu"
	"atlas/internal/telegram"
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
				e.tick(ctx)
			}
		}
	}()
	log.Println("[Scheduler] Motor de Proatividade e Buffers iniciado.")
}

func (e *Engine) tick(ctx context.Context) {
	now := time.Now().In(e.cfg.Location)
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
