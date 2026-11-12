package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"atlas/pkg/calculator"
	"atlas/pkg/calendar"
	"atlas/pkg/config"
	"atlas/pkg/database"
	"atlas/pkg/hud"
	"atlas/pkg/nlu"
	"atlas/pkg/scheduler"
	"atlas/pkg/stt"
	"atlas/pkg/telegram"
	"atlas/pkg/web"
)

type Server struct {
	cfg        *config.Config
	db         *database.DB
	tg         *telegram.Client
	sttClient  *stt.Client
	nluClient  *nlu.Client
	engine     *scheduler.Engine
	hudHandler *hud.Handler
	mux        *http.ServeMux
}

// New initializes all ATLAS modules and registers standard routes.
func New(cfg *config.Config) (*Server, error) {
	if cfg.Location == nil {
		cfg.Location = time.Local
	}

	db, err := database.Open(cfg.DBPath, cfg.TursoDatabaseURL, cfg.TursoAuthToken)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	tg := telegram.NewClient(cfg.TelegramBotToken)
	sttClient := stt.NewClient(cfg.GroqAPIKey)
	nluClient := nlu.NewClient(cfg.GroqAPIKey, cfg.NvidiaAPIKey, cfg.NvidiaModel, cfg.MaxTokensQuick, cfg.MaxTokensHeavy, cfg.MaxTokensResearch)
	engine := scheduler.NewEngine(cfg, db, tg, nluClient)
	hudHandler := hud.NewHandler(cfg, db)

	s := &Server{
		cfg:        cfg,
		db:         db,
		tg:         tg,
		sttClient:  sttClient,
		nluClient:  nluClient,
		engine:     engine,
		hudHandler: hudHandler,
		mux:        http.NewServeMux(),
	}

	s.registerRoutes()
	return s, nil
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/", s.handleRoot)
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/webhook", s.handleWebhook)
	s.mux.HandleFunc("/api/webhook", s.handleWebhook)

	// Cron endpoint for serverless (Vercel cron, cron-job.org)
	s.mux.HandleFunc("/api/cron/tick", s.handleCronTick)
	s.mux.HandleFunc("/cron/tick", s.handleCronTick)

	// HUD visual routes
	s.mux.HandleFunc("/hud", s.hudHandler.ServeHUD)
	s.mux.HandleFunc("/api/hud/data", s.hudHandler.HandleDataAPI)
	s.mux.HandleFunc("/api/hud/routine/toggle", s.hudHandler.HandleToggleRoutine)
	s.mux.HandleFunc("/api/hud/event/complete", s.hudHandler.HandleCompleteEvent)

	// Calendar iCal RFC 5545 feed
	s.mux.HandleFunc("/calendar.ics", s.handleCalendarICS)
	s.mux.HandleFunc("/feed.ics", s.handleCalendarICS)
}

// ServeHTTP delegates request handling to the internal multiplexer.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// StartScheduler starts background goroutines for continuous binary mode.
func (s *Server) StartScheduler(ctx context.Context) {
	if s.engine != nil {
		s.engine.Start(ctx)
	}
}

