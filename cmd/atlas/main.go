package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"atlas/internal/calculator"
	"atlas/internal/config"
	"atlas/internal/database"
	"atlas/internal/nlu"
	"atlas/internal/scheduler"
	"atlas/internal/stt"
	"atlas/internal/telegram"
)

type App struct {
	cfg       *config.Config
	db        *database.DB
	tg        *telegram.Client
	sttClient *stt.Client
	nluClient *nlu.Client
}

func main() {
	log.Println("[ATLAS] Inicializando assistente pessoal executivo...")

	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("[FATAL] Erro ao carregar configurações: %v", err)
	}

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("[FATAL] Erro ao abrir SQLite: %v", err)
	}
	defer db.Close()

	tg := telegram.NewClient(cfg.TelegramBotToken)
	sttClient := stt.NewClient(cfg.GroqAPIKey)
	nluClient := nlu.NewClient(cfg.NvidiaAPIKey, cfg.NvidiaModel)

	app := &App{
		cfg:       cfg,
		db:        db,
		tg:        tg,
		sttClient: sttClient,
		nluClient: nluClient,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Inicia o motor de proatividade, buffers e backups
	engine := scheduler.NewEngine(cfg, db, tg, nluClient)
	engine.Start(ctx)

	// Rotas HTTP
	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleRoot)
	mux.HandleFunc("/health", app.handleHealth)
	mux.HandleFunc("/webhook", app.handleWebhook)

	server := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[ATLAS] Servidor HTTP operando na porta :%s (Timezone: %s)\n", cfg.HTTPPort, cfg.Timezone)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Erro no servidor HTTP: %v", err)
		}
	}()

	<-sigChan
	log.Println("[ATLAS] Encerrando com segurança...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	log.Println("[ATLAS] Finalizado com sucesso.")
}

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ATLAS — Assistente Pessoal Executivo e Socrático (Go)"))
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"online","system":"atlas"}`))
}

func (a *App) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Validação de Segurança do Token Secreto do Webhook
	if a.cfg.TelegramSecretToken != "" {
		receivedToken := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if subtle.ConstantTimeCompare([]byte(a.cfg.TelegramSecretToken), []byte(receivedToken)) != 1 {
			log.Println("[Segurança] Tentativa de acesso ao webhook com secret token inválido.")
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	var update telegram.Update
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Se não for mensagem, responde 200 e finaliza
	if update.Message == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
		return
	}

	msg := update.Message
	chatID := msg.Chat.ID

	// 2. Validação Estrita de Whitelist do Usuário (Nada pode vazar)
	if a.cfg.TelegramChatID != 0 && chatID != a.cfg.TelegramChatID {
		log.Printf("[Segurança] Mensagem ignorada de chat_id não autorizado: %d\n", chatID)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
		return
	}

	// Responde OK imediatamente ao Telegram e processa de forma síncrona/rápida com timeout
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	a.processIncomingMessage(ctx, msg)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (a *App) processIncomingMessage(ctx context.Context, msg *telegram.Message) {
	chatID := msg.Chat.ID
	var inputText string

	// 1. Extração de Texto ou Voz (com Groq Whisper)
	if msg.Text != "" {
		inputText = strings.TrimSpace(msg.Text)
	} else if msg.Voice != nil && msg.Voice.FileID != "" {
		tmpPath, err := a.tg.DownloadVoiceFile(ctx, msg.Voice.FileID)
		if err != nil {
			log.Printf("[Erro] Download de áudio falhou: %v\n", err)
			_ = a.tg.SendMessage(ctx, chatID, "⚠️ Erro ao receber áudio.")
			return
		}
		defer os.Remove(tmpPath)

		transcription, err := a.sttClient.Transcribe(ctx, tmpPath)
		if err != nil {
			log.Printf("[Erro] Transcrição falhou: %v\n", err)
			_ = a.tg.SendMessage(ctx, chatID, "⚠️ Erro na transcrição de voz.")
			return
		}
		inputText = strings.TrimSpace(transcription)
	}

	if inputText == "" {
		return
	}

	// 2. Comandos Diretos (/rotinas, /hoje, /metas, /ideias, /ajuda)
	if strings.HasPrefix(inputText, "/") {
		a.handleCommand(ctx, chatID, inputText)
		return
	}

	// 3. Processamento Cognitivo e Socrático via NLU (NVIDIA NIM)
	now := time.Now().In(a.cfg.Location)
	currentDatetime := database.FormatDateTime(now)

	// Registra no histórico para janela deslizante
	_ = a.db.RecordHistory(ctx, "user", inputText)

	history, _ := a.db.GetRecentHistory(ctx, 6)

	intent, err := a.nluClient.ParseIntent(ctx, inputText, currentDatetime, history)
	if err != nil {
		log.Printf("[Erro NLU] %v\n", err)
		reply := "⚠️ Ocorreu uma oscilação no processamento do seu pensamento. Repita de forma mais concisa."
		_ = a.tg.SendMessage(ctx, chatID, reply)
		_ = a.db.RecordHistory(ctx, "assistant", reply)
		return
	}

	var reply string

	switch intent.Tipo {
	case "ideia":
		_, err := a.db.InsertIdeia(ctx, intent.Titulo, intent.Conteudo, intent.PerguntaSocratica)
		if err != nil {
			log.Printf("[Erro DB Ideia] %v\n", err)
			reply = "⚠️ Falha ao registrar ideia no banco."
		} else {
			reply = fmt.Sprintf("💡 *Ideia Anotada:* %s\n\n⚔️ *Desafio Socrático:*\n%s", intent.Titulo, intent.PerguntaSocratica)
		}

	case "evento":
		parsedTime, err := parseTimeFlexible(intent.HorarioOficial, a.cfg.Location)
		if err != nil {
			reply = fmt.Sprintf("⚠️ Não compreendi o horário '%s'. Especifique no formato YYYY-MM-DD HH:MM.", intent.HorarioOficial)
		} else {
			deslocamento := intent.EstimativaDeslocamentoMin
			if deslocamento <= 0 {
				deslocamento = 25
			}
			preparo := intent.EstimativaPreparoMin
			if preparo <= 0 {
				preparo = 35
			}

			buffers := calculator.Calculate(parsedTime, deslocamento, preparo, calculator.DefaultDelayMargin)
			b1Str := database.FormatDateTime(buffers.BufferPrep)
			b2Str := database.FormatDateTime(buffers.BufferLeave)
			oficialStr := database.FormatDateTime(buffers.OfficialTime)

			_, err = a.db.InsertEvento(ctx, intent.Descricao, oficialStr, b1Str, b2Str)
			if err != nil {
				log.Printf("[Erro DB Evento] %v\n", err)
				reply = "⚠️ Falha ao agendar evento."
			} else {
				reply = fmt.Sprintf("🎯 *Compromisso Agendado (+15%% margem determinística)*\n\n📌 *Evento:* %s\n⏰ *Oficial:* %s\n🛁 *Preparo:* %s\n🚗 *Saída:* %s\n🛡️ *Margem:* +%d min de segurança",
					intent.Descricao, oficialStr, b1Str, b2Str, buffers.MarginMinutes)
			}
		}

	case "objetivo":
		var prazoPtr *string
		if intent.Prazo != "" {
			prazoPtr = &intent.Prazo
		}
		_, err := a.db.InsertObjetivo(ctx, intent.Titulo, prazoPtr)
		if err != nil {
			log.Printf("[Erro DB Objetivo] %v\n", err)
			reply = "⚠️ Falha ao registrar objetivo."
		} else {
			prazoInfo := ""
			if prazoPtr != nil {
				prazoInfo = fmt.Sprintf(" (Prazo: %s)", *prazoPtr)
			}
			reply = fmt.Sprintf("🎯 *Meta Registrada:* %s%s\n\nQual é o primeiro passo concreto hoje?", intent.Titulo, prazoInfo)
		}

	default: // "conversa"
		reply = intent.Resposta
		if reply == "" {
			reply = "O que exatamente você pretende alcançar com essa premissa?"
		}
	}

	_ = a.tg.SendMessage(ctx, chatID, reply)
	_ = a.db.RecordHistory(ctx, "assistant", reply)
}

func (a *App) handleCommand(ctx context.Context, chatID int64, cmdText string) {
	parts := strings.Fields(cmdText)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/start", "/ajuda", "/help":
		msg := `🤖 *ATLAS — Painel de Controle*

