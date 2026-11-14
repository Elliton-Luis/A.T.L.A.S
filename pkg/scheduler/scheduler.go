package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"atlas/pkg/calculator"
	"atlas/pkg/catholic"
	"atlas/pkg/config"
	"atlas/pkg/database"
	"atlas/pkg/nlu"
	"atlas/pkg/synonyms"
	"atlas/pkg/telegram"
)

type MessageSender interface {
	SendMessage(context.Context, int64, string) error
	SendDocument(context.Context, int64, string, string) error
}

type Engine struct {
	cfg           *config.Config
	db            *database.DB
	tg            MessageSender
	nluClient     *nlu.Client
	lastBackup    string
	tickMu        sync.Mutex
	ExecuteAction func(context.Context, ActionRoutine) (string, error)
}

func NewEngine(cfg *config.Config, db *database.DB, tg MessageSender, nluClient *nlu.Client) *Engine {
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
	e.tickMu.Lock()
	defer e.tickMu.Unlock()
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

	// 3. Process Financial Bill Reminders (Continuous 3-day countdown: 3, 2, 1, 0 days)
	e.processBillReminders(ctx, now)

	// 4. Process Daily Offsite SQLite Backup
	e.processDailyBackup(ctx, todayStr, timeHM)
}

func (e *Engine) processBillReminders(ctx context.Context, now time.Time) {
	alerts, err := e.db.GetPendingBillAlerts(ctx, now)
	if err != nil || len(alerts) == 0 {
		return
	}

	todayStr := now.Format("2006-01-02")
	for _, a := range alerts {
		var title string
		var icon string
		switch a.DiasRestantes {
		case 3:
			icon = "💳"
			title = "Aviso de Vencimento (Faltam 3 dias)"
		case 2:
			icon = "💳"
			title = "Aviso de Vencimento (Faltam 2 dias)"
		case 1:
			icon = "⏳"
			title = "Aviso Urgente (Vence Amanhã!)"
		case 0:
			icon = "🚨"
			title = "VENCE HOJE! — Alerta de Pagamento"
		}

		valorStr := "Não informado"
		if a.Conta.Valor > 0 {
			valorStr = fmt.Sprintf("R$ %.2f", a.Conta.Valor)
		}

		dataVencStr := a.DataVenc.Format("02/01/2006")
		diaSemana := diasSemanaPt[a.DataVenc.Weekday()]

		msg := fmt.Sprintf("%s *%s*\n\n📌 *Conta:* %s\n📅 *Vencimento:* %s (%s)\n💰 *Valor:* %s\n\n_Já pagou? Responda: 'paguei %s' para registrar._",
			icon, title, a.Conta.Descricao, dataVencStr, diaSemana, valorStr, a.Conta.Descricao)

		if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, msg); err == nil {
			_ = e.db.MarkContaAlertSent(ctx, a.Conta.ID, todayStr)
			log.Printf("[Scheduler] Alerta de conta enviado: %s (faltam %d dias)", a.Conta.Descricao, a.DiasRestantes)
		}
	}
}