// Close releases any persistent database resources.
func (s *Server) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Server) getPublicURL() string {
	url := strings.TrimSpace(s.cfg.PublicURL)
	if url == "" || strings.Contains(url, "loca.lt") || strings.Contains(url, "localhost") {
		return "https://a-t-l-a-s.vercel.app"
	}
	return strings.TrimSuffix(url, "/")
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/api" {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ATLAS — Assistente Pessoal Executivo e Socratico (Go)"))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"online","system":"atlas"}`))
}

func (s *Server) handleCronTick(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CronSecret != "" {
		token := r.URL.Query().Get("token")
		if token == "" {
			token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.CronSecret)) != 1 {
			http.Error(w, "Unauthorized cron access", http.StatusUnauthorized)
			return
		}
	}

	if s.engine != nil {
		s.engine.Tick(r.Context())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	nowStr := time.Now().In(s.cfg.Location).Format(time.RFC3339)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"ok","time":"%s"}`, nowStr)))
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Validação de Segurança do Token Secreto do Webhook
	if s.cfg.TelegramSecretToken != "" {
		receivedToken := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if subtle.ConstantTimeCompare([]byte(s.cfg.TelegramSecretToken), []byte(receivedToken)) != 1 {
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

	if update.Message == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
		return
	}

	msg := update.Message
	chatID := msg.Chat.ID

	// 2. Validação Estrita de Whitelist do Usuário
	if s.cfg.TelegramChatID != 0 && chatID != s.cfg.TelegramChatID {
		log.Printf("[Segurança] Mensagem ignorada de chat_id não autorizado: %d\n", chatID)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	s.processIncomingMessage(ctx, msg)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (s *Server) processIncomingMessage(ctx context.Context, msg *telegram.Message) {
	chatID := msg.Chat.ID
	var inputText string

	// 1. Extração de Texto ou Voz (com Groq Whisper)
	if msg.Text != "" {
		inputText = strings.TrimSpace(msg.Text)
	} else if msg.Caption != "" {
		inputText = strings.TrimSpace(msg.Caption)
	} else if msg.Voice != nil && msg.Voice.FileID != "" {
		tmpPath, err := s.tg.DownloadVoiceFile(ctx, msg.Voice.FileID)
		if err != nil {
			log.Printf("[Erro] Download de áudio falhou: %v\n", err)
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Erro ao receber áudio.")
			return
		}
		defer os.Remove(tmpPath)

		transcription, err := s.sttClient.Transcribe(ctx, tmpPath)
		if err != nil {
			log.Printf("[Erro] Transcrição falhou: %v\n", err)
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Erro na transcrição de voz.")
			return
		}
		inputText = strings.TrimSpace(transcription)
	}

	if inputText == "" {
		return
	}

	// Identificação de Mensagens Encaminhadas
	if msg.ForwardSenderName != "" || msg.ForwardFrom != nil {
		sender := msg.ForwardSenderName
		if sender == "" && msg.ForwardFrom != nil {
			sender = msg.ForwardFrom.FirstName
		}
		inputText = fmt.Sprintf("[Mensagem Encaminhada de %s]: %s", sender, inputText)
	}

	// 2. Comandos Diretos
	if strings.HasPrefix(inputText, "/") {
		s.handleCommand(ctx, chatID, inputText)
		return
	}

	// 2.1 Confirmação Pendente (Sim/Não para substituição de eventos)
	if pending, err := s.db.GetPendingConfirmation(ctx, chatID); err == nil && pending != nil {
		if handled, reply := s.handlePendingConfirmation(ctx, chatID, inputText, pending); handled {
			_ = s.tg.SendMessage(ctx, chatID, reply)
			_ = s.db.RecordHistory(ctx, "assistant", reply)
			return
		}
	}

	// 2.2 Fast-Path Router Determinístico (Zero Tokens & Resposta Instantânea <1ms)
	if fastCmd, ok := matchFastPath(inputText); ok {
		s.handleCommand(ctx, chatID, fastCmd)
		return
	}

	// 3. Processamento Cognitivo e Socrático via NLU
	now := time.Now().In(s.cfg.Location)
	currentDatetime := database.FormatDateTime(now)

	_ = s.db.RecordHistory(ctx, "user", inputText)
	history, _ := s.db.GetRecentHistory(ctx, s.cfg.HistoryLimit)
	memories, _ := s.db.GetAllUserMemories(ctx)
	latestIdeia, _ := s.db.GetLatestActiveIdeia(ctx)
	activeEvents, _ := s.db.GetUpcomingEvents(ctx, 10)

	intent, err := s.nluClient.ParseIntent(ctx, inputText, currentDatetime, history, memories, latestIdeia, activeEvents)
	if err != nil {
		log.Printf("[Erro NLU] %v\n", err)
		reply := "⚠️ Ocorreu uma oscilação no processamento do seu pensamento. Repita de forma mais concisa."
		_ = s.tg.SendMessage(ctx, chatID, reply)
		_ = s.db.RecordHistory(ctx, "assistant", reply)
		return
	}

	// Registrar consumo de tokens
	if intent.TotalTokens > 0 {
		_ = s.db.RecordTokenUsage(ctx, intent.ProvedorUsado, intent.ModeloUsado, intent.PromptTokens, intent.CompletionTokens, intent.TotalTokens)
	}

	// 3.1 Perfil de Pesquisa Profunda (Tokens de Output Irrestritos)
	lowerInput := strings.ToLower(inputText)
	isResearch := intent.Complexidade == "pesquisa" || intent.Tipo == "pesquisa" ||
		strings.HasPrefix(lowerInput, "pesquise") ||
		strings.HasPrefix(lowerInput, "pesquisa") ||
		strings.Contains(lowerInput, "faça uma pesquisa") ||
		strings.Contains(lowerInput, "fazer uma pesquisa") ||
		strings.Contains(lowerInput, "traga artigos") ||
		strings.Contains(lowerInput, "trazer artigos") ||
		strings.Contains(lowerInput, "dossiê") ||
		strings.Contains(lowerInput, "dossie") ||
		strings.Contains(lowerInput, "estudo completo") ||
		strings.Contains(lowerInput, "resumo aprofundado") ||
		strings.Contains(lowerInput, "pesquise sobre")

	if isResearch {
		waitingMsg := "🔬 *Perfil de Pesquisa Ativado (Tokens de Output Irrestritos)*\n\nCompilando síntese exaustiva, múltiplos artigos e referências completas sem cortes..."
		_ = s.tg.SendMessage(ctx, chatID, waitingMsg)

		go func(promptText string, chatHistory []database.ChatMessage) {
			resCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			res, err := s.nluClient.ResearchAnalyze(resCtx, promptText, chatHistory)
			if err != nil {
				log.Printf("[Erro Modo Pesquisa] %v\n", err)
				_ = s.tg.SendMessage(resCtx, chatID, "⚠️ Houve uma instabilidade no processamento da pesquisa.")
				return
			}

			if res.TotalTokens > 0 {
				_ = s.db.RecordTokenUsage(resCtx, res.Provider, res.Model, res.PromptTokens, res.ComplTokens, res.TotalTokens)
			}

			researchResponse := nlu.SanitizeHumanResponse(res.Text)
			_ = s.tg.SendMessage(resCtx, chatID, researchResponse)
			_ = s.db.RecordHistory(resCtx, "assistant", researchResponse)
		}(inputText, history)

		return
	}

	// 3.2 Tarefa pesada / análise complexa moderada
	if intent.Complexidade == "pesada" {
		curiosity := nlu.RandomCuriosity()
		waitingMsg := fmt.Sprintf("⏳ *Esta solicitação exige análise aprofundada.* Acionando o núcleo de processamento inteligente da NVIDIA (Nemotron)...\n\nEnquanto isso, uma curiosidade aleatória para você:\n🦩 _%s_", curiosity)
		_ = s.tg.SendMessage(ctx, chatID, waitingMsg)

		go func(promptText string, chatHistory []database.ChatMessage) {
			heavyCtx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
			defer cancel()

			deepRes, err := s.nluClient.DeepAnalyze(heavyCtx, promptText, chatHistory)
			if err != nil {
				log.Printf("[Erro NVIDIA Heavy] %v\n", err)
				_ = s.tg.SendMessage(heavyCtx, chatID, "⚠️ Houve uma instabilidade momentânea na análise da NVIDIA.")
				return
			}

			if deepRes.TotalTokens > 0 {
				_ = s.db.RecordTokenUsage(heavyCtx, deepRes.Provider, deepRes.Model, deepRes.PromptTokens, deepRes.ComplTokens, deepRes.TotalTokens)
			}

			deepResponse := nlu.SanitizeHumanResponse(deepRes.Text)
			_ = s.tg.SendMessage(heavyCtx, chatID, deepResponse)
			_ = s.db.RecordHistory(heavyCtx, "assistant", deepResponse)
		}(inputText, history)

		return
	}

	var reply string

	switch intent.Tipo {
	case "ideia":
		suma := intent.SumaIdeia
		if suma == "" {
			suma = intent.Conteudo
		}
		id, err := s.db.InsertIdeia(ctx, intent.Titulo, intent.Conteudo, suma, intent.PerguntaSocratica)
		if err != nil {
			log.Printf("[Erro DB Ideia] %v\n", err)
			reply = "⚠️ Falha ao registrar ideia no banco."
		} else {
			reply = fmt.Sprintf("💡 *Ideia #%d Registrada com Sucesso!*\n📌 *Título:* %s\n\n📋 *Suma da Ideia:*\n%s\n\n⚔️ *Provocação Socrática:*\n%s\n\n_Para adicionar mais pontos a essa ideia, basta continuar falando ou digitar `/ideia %d`._",
				id, intent.Titulo, suma, intent.PerguntaSocratica, id)
		}

	case "adicionar_ideia":
		targetID := intent.ID
		if targetID == 0 && latestIdeia != nil {
			targetID = latestIdeia.ID
		}
		if targetID > 0 {
			_ = s.db.AppendToIdeia(ctx, targetID, intent.Conteudo, intent.SumaIdeia)
			reply = fmt.Sprintf("💡 *Ideia #%d Atualizada!*\n📌 *Novo ponto adicionado:* %s\n\n📋 *Suma Acumulada da Ideia:*\n%s\n\n⚔️ *Ponto de Atenção:*\n%s\n\n_Consulte a suma completa a qualquer momento com `/ideia %d`._",
				targetID, intent.Conteudo, intent.SumaIdeia, intent.PerguntaSocratica, targetID)
		} else {
			id, _ := s.db.InsertIdeia(ctx, "Pensamento em Desenvolvimento", intent.Conteudo, intent.SumaIdeia, intent.PerguntaSocratica)
			reply = fmt.Sprintf("💡 *Nova Ideia #%d Registrada!*\n📋 *Suma:*\n%s", id, intent.SumaIdeia)
		}

	case "lembrar_fato":
		if intent.ChaveMemoria != "" && intent.ValorMemoria != "" {
			_ = s.db.SaveUserMemory(ctx, intent.ChaveMemoria, intent.ValorMemoria)
			if intent.Resposta != "" {
				reply = intent.Resposta
			} else {
				reply = fmt.Sprintf("🧠 *Informação Gravada com Sucesso!*\nGuarde isto com prioridade: *%s* = %s", intent.ChaveMemoria, intent.ValorMemoria)
			}
		} else {
			reply = "🧠 Fato anotado em sua memória permanente."
		}

	case "esquecer_fato":
		if intent.ChaveMemoria != "" {
			_ = s.db.DeleteUserMemory(ctx, intent.ChaveMemoria)
			reply = fmt.Sprintf("🧠 A informação sobre *'%s'* foi esquecida e removida da sua memória.", intent.ChaveMemoria)
		} else {
			reply = "Memória atualizada."
		}

	case "evento":
		existingEv, _ := s.findExistingEventForAdjustment(ctx, intent, inputText)
		if existingEv != nil {
			intentBytes, _ := json.Marshal(intent)
			_ = s.db.SavePendingConfirmation(ctx, chatID, "substituir_evento", existingEv.ID, string(intentBytes))

			timeInfo := existingEv.HorarioOficial
			if existingEv.HorarioTime != "" {
				timeInfo = existingEv.HorarioTime
			}
			reply = fmt.Sprintf("⚠️ Identifiquei o compromisso existente: *%s* (marcado para %s).\n\nEntão é pra eu apagar esse e refazer?\n*(Responda Sim ou Não)*", existingEv.Descricao, timeInfo)
			break
		}

		reply = s.createEventoFromIntent(ctx, intent)

	case "objetivo":
		var prazoPtr *string
		if intent.Prazo != "" {
			prazoPtr = &intent.Prazo
		}
		_, err := s.db.InsertObjetivo(ctx, intent.Titulo, prazoPtr)
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

	case "lembrete_diario":
		if intent.Horario == "" || intent.Mensagem == "" {
			reply = "⚠️ Por favor, especifique o horário (ex: 09:00) e a mensagem que deseja programar."
		} else {
			nome := intent.Mensagem
			if len(nome) > 30 {
				nome = nome[:27] + "..."
			}
			_, err := s.db.InsertRoutine(ctx, nome, intent.Horario, "custom", &intent.Mensagem)
			if err != nil {
				log.Printf("[Erro DB Routine] %v\n", err)
				reply = "⚠️ Falha ao salvar a programação."
			} else {
				reply = fmt.Sprintf("✅ *Programado!*\n\nTodo dia às *%s* vou te mandar:\n👉 \"%s\"", intent.Horario, intent.Mensagem)
			}
		}

	case "acao":
		reply = s.executeAppAction(ctx, intent)

	default: // "conversa"
		reply = intent.Resposta
		if reply == "" {
			reply = "Entendido. Como posso prosseguir com isso?"
		}
	}

	reply = nlu.SanitizeHumanResponse(reply)

	_ = s.tg.SendMessage(ctx, chatID, reply)
	_ = s.db.RecordHistory(ctx, "assistant", reply)
}

func (s *Server) handlePendingConfirmation(ctx context.Context, chatID int64, inputText string, pending *database.ConfirmacaoPendente) (bool, string) {
	lower := strings.ToLower(strings.TrimSpace(inputText))
	clean := strings.Trim(lower, ".!?,;: ")

	isYes := clean == "sim" || clean == "s" || clean == "ss" || clean == "isso" ||
		clean == "pode" || clean == "pode apagar" || clean == "apaga" || clean == "apague" ||
		clean == "com certeza" || clean == "positivo" || clean == "correto" || clean == "exato" ||
		clean == "claro" || strings.HasPrefix(clean, "sim ") || strings.HasPrefix(clean, "pode ")

	isNo := clean == "não" || clean == "nao" || clean == "n" || clean == "nn" ||
		clean == "deixa" || clean == "cancela" || clean == "cancelar" || clean == "não apaga" ||
		clean == "nao apaga" || clean == "negativo" || clean == "errado"

	isKeepBoth := strings.Contains(clean, "os dois") || strings.Contains(clean, "ambos") ||
		strings.Contains(clean, "mantem os dois") || strings.Contains(clean, "mantém os dois")

	if !isYes && !isNo && !isKeepBoth {
		return false, ""
	}

	defer func() {
		_ = s.db.DeletePendingConfirmation(ctx, chatID)
	}()

	switch pending.Tipo {
	case "substituir_evento":
		if isNo {
			return true, "👍 Entendido. Mantive o compromisso original sem alterações."
		}

		var intent nlu.IntentResult
		if err := json.Unmarshal([]byte(pending.NovoEventoJSON), &intent); err != nil {
			return true, "⚠️ Ocorreu um erro ao recuperar os dados do novo compromisso. Por favor, solicite novamente."
		}

		oldEventDesc := ""
		if pending.EventoID > 0 {
			if oldEv, err := s.db.GetEventoByID(ctx, pending.EventoID); err == nil && oldEv != nil {
				oldEventDesc = oldEv.Descricao
			}
			if isYes {
				_ = s.db.DeleteEvento(ctx, pending.EventoID)
			}
		}

		newReply := s.createEventoFromIntent(ctx, &intent)
		if isYes && oldEventDesc != "" {
			return true, fmt.Sprintf("🗑️ Compromisso anterior (*%s*) apagado com sucesso.\n\n%s", oldEventDesc, newReply)
		}
		if isKeepBoth {
			return true, fmt.Sprintf("✅ Mantive o compromisso anterior e adicionei o novo:\n\n%s", newReply)
		}
		return true, newReply

	case "limpar_agenda":
		if isNo {
			return true, "👍 Operação cancelada. Seus compromissos continuam intactos na agenda."
		}
		if isYes {
			total, err := s.db.ClearAllEventos(ctx)
			if err != nil {
				return true, "⚠️ Ocorreu um erro ao tentar limpar os compromissos."
			}
			return true, fmt.Sprintf("🗑️ Agenda limpa com sucesso. Todos os *%d* compromissos foram removidos.", total)
		}
		return true, "⚠️ Confirmação não compreendida. Por segurança, sua agenda foi mantida intacta."
	}

	return false, ""
}

func (s *Server) findExistingEventForAdjustment(ctx context.Context, intent *nlu.IntentResult, inputText string) (*database.Evento, error) {
	// 1. NLU identificou diretamente pelo ID
	if intent.SubstituirEventoID > 0 {
		if ev, err := s.db.GetEventoByID(ctx, intent.SubstituirEventoID); err == nil && ev != nil && ev.Status == "pendente" {
			return ev, nil
		}
	}

	// 2. Busca por similaridade de descrição com evento ativo pendente
	if strings.TrimSpace(intent.Descricao) != "" {
		if ev, err := s.db.FindSimilarActiveEvento(ctx, intent.Descricao); err == nil && ev != nil {
			return ev, nil
		}
	}

	// 3. Checa palavras de correção no texto e recupera o último evento criado
	lower := strings.ToLower(inputText)
	isCorrection := strings.Contains(lower, "errad") ||
		strings.Contains(lower, "errei") ||
		strings.Contains(lower, "ajust") ||
		strings.Contains(lower, "corrig") ||
		strings.Contains(lower, "mudar") ||
		strings.Contains(lower, "trocar") ||
		strings.Contains(lower, "ao inves") ||
		strings.Contains(lower, "ao invés") ||
		strings.Contains(lower, "em vez") ||
		strings.HasPrefix(lower, "não,") ||
		strings.HasPrefix(lower, "nao,") ||
		strings.HasPrefix(lower, "não ") ||
		strings.HasPrefix(lower, "nao ")

	if isCorrection {
		if ev, err := s.db.GetLatestPendingEvento(ctx); err == nil && ev != nil {
			return ev, nil
		}
	}

	return nil, nil
}

func (s *Server) createEventoFromIntent(ctx context.Context, intent *nlu.IntentResult) string {
	deslocamento := intent.EstimativaDeslocamentoMin
	if deslocamento <= 0 {
		deslocamento = 25
	}
	preparo := intent.EstimativaPreparoMin
	if preparo <= 0 {
		preparo = 35
	}
	now := time.Now().In(s.cfg.Location)
	diasNomes := []string{"Domingo", "Segunda-feira", "Terça-feira", "Quarta-feira", "Quinta-feira", "Sexta-feira", "Sábado"}

	switch intent.Frequencia {
	case "diario":
		horarioHM := intent.Horario
		if horarioHM == "" && intent.HorarioOficial != "" {
			if t, err := parseTimeFlexible(intent.HorarioOficial, s.cfg.Location); err == nil {
				horarioHM = t.Format("15:04")
			}
		}
		if horarioHM == "" {
			return "⚠️ Para compromissos diários, por favor informe o horário (ex: 'todo dia às 08:30')."
		}

		parts := strings.Split(horarioHM, ":")
		hh, _ := strconv.Atoi(parts[0])
		mm := 0
		if len(parts) > 1 {
			mm, _ = strconv.Atoi(parts[1])
		}
		refTime := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, s.cfg.Location)
		buffers := calculator.Calculate(refTime, deslocamento, preparo, calculator.DefaultDelayMargin)

		_, err := s.db.InsertEventoCompleto(ctx, intent.Descricao, "", buffers.BufferPrep.Format("15:04"), buffers.BufferLeave.Format("15:04"), "diario", nil, nil, horarioHM, deslocamento, preparo)
		if err != nil {
			log.Printf("[Erro DB Evento Diario] %v\n", err)
			return "⚠️ Falha ao agendar compromisso diário."
		}
		return fmt.Sprintf("🎯 *Compromisso Diário Agendado (+15%% margem determinística)*\n\n📌 *Evento:* %s\n🔄 *Frequência:* Todo dia às *%s*\n🛁 *Preparo:* %s\n🚗 *Saída:* %s\n🛡️ *Margem:* +%d min de segurança diária",
			intent.Descricao, horarioHM, buffers.BufferPrep.Format("15:04"), buffers.BufferLeave.Format("15:04"), buffers.MarginMinutes)

	case "semanal":
		horarioHM := intent.Horario
		if horarioHM == "" && intent.HorarioOficial != "" {
			if t, err := parseTimeFlexible(intent.HorarioOficial, s.cfg.Location); err == nil {
				horarioHM = t.Format("15:04")
			}
		}
		if horarioHM == "" {
			return "⚠️ Para compromissos semanais, especifique o dia e o horário (ex: 'toda segunda às 14:00')."
		}
		diaSem := 1 // Default Segunda-feira se não especificado
		if intent.DiaSemana != nil && *intent.DiaSemana >= 0 && *intent.DiaSemana <= 6 {
			diaSem = *intent.DiaSemana
		}

		parts := strings.Split(horarioHM, ":")
		hh, _ := strconv.Atoi(parts[0])
		mm := 0
		if len(parts) > 1 {
			mm, _ = strconv.Atoi(parts[1])
		}
		refTime := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, s.cfg.Location)
		buffers := calculator.Calculate(refTime, deslocamento, preparo, calculator.DefaultDelayMargin)

		_, err := s.db.InsertEventoCompleto(ctx, intent.Descricao, "", buffers.BufferPrep.Format("15:04"), buffers.BufferLeave.Format("15:04"), "semanal", &diaSem, nil, horarioHM, deslocamento, preparo)
		if err != nil {
			log.Printf("[Erro DB Evento Semanal] %v\n", err)
			return "⚠️ Falha ao agendar compromisso semanal."
		}
		return fmt.Sprintf("🎯 *Compromisso Semanal Agendado (+15%% margem determinística)*\n\n📌 *Evento:* %s\n🔁 *Frequência:* Toda *%s* às *%s*\n🛁 *Preparo:* %s\n🚗 *Saída:* %s\n🛡️ *Margem:* +%d min de segurança",
			intent.Descricao, diasNomes[diaSem], horarioHM, buffers.BufferPrep.Format("15:04"), buffers.BufferLeave.Format("15:04"), buffers.MarginMinutes)

	case "mensal":
		horarioHM := intent.Horario
		if horarioHM == "" && intent.HorarioOficial != "" {
			if t, err := parseTimeFlexible(intent.HorarioOficial, s.cfg.Location); err == nil {
				horarioHM = t.Format("15:04")
			}
		}
		if horarioHM == "" {
			return "⚠️ Para compromissos mensais, especifique o dia do mês e o horário (ex: 'todo dia 5 às 10:00')."
		}
		diaMes := 1
		if intent.DiaMes != nil && *intent.DiaMes >= 1 && *intent.DiaMes <= 31 {
			diaMes = *intent.DiaMes
		}

		parts := strings.Split(horarioHM, ":")
		hh, _ := strconv.Atoi(parts[0])
		mm := 0
		if len(parts) > 1 {
			mm, _ = strconv.Atoi(parts[1])
		}
		refTime := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, s.cfg.Location)
		buffers := calculator.Calculate(refTime, deslocamento, preparo, calculator.DefaultDelayMargin)

		_, err := s.db.InsertEventoCompleto(ctx, intent.Descricao, "", buffers.BufferPrep.Format("15:04"), buffers.BufferLeave.Format("15:04"), "mensal", nil, &diaMes, horarioHM, deslocamento, preparo)
		if err != nil {
			log.Printf("[Erro DB Evento Mensal] %v\n", err)
			return "⚠️ Falha ao agendar compromisso mensal."
		}
		return fmt.Sprintf("🎯 *Compromisso Mensal Agendado (+15%% margem determinística)*\n\n📌 *Evento:* %s\n🗓️ *Frequência:* Todo dia *%d* de cada mês às *%s*\n🛁 *Preparo:* %s\n🚗 *Saída:* %s\n🛡️ *Margem:* +%d min de segurança\n_Nota: Em meses com menos de %d dias (como fevereiro ou meses de 30 dias), o ATLAS ajusta automaticamente para o último dia do mês para garantir seu cumprimento!_",
			intent.Descricao, diaMes, horarioHM, buffers.BufferPrep.Format("15:04"), buffers.BufferLeave.Format("15:04"), buffers.MarginMinutes, diaMes)

	default: // "pontual"
		parsedTime, err := parseTimeFlexible(intent.HorarioOficial, s.cfg.Location)
		if err != nil {
			return fmt.Sprintf("⚠️ Não compreendi o horário '%s'. Especifique no formato YYYY-MM-DD HH:MM.", intent.HorarioOficial)
		}
		buffers := calculator.Calculate(parsedTime, deslocamento, preparo, calculator.DefaultDelayMargin)
		b1Str := database.FormatDateTime(buffers.BufferPrep)
		b2Str := database.FormatDateTime(buffers.BufferLeave)
		oficialStr := database.FormatDateTime(buffers.OfficialTime)

		_, err = s.db.InsertEventoCompleto(ctx, intent.Descricao, oficialStr, b1Str, b2Str, "pontual", nil, nil, parsedTime.Format("15:04"), deslocamento, preparo)
		if err != nil {
			log.Printf("[Erro DB Evento Pontual] %v\n", err)
			return "⚠️ Falha ao agendar evento."
		}
		return fmt.Sprintf("🎯 *Compromisso Agendado (+15%% margem determinística)*\n\n📌 *Evento:* %s\n⏰ *Oficial:* %s\n🛁 *Preparo:* %s\n🚗 *Saída:* %s\n🛡️ *Margem:* +%d min de segurança",
			intent.Descricao, oficialStr, b1Str, b2Str, buffers.MarginMinutes)
	}
}