Eu sou o seu assistente executivo e socrático. Comandos disponíveis:

• 📋 */rotinas* — Ver e gerenciar rotinas proativas de mensagens
• ⏰ */rotina <id> horario <HH:MM>* — Alterar horário de uma rotina
• 🔄 */rotina <id> toggle* — Ativar/Desativar uma rotina
• 📅 */hoje* — Ver agenda de hoje com buffers de atraso (+15%)
• 🎯 */metas* — Ver objetivos e metas ativas
• 💡 */ideias* — Ver ideias registradas e questionamentos
• 🛡️ */backup* — Disparar backup imediato do banco

Você também pode falar comigo por *texto ou áudio* naturalmente. Eu identifico compromissos, metas, ideias e questiono seus planos.`
		_ = a.tg.SendMessage(ctx, chatID, msg)

	case "/rotinas":
		routines, err := a.db.GetAllRoutines(ctx)
		if err != nil || len(routines) == 0 {
			_ = a.tg.SendMessage(ctx, chatID, "Nenhuma rotina cadastrada.")
			return
		}

		var sb strings.Builder
		sb.WriteString("📋 *Rotinas Proativas Configuradas:*\n\n")
		for _, r := range routines {
			status := "🟢 Ativa"
			if r.Ativo == 0 {
				status = "🔴 Inativa"
			}
			sb.WriteString(fmt.Sprintf("• *[%d]* `%s` — ⏰ *%s* (%s)\n", r.ID, r.Nome, r.Horario, status))
		}
		sb.WriteString("\n_Use `/rotina <id> horario HH:MM` para mudar o horário ou `/rotina <id> toggle` para ativar/desativar._")
		_ = a.tg.SendMessage(ctx, chatID, sb.String())

	case "/rotina":
		if len(parts) >= 3 && strings.ToLower(parts[2]) == "toggle" {
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				_ = a.tg.SendMessage(ctx, chatID, "ID inválido.")
				return
			}
			active, err := a.db.ToggleRoutine(ctx, id)
			if err != nil {
				_ = a.tg.SendMessage(ctx, chatID, "Erro ao atualizar rotina.")
				return
			}
			statusStr := "desativada 🔴"
			if active {
				statusStr = "ativada 🟢"
			}
			_ = a.tg.SendMessage(ctx, chatID, fmt.Sprintf("Rotina [%d] %s.", id, statusStr))
			return
		}

		if len(parts) >= 4 && strings.ToLower(parts[2]) == "horario" {
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				_ = a.tg.SendMessage(ctx, chatID, "ID inválido.")
				return
			}
			newTime := parts[3]
			if _, err := time.Parse("15:04", newTime); err != nil {
				_ = a.tg.SendMessage(ctx, chatID, "Formato de hora inválido. Use HH:MM (ex: 08:30).")
				return
			}
			if err := a.db.UpdateRoutineTime(ctx, id, newTime); err != nil {
				_ = a.tg.SendMessage(ctx, chatID, "Erro ao alterar horário.")
				return
			}
			_ = a.tg.SendMessage(ctx, chatID, fmt.Sprintf("Horário da rotina [%d] atualizado para *%s* ⏰.", id, newTime))
			return
		}

		_ = a.tg.SendMessage(ctx, chatID, "Uso: `/rotina <id> horario HH:MM` ou `/rotina <id> toggle`")

	case "/hoje":
		now := time.Now().In(a.cfg.Location)
		todayStr := now.Format("2006-01-02")
		events, err := a.db.GetEventsForDay(ctx, todayStr)
		if err != nil || len(events) == 0 {
			_ = a.tg.SendMessage(ctx, chatID, fmt.Sprintf("📅 Não há compromissos agendados para hoje (%s).", todayStr))
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📅 *Agenda de Hoje (%s):*\n\n", todayStr))
		for _, ev := range events {
			sb.WriteString(fmt.Sprintf("📌 *%s*\n  ⏰ Oficial: %s\n  🛁 Preparo: %s\n  🚗 Saída: %s\n\n",
				ev.Descricao, ev.HorarioOficial, ev.HorarioBuffer1, ev.HorarioBuffer2))
		}
		_ = a.tg.SendMessage(ctx, chatID, sb.String())

	case "/metas":
		goals, err := a.db.GetActiveGoals(ctx, 10)
		if err != nil || len(goals) == 0 {
			_ = a.tg.SendMessage(ctx, chatID, "🎯 Nenhuma meta ativa cadastrada.")
			return
		}

		var sb strings.Builder
		sb.WriteString("🎯 *Metas e Objetivos Ativos:*\n\n")
		for _, g := range goals {
			prazo := "Sem prazo definido"
			if g.Prazo != nil && *g.Prazo != "" {
				prazo = *g.Prazo
			}
			sb.WriteString(fmt.Sprintf("• *%s*\n  ⏳ Prazo: %s | Progresso: %d%%\n\n", g.Titulo, prazo, g.ProgressoPct))
		}
		_ = a.tg.SendMessage(ctx, chatID, sb.String())

	case "/ideias":
		ideas, err := a.db.GetRecentIdeas(ctx, 5)
		if err != nil || len(ideas) == 0 {
			_ = a.tg.SendMessage(ctx, chatID, "💡 Nenhuma ideia registrada recentemente.")
			return
		}

		var sb strings.Builder
		sb.WriteString("💡 *Últimas Ideias Registradas:*\n\n")
		for _, i := range ideas {
			sb.WriteString(fmt.Sprintf("• *%s*\n  📝 %s\n  ⚔️ _%s_\n\n", i.Titulo, i.Conteudo, i.QuestionamentoSocratico))
		}
		_ = a.tg.SendMessage(ctx, chatID, sb.String())

	case "/backup":
		caption := fmt.Sprintf("🛡️ Backup manual solicitado em %s", time.Now().In(a.cfg.Location).Format("2006-01-02 15:04:05"))
		if err := a.tg.SendDocument(ctx, chatID, a.cfg.DBPath, caption); err != nil {
			_ = a.tg.SendMessage(ctx, chatID, "❌ Falha ao enviar backup.")
		}

	default:
		_ = a.tg.SendMessage(ctx, chatID, "Comando não reconhecido. Digite `/ajuda` para ver as opções.")
	}
}

func parseTimeFlexible(timeStr string, loc *time.Location) (time.Time, error) {
	timeStr = strings.TrimSpace(timeStr)
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	}

	for _, f := range formats {
		if t, err := time.ParseInLocation(f, timeStr, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("could not parse date: %s", timeStr)
}