var diasSemanaPt = map[time.Weekday]string{
	time.Sunday:    "Domingo",
	time.Monday:    "Segunda-feira",
	time.Tuesday:   "Terça-feira",
	time.Wednesday: "Quarta-feira",
	time.Thursday:  "Quinta-feira",
	time.Friday:    "Sexta-feira",
	time.Saturday:  "Sábado",
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
		completed, err := e.db.CompletedEventIDs(ctx, todayStr)
		if err != nil {
			log.Printf("[Scheduler] Consulta de conclusões: %v", err)
			return
		}
		weekday := int(now.Weekday())
		loc := now.Location()
		ultimoDiaMes := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, loc).Day()

		for _, rev := range recurringEvents {
			if completed[rev.ID] {
				continue
			}
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
			if desloc < 0 {
				desloc = 25
			}
			prep := rev.PreparoMin
			if prep < 0 {
				prep = 35
			}

			buffers := calculator.Calculate(oficialToday, desloc, prep, calculator.DefaultDelayMargin)

			// Buffer 1 (Preparo)
			if prep > 0 && !now.Before(buffers.BufferPrep) && (rev.UltimaNotificacaoB1 == nil || *rev.UltimaNotificacaoB1 != todayStr) {
				if now.Before(oficialToday.Add(30 * time.Minute)) {
					msg := fmt.Sprintf("🛁 *Alerta de Preparo! (%s • +15%% margem)*\n\nHora de começar a se arrumar para:\n👉 *%s*\n⏰ Marcado para hoje às: *%02d:%02d*", freqLabel, rev.Descricao, hh, mm)
					if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, msg); err == nil {
						_ = e.db.MarkRecurringB1Notified(ctx, rev.ID, todayStr)
					}
				}
			}

			// Buffer 2 (Saída)
			if desloc > 0 && !now.Before(buffers.BufferLeave) && (rev.UltimaNotificacaoB2 == nil || *rev.UltimaNotificacaoB2 != todayStr) {
				if now.Before(oficialToday.Add(30 * time.Minute)) {
					msg := fmt.Sprintf("🚗 *Alerta de Saída! (%s • +15%% margem)*\n\nHora de sair de casa para:\n👉 *%s*\n⏰ Marcado para hoje às: *%02d:%02d*", freqLabel, rev.Descricao, hh, mm)
					if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, msg); err == nil {
						_ = e.db.MarkRecurringB2Notified(ctx, rev.ID, todayStr)
					}
				}
			}

			// Auditoria Pós-Evento (15 min após o horário oficial)
			if !now.Before(oficialToday.Add(15*time.Minute)) && (rev.UltimaConclusaoData == nil || *rev.UltimaConclusaoData != todayStr) {
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

	day, err := time.ParseInLocation("2006-01-02", todayStr, e.cfg.Location)
	if err != nil {
		return
	}
	current, err := time.Parse("15:04", timeHM)
	if err != nil {
		return
	}
	for _, r := range routines {
		scheduled, err := time.Parse("15:04", r.Horario)
		if err != nil {
			log.Printf("[Scheduler] Horário inválido na rotina %d: %s", r.ID, r.Horario)
			continue
		}
		delay := current.Sub(scheduled)
		// Recover missed ticks for up to two hours, without sending stale daily work.
		if delay >= 0 && delay <= 2*time.Hour && routineMatchesDay(r, day) && (r.UltimaExecucao == nil || *r.UltimaExecucao != todayStr) {
			e.executeRoutine(ctx, r, todayStr)
		}
	}
}