func (s *Server) executeAppAction(ctx context.Context, intent *nlu.IntentResult) string {
	switch intent.Acao {
	case "apagar_rotina":
		if intent.ID > 0 {
			if err := s.db.DeleteRoutine(ctx, intent.ID); err == nil {
				return fmt.Sprintf("🗑️ Lembrete [%d] apagado com sucesso.", intent.ID)
			}
		}
		if intent.Termo != "" {
			if nome, err := s.db.DeleteRoutineByName(ctx, intent.Termo); err == nil {
				return fmt.Sprintf("🗑️ Lembrete '%s' apagado com sucesso.", nome)
			}
		}
		return "⚠️ Não encontrei o lembrete especificado para apagar."

	case "liturgia":
		liturgiaText, err := web.FetchLiturgiaDiaria(ctx)
		if err != nil {
			log.Printf("[Erro Liturgia] %v\n", err)
			return "⚠️ Não foi possível carregar a liturgia diária no momento."
		}
		return liturgiaText

	case "noticias":
		termo := intent.Termo
		if termo == "" {
			termo = "brasil"
		}
		news, err := web.SearchGoogleNews(ctx, termo, 4)
		if err != nil || len(news) == 0 {
			return fmt.Sprintf("⚠️ Nenhuma notícia recente encontrada para '%s'.", termo)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📰 *Principais Notícias sobre '%s':*\n\n", termo))
		for _, n := range news {
			sb.WriteString(fmt.Sprintf("• *%s*\n  🔗 [Abrir Notícia](%s)\n\n", n.Title, n.Link))
		}
		return sb.String()

	case "resumir_link":
		link := intent.URL
		if link == "" {
			link = intent.Termo
		}
		if link == "" {
			return "⚠️ Envie ou especifique o link (URL) que deseja resumir."
		}
		pageText, err := web.FetchPageText(ctx, link, 2500)
		if err != nil {
			log.Printf("[Erro FetchPage] %v\n", err)
			return fmt.Sprintf("⚠️ Não foi possível ler o link informado: %v", err)
		}
		summaryPrompt := fmt.Sprintf("Resuma este artigo/página em tópicos executivos diretos e concisos (máx 3 parágrafos):\n\n%s", pageText)
		summaryResp, err := s.nluClient.DeepAnalyze(ctx, summaryPrompt, nil)
		if err != nil || summaryResp == nil || summaryResp.Text == "" {
			return fmt.Sprintf("📄 *Conteúdo da Página:*\n\n%s", pageText)
		}
		if summaryResp.TotalTokens > 0 {
			_ = s.db.RecordTokenUsage(ctx, summaryResp.Provider, summaryResp.Model, summaryResp.PromptTokens, summaryResp.ComplTokens, summaryResp.TotalTokens)
		}
		return fmt.Sprintf("📄 *Resumo Executivo do Link:*\n\n%s\n\n🔗 *Fonte:* %s", summaryResp.Text, link)

	case "alterar_rotina":
		if intent.Horario == "" {
			return "⚠️ Por favor, especifique o novo horário (ex: 08:30)."
		}
		if intent.ID > 0 {
			if err := s.db.UpdateRoutineTime(ctx, intent.ID, intent.Horario); err == nil {
				return fmt.Sprintf("⏰ Horário da rotina [%d] atualizado para *%s*.", intent.ID, intent.Horario)
			}
		}
		return "⚠️ Não foi possível alterar o horário da rotina."

	case "toggle_rotina":
		if intent.ID > 0 {
			if active, err := s.db.ToggleRoutine(ctx, intent.ID); err == nil {
				status := "desativada 🔴"
				if active {
					status = "ativada 🟢"
				}
				return fmt.Sprintf("Rotina [%d] %s.", intent.ID, status)
			}
		}
		return "⚠️ Não consegui alterar o status da rotina."

	case "concluir_evento":
		if intent.ID > 0 {
			_ = s.db.MarkEventStatus(ctx, intent.ID, "concluido")
			return fmt.Sprintf("✅ Compromisso [%d] marcado como concluído.", intent.ID)
		}
		if intent.Termo != "" {
			id, desc, err := s.db.FindEventoByDesc(ctx, intent.Termo)
			if err == nil {
				_ = s.db.MarkEventStatus(ctx, id, "concluido")
				return fmt.Sprintf("✅ Compromisso *%s* marcado como concluído com sucesso.", desc)
			}
		}
		return "⚠️ Não localizei o compromisso pendente para marcar como concluído."

	case "apagar_evento":
		if intent.ID > 0 {
			_ = s.db.DeleteEvento(ctx, intent.ID)
			return fmt.Sprintf("🗑️ Compromisso [%d] excluído da sua agenda.", intent.ID)
		}
		if intent.Termo != "" {
			id, desc, err := s.db.FindEventoByDesc(ctx, intent.Termo)
			if err == nil {
				_ = s.db.DeleteEvento(ctx, id)
				return fmt.Sprintf("🗑️ Compromisso *%s* excluído da sua agenda.", desc)
			}
		}
		return "⚠️ Não localizei o compromisso para excluir."

	case "limpar_agenda":
		count, _ := s.db.CountAllEventos(ctx)
		if count == 0 {
			return "📅 Sua agenda já está limpa. Não há nenhum compromisso registrado."
		}
		_ = s.db.SavePendingConfirmation(ctx, s.cfg.TelegramChatID, "limpar_agenda", 0, "")
		return fmt.Sprintf("⚠️ *Atenção:* Você tem certeza de que deseja *apagar todos os %d compromissos* da sua agenda?\n\nEsta ação é permanente e irreversível.\n\n👉 Responda com *Sim* para confirmar ou *Não* para cancelar.", count)

	case "atualizar_meta":
		if intent.Termo != "" {
			id, title, err := s.db.FindGoalByTitle(ctx, intent.Termo)
			if err == nil {
				prog := intent.Progresso
				if prog <= 0 {
					prog = 100
				}
				_ = s.db.UpdateGoalProgress(ctx, id, prog)
				return fmt.Sprintf("🎯 Meta *%s* atualizada para *%d%%* de progresso.", title, prog)
			}
		}
		return "⚠️ Não localizei a meta ativa para atualizar."

	case "fazer_backup":
		if s.cfg.TursoDatabaseURL != "" {
			return "🛡️ O banco de dados está operando no Turso Cloud com replicação e backups automáticos na nuvem."
		}
		caption := fmt.Sprintf("🛡️ Backup solicitado em %s", time.Now().In(s.cfg.Location).Format("2006-01-02 15:04:05"))
		if err := s.tg.SendDocument(ctx, s.cfg.TelegramChatID, s.cfg.DBPath, caption); err == nil {
			return "🛡️ Arquivo de backup do banco de dados enviado acima."
		}
		return "⚠️ Falha ao gerar o arquivo de backup."

	case "listar_agenda":
		now := time.Now().In(s.cfg.Location)
		todayStr := now.Format("2006-01-02")
		events, _ := s.db.GetEventsForDay(ctx, todayStr)
		if len(events) == 0 {
			return fmt.Sprintf("📅 Não há compromissos agendados para hoje (%s).", todayStr)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📅 *Agenda de Hoje (%s):*\n\n", todayStr))
		for _, ev := range events {
			sb.WriteString(fmt.Sprintf("📌 *%s*\n  ⏰ Oficial: %s | 🚗 Saída: %s\n\n", ev.Descricao, ev.HorarioOficial, ev.HorarioBuffer2))
		}
		return sb.String()

	case "listar_lembretes":
		routines, _ := s.db.GetAllRoutines(ctx)
		if len(routines) == 0 {
			return "Nenhum lembrete ou rotina cadastrada."
		}
		var sb strings.Builder
		sb.WriteString("⏰ *Lembretes e Rotinas Diárias:*\n\n")
		for _, r := range routines {
			status := "🟢"
			if r.Ativo == 0 {
				status = "🔴"
			}
			desc := r.Nome
			if r.PromptCustom != nil && *r.PromptCustom != "" {
				desc = fmt.Sprintf("\"%s\"", *r.PromptCustom)
			}
			sb.WriteString(fmt.Sprintf("%s *[%d]* às *%s* — %s\n", status, r.ID, r.Horario, desc))
		}
		return sb.String()

	case "ver_perfil":
		mems, err := s.db.GetAllUserMemories(ctx)
		if err != nil || len(mems) == 0 {
			return "🧠 Minha memória permanente sobre você ainda está vazia."
		}
		var sb strings.Builder
		sb.WriteString("🧠 *O Que o ATLAS Lembra Sobre Você:*\n\n")
		for _, m := range mems {
			sb.WriteString(fmt.Sprintf("• *%s:* %s\n", strings.Title(m.Chave), m.Valor))
		}
		return sb.String()

	default:
		return "Comando executado com sucesso."
	}
}

func (s *Server) handleCommand(ctx context.Context, chatID int64, cmdText string) {
	parts := strings.Fields(cmdText)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/start", "/ajuda", "/help":
		msg := `🤖 *ATLAS — Como Usar (Simples e Direto)*

• ⚡ */hud* — Abrir painel visual de controle
• 💡 */ideias* — Ver todas as ideias e projetos registrados
• 🔍 */ideia <id>* — Ler a suma acumulada completa de uma ideia
• 🧠 */perfil* — Ver fatos e preferências que o ATLAS memorizou sobre você
• 📖 */liturgia* — Liturgia diária católica completa com link
• 📰 */noticias <tema>* — Buscar notícias recentes na internet com links
• ⏰ */lembretes* — Ver mensagens diárias programadas
• 🗑️ */apagar <id>* — Apagar uma programação diária (ex: /apagar 3)
• 📅 */hoje* — Agenda de hoje com tempos de preparo e saída (+15%)
• 🎯 */metas* — Suas metas e progresso
• 📊 */tokens* — Relatório de consumo de tokens e limites
• 🗓️ */calendario* — Link para sincronizar no Google/Apple Calendar

💬 *Comandos Naturais (Voz ou Texto):*
👉 _"Anotar uma ideia: criar um app de assinatura para barbeiros"_
👉 _"Adicione à ideia anterior que o plano mensal será R$ 49"_
👉 _"Lembre-se que eu sou desenvolvedor e acordo às 06:30"_
👉 _"Qual é a liturgia diária de hoje?"_
👉 _"Todo dia às 09:00 mande uma mensagem dizendo: tomar creatina"_`
		_ = s.tg.SendMessage(ctx, chatID, msg)

	case "/liturgia":
		text, err := web.FetchLiturgiaDiaria(ctx)
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Erro ao consultar a liturgia diária.")
			return
		}
		_ = s.tg.SendMessage(ctx, chatID, text)

	case "/noticias":
		termo := "brasil"
		if len(parts) >= 2 {
			termo = strings.Join(parts[1:], " ")
		}
		news, err := web.SearchGoogleNews(ctx, termo, 4)
		if err != nil || len(news) == 0 {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("Nenhuma notícia recente encontrada para '%s'.", termo))
			return
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📰 *Principais Notícias sobre '%s':*\n\n", termo))
		for _, n := range news {
			sb.WriteString(fmt.Sprintf("• *%s*\n  🔗 [Abrir Notícia](%s)\n\n", n.Title, n.Link))
		}
		_ = s.tg.SendMessage(ctx, chatID, sb.String())

	case "/pesquisa":
		if len(parts) < 2 {
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Uso correto: `/pesquisa <tema ou pergunta>`")
			return
		}
		tema := strings.Join(parts[1:], " ")
		waitingMsg := fmt.Sprintf("🔬 *Perfil de Pesquisa Ativado (Tokens de Output Irrestritos)*\n\nPesquisando a fundo sobre: *%s*...", tema)
		_ = s.tg.SendMessage(ctx, chatID, waitingMsg)

		go func(promptText string) {
			resCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			res, err := s.nluClient.ResearchAnalyze(resCtx, promptText, nil)
			if err != nil {
				log.Printf("[Erro Comando /pesquisa] %v\n", err)
				_ = s.tg.SendMessage(resCtx, chatID, "⚠️ Falha ao executar pesquisa aprofundada.")
				return
			}

			if res.TotalTokens > 0 {
				_ = s.db.RecordTokenUsage(resCtx, res.Provider, res.Model, res.PromptTokens, res.ComplTokens, res.TotalTokens)
			}

			cleanResponse := nlu.SanitizeHumanResponse(res.Text)
			_ = s.tg.SendMessage(resCtx, chatID, cleanResponse)
			_ = s.db.RecordHistory(resCtx, "assistant", cleanResponse)
		}(tema)
		return

	case "/hud", "/painel":
		hudURL := fmt.Sprintf("%s/hud?token=%s", s.getPublicURL(), s.cfg.CalendarToken)
		msg := "⚡ *ATLAS — Painel Visual (HUD)*\n\nToque no botão abaixo ou acesse pelo link direto:\n" + hudURL
		err := s.tg.SendMessageWithWebApp(ctx, chatID, msg, "⚡ Abrir Painel HUD", hudURL)
		if err != nil {
			log.Printf("[HUD] Botão WebApp falhou: %v. Enviando link direto.\n", err)
			_ = s.tg.SendMessage(ctx, chatID, "⚡ *ATLAS — Painel Visual (HUD)*\n\nAcesse seu painel pelo link:\n"+hudURL)
		}

	case "/mes", "/calendario":
		now := time.Now().In(s.cfg.Location)
		counts, _ := s.db.GetMonthEventCounts(ctx, now.Year(), now.Month())
		grid := calendar.RenderEmojiCalendar(now.Year(), now.Month(), now, counts)
		calURL := fmt.Sprintf("%s/calendar.ics?token=%s", s.getPublicURL(), s.cfg.CalendarToken)
		fullMsg := fmt.Sprintf("%s\n\n🔗 _Sync Google/Apple Calendar:_ `%s`", grid, calURL)
		_ = s.tg.SendMessage(ctx, chatID, fullMsg)

	case "/rotinas", "/lembretes":
		routines, err := s.db.GetAllRoutines(ctx)
		if err != nil || len(routines) == 0 {
			_ = s.tg.SendMessage(ctx, chatID, "Nenhum lembrete programado.")
			return
		}

		var sb strings.Builder
		sb.WriteString("⏰ *Suas Mensagens e Rotinas Diárias:*\n\n")
		for _, r := range routines {
			status := "🟢"
			if r.Ativo == 0 {
				status = "🔴"
			}
			desc := r.Nome
			if r.PromptCustom != nil && *r.PromptCustom != "" {
				desc = fmt.Sprintf("\"%s\"", *r.PromptCustom)
			}
			sb.WriteString(fmt.Sprintf("%s *[%d]* às *%s* — %s\n", status, r.ID, r.Horario, desc))
		}
		sb.WriteString("\n_Para apagar qualquer item: `/apagar <id>` (ex: `/apagar 4`)_")
		_ = s.tg.SendMessage(ctx, chatID, sb.String())

	case "/apagar", "/remover", "/del":
		if len(parts) >= 2 {
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil {
				if err := s.db.DeleteRoutine(ctx, id); err == nil {
					_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("🗑️ Programação [%d] apagada.", id))
					return
				}
			}
		}
		_ = s.tg.SendMessage(ctx, chatID, "Uso: `/apagar <id>` (ex: `/apagar 3`)")

	case "/limpar", "/limpar_agenda", "/limpar_eventos", "/reset_agenda":
		count, _ := s.db.CountAllEventos(ctx)
		if count == 0 {
			_ = s.tg.SendMessage(ctx, chatID, "📅 Sua agenda já está limpa. Não há nenhum compromisso registrado.")
			return
		}
		_ = s.db.SavePendingConfirmation(ctx, chatID, "limpar_agenda", 0, "")
		msg := fmt.Sprintf("⚠️ *Atenção:* Você tem certeza de que deseja *apagar todos os %d compromissos* da sua agenda?\n\nEsta ação é permanente e irreversível.\n\n👉 Responda com *Sim* para confirmar ou *Não* para cancelar.", count)
		_ = s.tg.SendMessage(ctx, chatID, msg)

	case "/rotina":
		if len(parts) >= 3 && strings.ToLower(parts[2]) == "toggle" {
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				_ = s.tg.SendMessage(ctx, chatID, "ID inválido.")
				return
			}
			active, err := s.db.ToggleRoutine(ctx, id)
			if err != nil {
				_ = s.tg.SendMessage(ctx, chatID, "Erro ao atualizar rotina.")
				return
			}
			statusStr := "desativada 🔴"
			if active {
				statusStr = "ativada 🟢"
			}
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("Rotina [%d] %s.", id, statusStr))
			return
		}

		if len(parts) >= 4 && strings.ToLower(parts[2]) == "horario" {
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				_ = s.tg.SendMessage(ctx, chatID, "ID inválido.")
				return
			}
			newTime := parts[3]
			if _, err := time.Parse("15:04", newTime); err != nil {
				_ = s.tg.SendMessage(ctx, chatID, "Formato de hora inválido. Use HH:MM (ex: 08:30).")
				return
			}
			if err := s.db.UpdateRoutineTime(ctx, id, newTime); err != nil {
				_ = s.tg.SendMessage(ctx, chatID, "Erro ao alterar horário.")
				return
			}
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("Horário da rotina [%d] atualizado para *%s* ⏰.", id, newTime))
			return
		}

		_ = s.tg.SendMessage(ctx, chatID, "Uso: `/rotina <id> horario HH:MM` ou `/rotina <id> toggle`")

	case "/hoje", "/agenda":
		now := time.Now().In(s.cfg.Location)
		todayStr := now.Format("2006-01-02")
		events, err := s.db.GetEventsForDay(ctx, todayStr)
		if err != nil || len(events) == 0 {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("📅 Não há compromissos agendados para hoje (%s).", todayStr))
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📅 *Agenda de Hoje (%s):*\n\n", todayStr))
		for _, ev := range events {
			badge := ""
			switch ev.Frequencia {
			case "diario":
				badge = " 🔄 _(Diário)_"
			case "semanal":
				badge = " 🔁 _(Semanal)_"
			case "mensal":
				badge = " 🗓️ _(Mensal)_"
			}

			b1 := ev.HorarioBuffer1
			b2 := ev.HorarioBuffer2
			if (b1 == "" || b2 == "") && ev.HorarioTime != "" {
				parts := strings.Split(ev.HorarioTime, ":")
				if len(parts) >= 2 {
					hh, _ := strconv.Atoi(parts[0])
					mm, _ := strconv.Atoi(parts[1])
					oficial := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, s.cfg.Location)
					desloc := ev.DeslocamentoMin
					if desloc <= 0 {
						desloc = 25
					}
					prep := ev.PreparoMin
					if prep <= 0 {
						prep = 35
					}
					calc := calculator.Calculate(oficial, desloc, prep, calculator.DefaultDelayMargin)
					b1 = calc.BufferPrep.Format("15:04")
					b2 = calc.BufferLeave.Format("15:04")
				}
			}

			sb.WriteString(fmt.Sprintf("📌 *%s*%s\n  ⏰ Oficial: %s\n  🛁 Preparo: %s\n  🚗 Saída: %s\n\n",
				ev.Descricao, badge, ev.HorarioOficial, b1, b2))
		}
		_ = s.tg.SendMessage(ctx, chatID, sb.String())

	case "/metas":
		goals, err := s.db.GetActiveGoals(ctx, 10)
		if err != nil || len(goals) == 0 {
			_ = s.tg.SendMessage(ctx, chatID, "🎯 Nenhuma meta ativa cadastrada.")
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
		_ = s.tg.SendMessage(ctx, chatID, sb.String())

	case "/ideias":
		ideas, err := s.db.GetRecentIdeas(ctx, 10)
		if err != nil || len(ideas) == 0 {
			_ = s.tg.SendMessage(ctx, chatID, "💡 Nenhuma ideia registrada ainda. Basta me contar uma ideia no chat!")
			return
		}

		var sb strings.Builder
		sb.WriteString("💡 *Suas Ideias e Projetos:*\n\n")
		for _, i := range ideas {
			suma := i.SumaExecutiva
			if suma == "" {
				suma = i.Conteudo
			}
			if len(suma) > 120 {
				suma = suma[:117] + "..."
			}
			sb.WriteString(fmt.Sprintf("• *[%d]* *%s*\n  📋 %s\n  _Ver completa: `/ideia %d`_\n\n", i.ID, i.Titulo, suma, i.ID))
		}
		_ = s.tg.SendMessage(ctx, chatID, sb.String())

	case "/ideia":
		var targetID int64
		if len(parts) >= 2 {
			targetID, _ = strconv.ParseInt(parts[1], 10, 64)
		}
		var ideia *database.Ideia
		if targetID > 0 {
			ideia, _ = s.db.GetIdeiaByID(ctx, targetID)
		} else {
			ideia, _ = s.db.GetLatestActiveIdeia(ctx)
		}
		if ideia == nil {
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Ideia não encontrada. Digite `/ideias` para ver a lista.")
			return
		}

		suma := ideia.SumaExecutiva
		if suma == "" {
			suma = ideia.Conteudo
		}
		msg := fmt.Sprintf("💡 *Ideia #%d: %s*\nStatus: _%s_ | Atualizada: %s\n\n📋 *Suma Executiva da Ideia:*\n%s\n\n📝 *Histórico de Pensamentos Anotados:*\n%s\n\n⚔️ *Provocação Socrática:*\n_%s_",
			ideia.ID, ideia.Titulo, ideia.Status, ideia.AtualizadoEm, suma, ideia.Conteudo, ideia.QuestionamentoSocratico)
		_ = s.tg.SendMessage(ctx, chatID, msg)

	case "/perfil", "/memoria":
		mems, err := s.db.GetAllUserMemories(ctx)
		if err != nil || len(mems) == 0 {
			_ = s.tg.SendMessage(ctx, chatID, "🧠 Minha memória permanente sobre você ainda está vazia.\n\nBasta me contar algo no chat, por exemplo:\n👉 _\"Lembre-se que sou desenvolvedor\"_\n👉 _\"Guarde isto: acordo todo dia às 06:30\"_")
			return
		}

		var sb strings.Builder
		sb.WriteString("🧠 *O Que o ATLAS Lembra Sobre Você (Memória Permanente):*\n\n")
		for _, m := range mems {
			sb.WriteString(fmt.Sprintf("• *%s:* %s\n", strings.Title(m.Chave), m.Valor))
		}
		sb.WriteString("\n_Para apagar qualquer fato: `/esquecer <chave>`_")
		_ = s.tg.SendMessage(ctx, chatID, sb.String())

	case "/esquecer":
		if len(parts) >= 2 {
			chave := parts[1]
			_ = s.db.DeleteUserMemory(ctx, chave)
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("🧠 Informação sobre *'%s'* removida da memória permanente.", chave))
			return
		}
		_ = s.tg.SendMessage(ctx, chatID, "Uso: `/esquecer <chave>` (ex: `/esquecer profissao`)")

	case "/tokens":
		now := time.Now().In(s.cfg.Location)
		todayStr := now.Format("2006-01-02")
		monthStr := now.Format("2006-01")
		stats, _ := s.db.GetTokenStats(ctx, todayStr, monthStr)

		msg := fmt.Sprintf("📊 *Consumo e Limites de Tokens (ATLAS)*\n\n"+
			"• *Hoje:* *%d* tokens\n"+
			"• *Este Mês:* *%d* tokens\n\n"+
			"🤖 *Por Provedor:*\n"+
			"• Groq LPU (Rápido): *%d* tokens\n"+
			"• NVIDIA NIM (Deep/Nemotron): *%d* tokens\n\n"+
			"⚙️ *Limites de Segurança:*\n"+
			"• Teto Resposta Rápida: *%d* tokens\n"+
			"• Teto Análise Pesada: *%d* tokens\n"+
			"• Teto Modo Pesquisa: *%d* tokens (Desbloqueado)\n"+
			"• Janela de Histórico: últimas *%d* mensagens",
			stats.TotalHoje, stats.TotalMes, stats.TotalGroq, stats.TotalNv,
			s.cfg.MaxTokensQuick, s.cfg.MaxTokensHeavy, s.cfg.MaxTokensResearch, s.cfg.HistoryLimit)
		_ = s.tg.SendMessage(ctx, chatID, msg)

	case "/backup":
		if s.cfg.TursoDatabaseURL != "" {
			_ = s.tg.SendMessage(ctx, chatID, "🛡️ O banco de dados está operando no Turso Cloud com replicação e backups automáticos.")
			return
		}
		caption := fmt.Sprintf("🛡️ Backup manual solicitado em %s", time.Now().In(s.cfg.Location).Format("2006-01-02 15:04:05"))
		if err := s.tg.SendDocument(ctx, chatID, s.cfg.DBPath, caption); err != nil {
			_ = s.tg.SendMessage(ctx, chatID, "❌ Falha ao enviar backup.")
		}

	default:
		_ = s.tg.SendMessage(ctx, chatID, "Comando não reconhecido. Digite `/ajuda` para ver as opções.")
	}
}

func (s *Server) handleCalendarICS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || (s.cfg.CalendarToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.CalendarToken)) != 1) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	events, err := s.db.GetUpcomingEvents(r.Context(), 50)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	icsData := calendar.GenerateICS(events, s.cfg.Location)
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline; filename=atlas_agenda.ics")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(icsData))
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

func matchFastPath(text string) (string, bool) {
	norm := strings.ToLower(strings.TrimSpace(text))
	norm = strings.Trim(norm, ".,!?\"'#")

	switch norm {
	case "hoje", "agenda", "minha agenda", "agenda de hoje", "compromissos", "meus compromissos", "o que tenho hoje", "ver agenda":
		return "/hoje", true
	case "mes", "mês", "calendario", "calendário", "meu mes", "meu mês", "calendario do mes", "calendario do mês", "calendario de emojis", "ver mes", "ver mês":
		return "/mes", true
	case "liturgia", "liturgia de hoje", "evangelho", "evangelho de hoje":
		return "/liturgia", true
	case "perfil", "meu perfil", "minha memoria", "minhas memorias", "minha memória", "minhas memórias", "o que voce sabe sobre mim", "o que você sabe sobre mim":
		return "/perfil", true
	case "metas", "minhas metas", "objetivos", "meus objetivos", "ver metas":
		return "/metas", true
	case "ideias", "minhas ideias", "projetos", "meus projetos", "ver ideias":
		return "/ideias", true
	case "lembretes", "meus lembretes", "rotinas", "minhas rotinas", "ver rotinas", "ver lembretes":
		return "/lembretes", true
	case "tokens", "meus tokens", "gasto de tokens", "consumo", "consumo de tokens":
		return "/tokens", true
	case "hud", "painel", "abrir hud", "abrir painel":
		return "/hud", true
	case "ajuda", "help", "como usar", "comandos", "menu":
		return "/ajuda", true
	case "backup", "fazer backup":
		return "/backup", true
	case "limpar agenda", "limpar compromissos", "limpar tudo", "apagar agenda", "apagar todos os compromissos", "apagar todos compromissos", "excluir todos os compromissos", "limpar todos os compromissos", "zerar agenda", "zerar compromissos", "apagar meus compromissos":
		return "/limpar_agenda", true
	}

	return "", false
}