func (e *Engine) executeRoutine(ctx context.Context, r database.RotinaProativa, todayStr string) {
	key := fmt.Sprintf("rotina:%d:%s", r.ID, todayStr)
	if r.Tipo == "briefing" {
		key = "briefing:" + todayStr
	}
	if action, err := decodeAction(r); err == nil && action.Action == "briefing" {
		key = "briefing:" + todayStr
	}
	claimed, err := e.db.ClaimRoutineExecution(ctx, key)
	if err != nil {
		log.Printf("[Scheduler] Reserva %s: %v", key, err)
		return
	}
	if !claimed {
		return
	}

	var message string

	switch r.Tipo {
	case "briefing":
		message, err = e.BuildBriefing(ctx, todayStr)

	case "checkin":
		events, eventErr := e.db.GetEventsForDay(ctx, todayStr)
		goals, goalErr := e.db.GetActiveGoals(ctx, 100)
		if eventErr != nil {
			err = eventErr
			break
		}
		if goalErr != nil {
			err = goalErr
			break
		}
		message = fmt.Sprintf("⚡ *ATLAS — Ponto de Situação*\n\nAgenda de hoje: %d compromisso(s) e %d meta(s) ativas. Como está o andamento das prioridades?", len(events), len(goals))

	case "review":
		events, eventErr := e.db.GetEventsForDay(ctx, todayStr)
		goals, goalErr := e.db.GetActiveGoals(ctx, 100)
		if eventErr != nil {
			err = eventErr
			break
		}
		if goalErr != nil {
			err = goalErr
			break
		}
		if e.nluClient == nil {
			err = fmt.Errorf("IA não configurada para review")
			break
		}
		aiReview, err := e.nluClient.GenerateReview(ctx, todayStr, events, goals)
		if err == nil && aiReview != "" {
			message = fmt.Sprintf("🌙 *ATLAS — Fechamento do Dia*\n\n%s", aiReview)
		} else {
			message = fmt.Sprintf("🌙 *ATLAS — Fechamento do Dia*\n\nO dia está encerrando. Suas tarefas foram cumpridas ou você tem justificativas para a procrastinação de hoje?")
		}

	case "action":
		var action ActionRoutine
		action, err = decodeAction(r)
		if err == nil {
			if e.ExecuteAction == nil {
				err = fmt.Errorf("executor de ações indisponível")
			} else {
				message, err = e.ExecuteAction(ctx, action)
			}
		}
	case "custom":
		legacy, _ := decodeAction(r)
		if legacy.Action != "" {
			if e.ExecuteAction == nil {
				err = fmt.Errorf("executor de ações indisponível")
			} else {
				message, err = e.ExecuteAction(ctx, legacy)
			}
		} else if strings.Contains(r.Nome, "Liturgia das Horas") {
			hourQuery := "auto"
			if strings.Contains(r.Nome, "Laudes") {
				hourQuery = "Laudes"
			} else if strings.Contains(r.Nome, "Vésperas") || strings.Contains(r.Nome, "Vesperas") {
				hourQuery = "Vésperas"
			} else if strings.Contains(r.Nome, "Completas") {
				hourQuery = "Completas"
			} else if strings.Contains(r.Nome, "Ofício") || strings.Contains(r.Nome, "Oficio") {
				hourQuery = "Ofício das Leituras"
			}
			message, err = catholic.FetchLiturgiaDasHoras(ctx, hourQuery, e.cfg.Location)
		} else if r.PromptCustom != nil && *r.PromptCustom != "" {
			message = *r.PromptCustom
		} else {
			message = fmt.Sprintf("🔔 *Alerta de Rotina: %s*", r.Nome)
		}
	default:
		err = fmt.Errorf("tipo de rotina desconhecido: %s", r.Tipo)
	}

	if err != nil || message == "" {
		log.Printf("[Scheduler] Falha na rotina %d (%s): %v", r.ID, r.Nome, err)
		if releaseErr := e.db.SetExecutionState(ctx, key, "falhou"); releaseErr != nil {
			log.Printf("[Scheduler] Liberação %s: %v", key, releaseErr)
		}
		return
	}
	if message != "" {
		if err := e.db.SetExecutionState(ctx, key, "enviando"); err != nil {
			log.Printf("[Scheduler] Não foi possível reservar entrega %s: %v", key, err)
			return
		}
		if err := e.tg.SendMessage(ctx, e.cfg.TelegramChatID, message); err == nil {
			if err := e.db.FinishExecution(ctx, key); err != nil {
				log.Printf("[Scheduler] Registro de entrega %s: %v", key, err)
			}
			if err := e.db.MarkRoutineExecuted(ctx, r.ID, todayStr); err != nil {
				log.Printf("[Scheduler] Registro da rotina %d: %v", r.ID, err)
			}
			log.Printf("[Scheduler] Rotina executada com sucesso: %s", r.Nome)
		} else {
			// Delivery may have succeeded before a network timeout. Keep the reservation
			// rather than automatically duplicating an ambiguous Telegram delivery.
			log.Printf("[Scheduler] Entrega não confirmada %s: %v", key, err)
			state := "incerta"
			var rejected *telegram.RejectedError
			if errors.As(err, &rejected) {
				state = "falhou"
			}
			if err := e.db.SetExecutionState(ctx, key, state); err != nil {
				log.Printf("[Scheduler] Falha ao registrar estado %s: %v", key, err)
			}
		}
	}
}

func decodeAction(r database.RotinaProativa) (ActionRoutine, error) {
	var action ActionRoutine
	if r.Tipo == "custom" && r.PromptCustom != nil {
		// Preserve literal reminders; upgrade only exact legacy tool labels.
		label := strings.TrimSpace(synonyms.RemoveAccents(strings.ToLower(*r.PromptCustom)))
		switch strings.TrimPrefix(label, "/") {
		case "liturgia", "liturgia diaria", "liturgia de hoje", "liturgia do dia":
			action.Action = "liturgia"
		case "briefing", "briefing matinal":
			action.Action = "briefing"
		case "emails", "email":
			action.Action = "ler_emails"
		}
		return action, nil
	}
	if r.Tipo != "action" {
		return action, nil
	}
	if r.PromptCustom == nil {
		return action, fmt.Errorf("rotina sem ação")
	}
	err := json.Unmarshal([]byte(*r.PromptCustom), &action)
	if err == nil && action.Action == "" {
		err = fmt.Errorf("ação vazia")
	}
	return action, err
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
