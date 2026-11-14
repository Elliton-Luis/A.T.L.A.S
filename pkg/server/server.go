package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"atlas/pkg/calculator"
	"atlas/pkg/calendar"
	"atlas/pkg/catholic"
	"atlas/pkg/config"
	"atlas/pkg/crawler"
	"atlas/pkg/database"
	"atlas/pkg/documents"
	"atlas/pkg/email"
	"atlas/pkg/hud"
	"atlas/pkg/interpreter"
	"atlas/pkg/nlu"
	"atlas/pkg/scheduler"
	"atlas/pkg/sonar"
	"atlas/pkg/stt"
	"atlas/pkg/swarm"
	"atlas/pkg/synonyms"
	"atlas/pkg/telegram"
	"atlas/pkg/web"
)

type Server struct {
	cfg         *config.Config
	db          *database.DB
	tg          *telegram.Client
	sttClient   *stt.Client
	nluClient   *nlu.Client
	engine      *scheduler.Engine
	hudHandler  *hud.Handler
	sonarClient *sonar.Client
	interpreter *interpreter.Runner
	crawler     *crawler.Crawler
	swarm       *swarm.Orchestrator
	emailReader *email.Reader
	mux         *http.ServeMux
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
	sonarClient := sonar.NewClient(cfg.SonarBridgeURL, cfg.SonarBridgeToken, cfg.SonarCLIPath, cfg.ProjectsDir)
	interpreterRunner := interpreter.NewRunner(sonarClient)
	webCrawler := crawler.New()
	swarmOrch := swarm.NewOrchestrator(nluClient)
	emailReader := email.NewReader(email.Config{
		Host:     cfg.IMAPHost,
		Port:     cfg.IMAPPort,
		User:     cfg.IMAPUser,
		Password: cfg.IMAPPass,
	})

	s := &Server{
		cfg:         cfg,
		db:          db,
		tg:          tg,
		sttClient:   sttClient,
		nluClient:   nluClient,
		engine:      engine,
		hudHandler:  hudHandler,
		sonarClient: sonarClient,
		interpreter: interpreterRunner,
		crawler:     webCrawler,
		swarm:       swarmOrch,
		emailReader: emailReader,
		mux:         http.NewServeMux(),
	}

	s.registerRoutes()
	engine.ExecuteAction = s.executeScheduledAction
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

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	key := fmt.Sprintf("telegram:%d:%d", chatID, update.UpdateID)
	claimed, err := s.db.ClaimExecution(ctx, key)
	if err != nil {
		http.Error(w, "Falha ao registrar mensagem", http.StatusServiceUnavailable)
		return
	}
	if !claimed {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.processIncomingMessage(ctx, msg)
	if err := s.db.FinishExecution(ctx, key); err != nil {
		log.Printf("[Webhook] Registro %s: %v", key, err)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (s *Server) processIncomingMessage(ctx context.Context, msg *telegram.Message) {
	chatID := msg.Chat.ID

	// Feedback Imediato: Ativa indicador nativo ("ATLAS está digitando...") para o usuário saber que a mensagem foi recebida e compreendida
	_ = s.tg.SendChatAction(ctx, chatID, "typing")

	var inputText string

	// 1. Extração de Texto, Voz, Áudios ou Documentos (com Groq Whisper e Extrator de Documentos)
	if msg.Text != "" {
		inputText = strings.TrimSpace(msg.Text)
	} else if msg.Caption != "" && msg.Document == nil && msg.Audio == nil {
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
	} else if msg.Audio != nil && msg.Audio.FileID != "" {
		tmpPath, err := s.tg.DownloadFileToTemp(ctx, msg.Audio.FileID, "atlas_audio_*")
		if err != nil {
			log.Printf("[Erro] Download de áudio falhou: %v\n", err)
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Erro ao receber arquivo de áudio.")
			return
		}
		defer os.Remove(tmpPath)

		transcription, err := s.sttClient.Transcribe(ctx, tmpPath)
		if err != nil {
			log.Printf("[Erro] Transcrição falhou: %v\n", err)
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Erro na transcrição de áudio.")
			return
		}
		if msg.Caption != "" {
			inputText = fmt.Sprintf("%s\n\n[Transcrição do Áudio]: %s", msg.Caption, strings.TrimSpace(transcription))
		} else {
			inputText = strings.TrimSpace(transcription)
		}
	} else if msg.Document != nil && msg.Document.FileID != "" {
		if documents.IsAudio(msg.Document.FileName) {
			tmpPath, err := s.tg.DownloadFileToTemp(ctx, msg.Document.FileID, "atlas_doc_audio_*")
			if err != nil {
				log.Printf("[Erro] Download de áudio falhou: %v\n", err)
				_ = s.tg.SendMessage(ctx, chatID, "⚠️ Erro ao receber arquivo.")
				return
			}
			defer os.Remove(tmpPath)

			transcription, err := s.sttClient.Transcribe(ctx, tmpPath)
			if err != nil {
				log.Printf("[Erro] Transcrição falhou: %v\n", err)
				_ = s.tg.SendMessage(ctx, chatID, "⚠️ Erro na transcrição do áudio.")
				return
			}
			if msg.Caption != "" {
				inputText = fmt.Sprintf("%s\n\n[Transcrição do Áudio]: %s", msg.Caption, strings.TrimSpace(transcription))
			} else {
				inputText = strings.TrimSpace(transcription)
			}
		} else if documents.IsSupportedDocument(msg.Document.FileName) {
			_ = s.tg.SendChatAction(ctx, chatID, "typing")
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("📄 *Documento recebido:* `%s`\n_Lendo e estruturando síntese executiva..._", msg.Document.FileName))
			_ = s.tg.SendChatAction(ctx, chatID, "typing")

			tmpPath, err := s.tg.DownloadFileToTemp(ctx, msg.Document.FileID, "atlas_doc_*")
			if err != nil {
				log.Printf("[Erro] Download de documento falhou: %v\n", err)
				_ = s.tg.SendMessage(ctx, chatID, "⚠️ Erro ao receber documento.")
				return
			}
			defer os.Remove(tmpPath)

			extracted, err := documents.ExtractText(ctx, tmpPath, msg.Document.FileName, documents.DefaultMaxChars)
			if err != nil {
				log.Printf("[Erro] Extração de texto falhou: %v\n", err)
				_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Erro ao processar o arquivo '%s': %v", msg.Document.FileName, err))
				return
			}

			if msg.Caption != "" {
				inputText = fmt.Sprintf("Documento '%s':\n%s\n\nInstrução do usuário: %s", msg.Document.FileName, extracted, msg.Caption)
			} else {
				inputText = fmt.Sprintf("Analise e elabore uma síntese executiva deste documento '%s':\n\n%s", msg.Document.FileName, extracted)
			}
		} else {
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Formato não suportado. Aceitos: Documentos (PDF, DOCX, XLSX, PPTX, ODT, ODS, ODP, EPUB, RTF, TXT, CSV, JSON, Markdown, código) e Áudios.")
			return
		}
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
		_ = s.db.RecordHistory(ctx, "user", inputText)
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
		_ = s.db.RecordHistory(ctx, "user", inputText)
		s.handleCommand(ctx, chatID, fastCmd)
		return
	}
	if reply, handled := s.handleAutomation(ctx, inputText); handled {
		_ = s.db.RecordHistory(ctx, "user", inputText)
		_ = s.tg.SendMessage(ctx, chatID, reply)
		_ = s.db.RecordHistory(ctx, "assistant", reply)
		return
	}

	// 3. Processamento Cognitivo e Socrático via NLU
	_ = s.tg.SendChatAction(ctx, chatID, "typing")
	now := time.Now().In(s.cfg.Location)
	currentDatetime := database.FormatDateTime(now)

	history, _ := s.db.GetRecentHistory(ctx, s.cfg.HistoryLimit)
	_ = s.db.RecordHistory(ctx, "user", inputText)
	memories, _ := s.db.GetAllUserMemories(ctx)
	latestIdeia, _ := s.db.GetLatestActiveIdeia(ctx)
	activeEvents, _ := s.db.GetUpcomingEvents(ctx, 10)
	routines, _ := s.db.GetAllRoutines(ctx)

	intent, err := s.nluClient.ParseIntent(ctx, inputText, currentDatetime, history, memories, latestIdeia, activeEvents, routines)
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

		res, err := s.nluClient.ResearchAnalyze(ctx, inputText, history)
		if err != nil {
			log.Printf("[Erro Modo Pesquisa] %v\n", err)
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Houve uma instabilidade no processamento da pesquisa.")
			return
		}

		if res.TotalTokens > 0 {
			_ = s.db.RecordTokenUsage(ctx, res.Provider, res.Model, res.PromptTokens, res.ComplTokens, res.TotalTokens)
		}

		researchResponse := nlu.SanitizeHumanResponse(res.Text)
		_ = s.tg.SendMessage(ctx, chatID, researchResponse)
		_ = s.db.RecordHistory(ctx, "assistant", researchResponse)
		return
	}

	// 3.2 Tarefa pesada / análise complexa moderada
	if intent.Complexidade == "pesada" && (intent.Tipo == "conversa" || intent.Tipo == "pesquisa") {
		curiosity := nlu.RandomCuriosity()
		waitingMsg := fmt.Sprintf("⏳ *Esta solicitação exige análise aprofundada.* Acionando o núcleo de processamento inteligente da NVIDIA (Nemotron)...\n\nEnquanto isso, uma curiosidade aleatória para você:\n🦩 _%s_", curiosity)
		_ = s.tg.SendMessage(ctx, chatID, waitingMsg)

		deepRes, err := s.nluClient.DeepAnalyze(ctx, inputText, history)
		if err != nil {
			log.Printf("[Erro NVIDIA Heavy] %v\n", err)
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Houve uma instabilidade momentânea na análise da NVIDIA.")
			return
		}

		if deepRes.TotalTokens > 0 {
			_ = s.db.RecordTokenUsage(ctx, deepRes.Provider, deepRes.Model, deepRes.PromptTokens, deepRes.ComplTokens, deepRes.TotalTokens)
		}

		deepResponse := nlu.SanitizeHumanResponse(deepRes.Text)
		_ = s.tg.SendMessage(ctx, chatID, deepResponse)
		_ = s.db.RecordHistory(ctx, "assistant", deepResponse)
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

	case "acao_programada":
		reply = s.saveScheduledAction(ctx, intent)
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

	case "limpar_lembretes":
		if isNo {
			return true, "👍 Operação cancelada. Seus lembretes e rotinas continuam intactos."
		}
		if isYes {
			total, err := s.db.ClearAllRoutines(ctx)
			if err != nil {
				return true, "⚠️ Ocorreu um erro ao tentar limpar os lembretes."
			}
			return true, fmt.Sprintf("🗑️ Todos os *%d* lembretes e rotinas programadas foram removidos com sucesso.", total)
		}
		return true, "⚠️ Confirmação não compreendida. Por segurança, seus lembretes foram mantidos intactos."

	case "limpar_tudo":
		if isNo {
			return true, "👍 Operação cancelada. Seus compromissos e lembretes continuam intactos."
		}
		if isYes {
			totalEv, _ := s.db.ClearAllEventos(ctx)
			totalRt, _ := s.db.ClearAllRoutines(ctx)
			return true, fmt.Sprintf("🗑️ *Sistema completamente limpo!*\nForam removidos *%d* compromisso(s) e *%d* lembrete(s).", totalEv, totalRt)
		}
		return true, "⚠️ Confirmação não compreendida. Seus dados foram mantidos intactos por segurança."
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
	deslocamento, preparo := 25, 35
	if intent.EstimativaDeslocamentoMin != nil && *intent.EstimativaDeslocamentoMin >= 0 {
		deslocamento = *intent.EstimativaDeslocamentoMin
	}
	if intent.EstimativaPreparoMin != nil && *intent.EstimativaPreparoMin >= 0 {
		preparo = *intent.EstimativaPreparoMin
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
		if _, err := time.Parse("15:04", horarioHM); err != nil {
			return "⚠️ Informe horário válido, como 08:30."
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
			return "⚠️ Para compromissos semanais, especifique o dia e o horário (ex: 'toda segunda às 14:00' ou 'toda semana exceto domingo às 08:00')."
		}
		if _, err := time.Parse("15:04", horarioHM); err != nil {
			return "⚠️ Informe horário válido, como 08:30."
		}

		parts := strings.Split(horarioHM, ":")
		hh, _ := strconv.Atoi(parts[0])
		mm := 0
		if len(parts) > 1 {
			mm, _ = strconv.Atoi(parts[1])
		}
		refTime := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, s.cfg.Location)
		buffers := calculator.Calculate(refTime, deslocamento, preparo, calculator.DefaultDelayMargin)

		// Múltiplos dias da semana ou exceções (ex: "exceto domingo e segunda", "segunda a sexta", "terça e quinta")
		if len(intent.DiasSemana) > 0 {
			var insertedDays []string
			for _, d := range intent.DiasSemana {
				if d < 0 || d > 6 {
					continue
				}
				dayVal := d
				_, err := s.db.InsertEventoCompleto(ctx, intent.Descricao, "", buffers.BufferPrep.Format("15:04"), buffers.BufferLeave.Format("15:04"), "semanal", &dayVal, nil, horarioHM, deslocamento, preparo)
				if err == nil {
					insertedDays = append(insertedDays, diasNomes[dayVal])
				}
			}
			if len(insertedDays) == 0 {
				return "⚠️ Não foi possível identificar os dias da semana solicitados."
			}
			return fmt.Sprintf("🎯 *Compromisso Recorrente Agendado (+15%% margem determinística)*\n\n📌 *Evento:* %s\n🔁 *Dias:* %s às *%s*\n🛁 *Preparo:* %s\n🚗 *Saída:* %s\n🛡️ *Margem:* +%d min de segurança diária",
				intent.Descricao, strings.Join(insertedDays, ", "), horarioHM, buffers.BufferPrep.Format("15:04"), buffers.BufferLeave.Format("15:04"), buffers.MarginMinutes)
		}

		diaSem := 1 // Default Segunda-feira se não especificado
		if intent.DiaSemana != nil && *intent.DiaSemana >= 0 && *intent.DiaSemana <= 6 {
			diaSem = *intent.DiaSemana
		}

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
		if _, err := time.Parse("15:04", horarioHM); err != nil {
			return "⚠️ Informe horário válido, como 08:30."
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
	case "santo_do_dia", "santo":
		santoText, err := catholic.FetchSantoDoDia(ctx)
		if err != nil {
			log.Printf("[Erro Santo] %v\n", err)
			return "⚠️ Não foi possível carregar as informações do Santo do Dia no momento."
		}
		return santoText

	case "versiculo_biblico", "versiculo", "biblia":
		ref := intent.Termo
		passageText, err := catholic.FetchPassage(ctx, ref)
		if err != nil {
			log.Printf("[Erro Versículo] %v\n", err)
			return fmt.Sprintf("⚠️ Erro ao consultar a Bíblia: %v", err)
		}
		return passageText

	case "cadastrar_conta":
		desc := intent.Termo
		if desc == "" {
			desc = intent.Descricao
		}
		dia := intent.DiaVencimento
		if dia <= 0 {
			dia = 10
		}
		cat := intent.Categoria
		if cat == "" {
			cat = "outros"
		}
		id, err := s.db.InsertConta(ctx, desc, dia, nil, intent.Valor, 1, cat)
		if err != nil {
			return fmt.Sprintf("⚠️ Falha ao cadastrar conta: %v", err)
		}
		valorStr := "Não informado"
		if intent.Valor > 0 {
			valorStr = fmt.Sprintf("R$ %.2f", intent.Valor)
		}
		return fmt.Sprintf("💳 *Conta Cadastrada com Sucesso! [ID: %d]*\n\n📌 *Conta:* %s\n📅 *Vencimento:* Todo dia %02d de cada mês\n💰 *Valor:* %s\n🔔 *Regra de Notificação:* Avisos contínuos a partir de **3 dias antes** (D-3, D-2, D-1 e no dia do vencimento).",
			id, desc, dia, valorStr)

	case "listar_contas", "contas":
		return s.formatContasList(ctx)

	case "pagar_conta", "baixar_conta":
		term := intent.Termo
		now := time.Now().In(s.cfg.Location)
		conta, err := s.db.FindContaByName(ctx, term)
		if err != nil || conta == nil {
			return fmt.Sprintf("⚠️ Conta '%s' não encontrada. Digite /contas para ver suas contas cadastradas.", term)
		}
		currMonth := now.Format("2006-01")
		if err := s.db.MarkContaPaid(ctx, conta.ID, currMonth); err != nil {
			return fmt.Sprintf("⚠️ Erro ao registrar pagamento: %v", err)
		}
		return fmt.Sprintf("✅ *Conta Marcada como Paga!*\n\n📌 *Conta:* %s\n📅 *Mês de Referência:* %s\n🎉 Não enviarei mais avisos desta conta até o próximo mês!",
			conta.Descricao, currMonth)

	case "apagar_conta":
		delName, err := s.db.DeleteContaByName(ctx, intent.Termo)
		if err != nil {
			return fmt.Sprintf("⚠️ Não foi possível apagar a conta '%s': %v", intent.Termo, err)
		}
		return fmt.Sprintf("🗑️ Conta *%s* removida do monitoramento de vencimentos.", delName)

	case "liturgia_das_horas", "horas":
		hourName := intent.Termo
		horasText, err := catholic.FetchLiturgiaDasHoras(ctx, hourName, s.cfg.Location)
		if err != nil {
			log.Printf("[Erro LiturgiaDasHoras] %v\n", err)
			return fmt.Sprintf("⚠️ Falha ao carregar a Liturgia das Horas: %v", err)
		}
		return horasText

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

	case "ler_emails", "emails":
		result, err := s.readEmails(ctx, intent.Termo)
		if err != nil {
			return fmt.Sprintf("⚠️ Falha ao ler e-mails: %v", err)
		}
		return result
	case "briefing":
		result, err := s.engine.BuildBriefing(ctx, s.today())
		if err != nil {
			return "⚠️ Não foi possível consultar os dados do briefing."
		}
		return result

	case "executar_codigo", "executar_python", "python":
		code := strings.TrimSpace(intent.Termo)
		if code == "" {
			code = strings.TrimSpace(intent.Conteudo)
		}
		if code == "" {
			return "⚠️ Por favor, forneça o código Python a ser executado."
		}
		code = strings.TrimPrefix(code, "```python")
		code = strings.TrimPrefix(code, "```py")
		code = strings.TrimPrefix(code, "```")
		code = strings.TrimSuffix(code, "```")
		code = strings.TrimSpace(code)

		res, err := s.interpreter.RunPython(ctx, code)
		if err != nil {
			return fmt.Sprintf("⚠️ Falha ao executar script Python: %v", err)
		}
		return res.FormatTelegramHUD()

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
			if err := s.db.CompleteEvent(ctx, intent.ID, s.today()); err != nil {
				return "⚠️ Não foi possível concluir esse compromisso."
			}
			return fmt.Sprintf("✅ Compromisso [%d] marcado como concluído.", intent.ID)
		}
		if intent.Termo != "" {
			id, desc, err := s.db.FindEventoByDesc(ctx, intent.Termo)
			if err == nil {
				if err := s.db.CompleteEvent(ctx, id, s.today()); err != nil {
					return "⚠️ Não foi possível concluir esse compromisso."
				}
				return fmt.Sprintf("✅ Compromisso *%s* marcado como concluído com sucesso.", desc)
			}
		}
		return "⚠️ Não localizei o compromisso pendente para marcar como concluído."

	case "apagar_evento":
		if intent.ID > 0 {
			_ = s.db.DeleteEvento(ctx, intent.ID)
			return fmt.Sprintf("🗑️ Compromisso [%d] excluído da sua agenda.", intent.ID)
		}
		cleanTerm := strings.ToLower(strings.TrimSpace(intent.Termo))
		if cleanTerm == "isso" || cleanTerm == "esse" || cleanTerm == "este" || cleanTerm == "ultimo" || cleanTerm == "o ultimo" || cleanTerm == "" {
			if latestEv, err := s.db.GetLatestPendingEvento(ctx); err == nil && latestEv != nil {
				_ = s.db.DeleteEvento(ctx, latestEv.ID)
				return fmt.Sprintf("🗑️ Último compromisso agendado (*%s*) foi excluído.", latestEv.Descricao)
			}
			if latestRt, err := s.db.GetLatestRoutine(ctx); err == nil && latestRt != nil {
				_ = s.db.DeleteRoutine(ctx, latestRt.ID)
				return fmt.Sprintf("🗑️ Último lembrete/rotina (*%s*) foi excluído.", latestRt.Nome)
			}
			return "⚠️ Não encontrei nenhum compromisso recente pendente para excluir."
		}

		// 1. Tenta apagar na agenda (todas as ocorrências)
		affected, desc, err := s.db.DeleteEventosByDesc(ctx, cleanTerm)
		if err == nil && affected > 0 {
			if affected > 1 {
				return fmt.Sprintf("🗑️ Compromisso *%s* excluído da sua agenda (%d ocorrências removidas).", desc, affected)
			}
			return fmt.Sprintf("🗑️ Compromisso *%s* excluído da sua agenda.", desc)
		}

		// 2. Se não achou na agenda, busca nas rotinas/lembretes (generalização)
		if nome, err := s.db.DeleteRoutineByName(ctx, cleanTerm); err == nil && nome != "" {
			return fmt.Sprintf("🗑️ Lembrete/rotina *%s* excluído com sucesso.", nome)
		}

		return fmt.Sprintf("⚠️ Não localizei nenhum compromisso ou lembrete correspondente a '%s'.", intent.Termo)

	case "apagar_rotina":
		if intent.ID > 0 {
			if err := s.db.DeleteRoutine(ctx, intent.ID); err == nil {
				return fmt.Sprintf("🗑️ Rotina [%d] apagada.", intent.ID)
			}
		}
		cleanTerm := strings.ToLower(strings.TrimSpace(intent.Termo))
		if cleanTerm == "isso" || cleanTerm == "esse" || cleanTerm == "este" || cleanTerm == "ultimo" || cleanTerm == "" {
			if latestRt, err := s.db.GetLatestRoutine(ctx); err == nil && latestRt != nil {
				_ = s.db.DeleteRoutine(ctx, latestRt.ID)
				return fmt.Sprintf("🗑️ Último lembrete/rotina (*%s*) foi excluído.", latestRt.Nome)
			}
			if latestEv, err := s.db.GetLatestPendingEvento(ctx); err == nil && latestEv != nil {
				_ = s.db.DeleteEvento(ctx, latestEv.ID)
				return fmt.Sprintf("🗑️ Último compromisso agendado (*%s*) foi excluído.", latestEv.Descricao)
			}
			return "⚠️ Não encontrei nenhuma rotina recente para apagar."
		}

		// 1. Tenta deletar rotina
		if nome, err := s.db.DeleteRoutineByName(ctx, cleanTerm); err == nil && nome != "" {
			return fmt.Sprintf("🗑️ Lembrete/rotina *%s* excluído com sucesso.", nome)
		}

		// 2. Se não achou em rotinas, busca nos eventos da agenda (generalização)
		affected, desc, err := s.db.DeleteEventosByDesc(ctx, cleanTerm)
		if err == nil && affected > 0 {
			return fmt.Sprintf("🗑️ Compromisso *%s* excluído da agenda.", desc)
		}

		return fmt.Sprintf("⚠️ Não localizei nenhuma rotina ou compromisso correspondente a '%s'.", intent.Termo)

	case "limpar_tudo", "delete_tudo":
		countEv, _ := s.db.CountAllEventos(ctx)
		countRt, _ := s.db.CountAllRoutines(ctx)
		if countEv == 0 && countRt == 0 {
			return "✨ O sistema já está completamente limpo. Não há compromissos nem lembretes registrados."
		}
		_ = s.db.SavePendingConfirmation(ctx, s.cfg.TelegramChatID, "limpar_tudo", 0, "")
		return fmt.Sprintf("⚠️ *Atenção:* Você tem certeza de que deseja *apagar TUDO* (%d compromisso(s) e %d lembrete(s))?\n\nEsta ação é permanente e irreversível.\n\n👉 Responda com *Sim* para confirmar ou *Não* para cancelar.", countEv, countRt)

	case "limpar_agenda":
		count, _ := s.db.CountAllEventos(ctx)
		if count == 0 {
			return "📅 Sua agenda já está limpa. Não há nenhum compromisso registrado."
		}
		_ = s.db.SavePendingConfirmation(ctx, s.cfg.TelegramChatID, "limpar_agenda", 0, "")
		return fmt.Sprintf("⚠️ *Atenção:* Você tem certeza de que deseja *apagar todos os %d compromissos* da sua agenda?\n\nEsta ação é permanente e irreversível.\n\n👉 Responda com *Sim* para confirmar ou *Não* para cancelar.", count)

	case "limpar_lembretes":
		count, _ := s.db.CountAllRoutines(ctx)
		if count == 0 {
			return "⏰ Nenhum lembrete ou rotina cadastrada no momento."
		}
		_ = s.db.SavePendingConfirmation(ctx, s.cfg.TelegramChatID, "limpar_lembretes", 0, "")
		return fmt.Sprintf("⚠️ *Atenção:* Você tem certeza de que deseja *apagar todos os %d lembretes e rotinas diárias*?\n\nEsta ação é permanente e irreversível.\n\n👉 Responda com *Sim* para confirmar ou *Não* para cancelar.", count)

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

	case "ver_calendario", "calendario", "ver_mes":
		return s.formatMonthlyCalendarView(ctx)

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
			if r.Tipo == "custom" && r.PromptCustom != nil && *r.PromptCustom != "" {
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
		return "⚠️ Ação não reconhecida. Nenhuma alteração foi realizada."
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
• 🌟 */santo* — Santo do dia católico, história e oração
• 📜 */versiculo <passagem>* — Bíblia Católica com Vulgata Latina (ambas)
• ⛪ */horas [hora]* — Liturgia das Horas (opcional: */horas ativar* ou */horas desativar*)
• 💳 */contas* — Suas contas, faturas e assinaturas (avisa 3 dias antes)
• 💰 */pagar <nome>* — Baixar conta como paga neste mês
• ➕ */conta <nome> dia <dia> [valor]* — Cadastrar nova conta
• 📰 */noticias <tema>* — Buscar notícias recentes na internet com links
• ⏰ */lembretes* — Ver mensagens diárias programadas
• 🗑️ */apagar <id>* — Apagar uma programação diária (ex: /apagar 3)
• 📅 */hoje* — Agenda de hoje com tempos de preparo e saída (+15%)
• 🎯 */metas* — Suas metas e progresso
• 📊 */tokens* — Relatório de consumo de tokens e limites
• 🗓️ */calendario* ou */mes* — Grade visual em emojis e link iCal
• 🛰️ */sonar <cmd>* ou *$ <cmd>* — Terminal Linux local (Zero Tokens)
• 🐍 */py <código>* — Interpretador Python local seguro
• 🕷️ */crawl <url>* — Rastrear e consolidar páginas de um site
• 🏛️ */swarm <tema>* — Conselho de 3 especialistas (Pesquisador, Analista, Crítico)
• ☀️ */briefing* / 🌙 */review* — Briefing matinal e auditoria socrática do dia
• 📬 */emails* — Ler e curar e-mails importantes (somente leitura)
• 📄 *Envio de Arquivos* — Envie PDFs, CSVs, TXT, código ou áudios para leitura direta

💬 *Comandos Naturais (Voz ou Texto):*
👉 _"$ git status"_ ou _"/sonar ls -la"_
👉 _"Anotar uma ideia: criar um app de assinatura para barbeiros"_
👉 _"Adicione à ideia anterior que o plano mensal será R$ 49"_
👉 _"Lembre-se que eu sou desenvolvedor e acordo às 06:30"_
👉 _"Qual é a liturgia diária de hoje?"_
👉 _"Todo dia às 09:00 mande uma mensagem dizendo: tomar creatina"_`
		_ = s.tg.SendMessage(ctx, chatID, msg)

	case "/liturgia":
		_ = s.tg.SendChatAction(ctx, chatID, "typing")
		text, err := web.FetchLiturgiaDiaria(ctx)
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Erro ao consultar a liturgia diária.")
			return
		}
		_ = s.tg.SendMessage(ctx, chatID, text)

	case "/santo":
		_ = s.tg.SendChatAction(ctx, chatID, "typing")
		text, err := catholic.FetchSantoDoDia(ctx)
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Erro ao consultar o Santo do Dia.")
			return
		}
		_ = s.tg.SendMessage(ctx, chatID, text)

	case "/versiculo", "/biblia":
		_ = s.tg.SendChatAction(ctx, chatID, "typing")
		ref := ""
		if len(parts) >= 2 {
			ref = strings.Join(parts[1:], " ")
		}
		text, err := catholic.FetchPassage(ctx, ref)
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Erro ao buscar passagem bíblica: %v", err))
			return
		}
		_ = s.tg.SendMessage(ctx, chatID, text)

	case "/horas", "/liturgiadashoras":
		_ = s.tg.SendChatAction(ctx, chatID, "typing")
		sub := ""
		if len(parts) >= 2 {
			sub = strings.ToLower(parts[1])
		}

		switch sub {
		case "ativar", "ligar", "on", "enable":
			routines, _ := s.db.GetAllRoutines(ctx)
			hasLaudes, hasVesperas, hasCompletas := false, false, false
			for _, r := range routines {
				if strings.Contains(r.Nome, "Liturgia das Horas") {
					if strings.Contains(r.Nome, "Laudes") {
						hasLaudes = true
					}
					if strings.Contains(r.Nome, "Vésperas") || strings.Contains(r.Nome, "Vesperas") {
						hasVesperas = true
					}
					if strings.Contains(r.Nome, "Completas") {
						hasCompletas = true
					}
					if r.Ativo == 0 {
						_, _ = s.db.ToggleRoutine(ctx, r.ID)
					}
				}
			}
			if !hasLaudes {
				prompt := "Liturgia das Horas — Laudes"
				_, _ = s.db.InsertRoutine(ctx, "Liturgia das Horas (Laudes)", "06:00", "custom", &prompt)
			}
			if !hasVesperas {
				prompt := "Liturgia das Horas — Vésperas"
				_, _ = s.db.InsertRoutine(ctx, "Liturgia das Horas (Vésperas)", "18:00", "custom", &prompt)
			}
			if !hasCompletas {
				prompt := "Liturgia das Horas — Completas"
				_, _ = s.db.InsertRoutine(ctx, "Liturgia das Horas (Completas)", "21:30", "custom", &prompt)
			}
			confirmMsg := "🔔 *Liturgia das Horas Ativada!*\n\nVocê receberá diariamente as orações nos momentos canônicos:\n• 🌅 *06:00* — Laudes (Oração da Manhã)\n• 🌇 *18:00* — Vésperas (Oração da Tarde)\n• 🌙 *21:30* — Completas (Oração da Noite)\n\n_Para desativar a qualquer momento: /horas desativar_"
			_ = s.tg.SendMessage(ctx, chatID, confirmMsg)
			return

		case "desativar", "desligar", "off", "disable":
			routines, _ := s.db.GetAllRoutines(ctx)
			for _, r := range routines {
				if strings.Contains(r.Nome, "Liturgia das Horas") {
					_ = s.db.DeleteRoutine(ctx, r.ID)
				}
			}
			confirmMsg := "🔕 *Liturgia das Horas Desativada.*\n\nO envio automático foi suspenso. Você ainda pode rezar sob demanda digitando:\n👉 `/horas` (hora atual)\n👉 `/horas laudes`\n👉 `/horas vesperas`\n👉 `/horas completas`"
			_ = s.tg.SendMessage(ctx, chatID, confirmMsg)
			return

		case "status":
			routines, _ := s.db.GetAllRoutines(ctx)
			var activeList []string
			for _, r := range routines {
				if strings.Contains(r.Nome, "Liturgia das Horas") && r.Ativo == 1 {
					activeList = append(activeList, fmt.Sprintf("• *%s* às %s", r.Nome, r.Horario))
				}
			}
			if len(activeList) > 0 {
				msg := "🔔 *Liturgia das Horas: ATIVADA 🟢*\n\nHorários diários programados:\n" + strings.Join(activeList, "\n") + "\n\n_Para desligar: /horas desativar_"
				_ = s.tg.SendMessage(ctx, chatID, msg)
			} else {
				msg := "🔕 *Liturgia das Horas: DESATIVADA 🔴*\n\nO envio automático está desligado.\n\n_Para ativar o envio diário (06h, 18h e 21h30): /horas ativar_"
				_ = s.tg.SendMessage(ctx, chatID, msg)
			}
			return

		default:
			target := sub
			if len(parts) >= 2 {
				target = strings.Join(parts[1:], " ")
			}
			text, err := catholic.FetchLiturgiaDasHoras(ctx, target, s.cfg.Location)
			if err != nil {
				_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Erro ao consultar a Liturgia das Horas: %v", err))
				return
			}
			_ = s.tg.SendMessage(ctx, chatID, text)
			return
		}

	case "/contas":
		msg := s.formatContasList(ctx)
		_ = s.tg.SendMessage(ctx, chatID, msg)

	case "/conta":
		if len(parts) < 2 {
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Uso: `/conta <nome> dia <dia> [valor]`\nEx: `/conta Netflix dia 10 55.90` ou `/conta Cartão Nubank dia 15`")
			return
		}
		raw := strings.Join(parts[1:], " ")
		desc := raw
		dia := 10
		var valor float64

		reDia := regexp.MustCompile(`(?i)(?:dia\s+|vence\s+|todo\s+dia\s+)(\d{1,2})`)
		if m := reDia.FindStringSubmatch(raw); len(m) >= 2 {
			if d, err := strconv.Atoi(m[1]); err == nil && d >= 1 && d <= 31 {
				dia = d
			}
			desc = strings.TrimSpace(reDia.ReplaceAllString(desc, ""))
		}

		reVal := regexp.MustCompile(`(?i)(?:r\$\s*|valor\s*)?(\d+(?:[.,]\d{2})?)`)
		if m := reVal.FindStringSubmatch(desc); len(m) >= 2 {
			valStr := strings.ReplaceAll(m[1], ",", ".")
			if v, err := strconv.ParseFloat(valStr, 64); err == nil {
				valor = v
				desc = strings.TrimSpace(reVal.ReplaceAllString(desc, ""))
			}
		}

		if desc == "" {
			desc = "Conta Mensal"
		}

		id, err := s.db.InsertConta(ctx, desc, dia, nil, valor, 1, "outros")
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Erro ao cadastrar conta: %v", err))
			return
		}

		valorStr := "Não informado"
		if valor > 0 {
			valorStr = fmt.Sprintf("R$ %.2f", valor)
		}
		msg := fmt.Sprintf("💳 *Conta Cadastrada! [ID: %d]*\n\n📌 *Conta:* %s\n📅 *Vencimento:* Todo dia %02d\n💰 *Valor:* %s\n🔔 *Avisos:* 3 dias antes (D-3, D-2, D-1 e no dia do vencimento)!",
			id, desc, dia, valorStr)
		_ = s.tg.SendMessage(ctx, chatID, msg)

	case "/pagar":
		if len(parts) < 2 {
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Uso: `/pagar <nome ou ID da conta>` (ex: `/pagar netflix` ou `/pagar nubank`)")
			return
		}
		term := strings.Join(parts[1:], " ")
		now := time.Now().In(s.cfg.Location)
		conta, err := s.db.FindContaByName(ctx, term)
		if err != nil || conta == nil {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Nenhuma conta ativa encontrada para '%s'. Digite /contas para ver a lista.", term))
			return
		}
		currMonth := now.Format("2006-01")
		if err := s.db.MarkContaPaid(ctx, conta.ID, currMonth); err != nil {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Erro ao registrar pagamento: %v", err))
			return
		}
		msg := fmt.Sprintf("✅ *Conta Marcada como Paga!*\n\n📌 *Conta:* %s\n📅 *Mês de Referência:* %s\n🎉 Não enviarei mais avisos desta conta até o próximo mês!",
			conta.Descricao, currMonth)
		_ = s.tg.SendMessage(ctx, chatID, msg)

	case "/apagar_conta":
		if len(parts) < 2 {
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Uso: `/apagar_conta <nome ou ID da conta>`")
			return
		}
		term := strings.Join(parts[1:], " ")
		delName, err := s.db.DeleteContaByName(ctx, term)
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Não foi possível apagar a conta '%s': %v", term, err))
			return
		}
		_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("🗑️ Conta *%s* removida do monitoramento de vencimentos.", delName))

	case "/noticias":
		_ = s.tg.SendChatAction(ctx, chatID, "typing")
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

		res, err := s.nluClient.ResearchAnalyze(ctx, tema, nil)
		if err != nil {
			log.Printf("[Erro Comando /pesquisa] %v\n", err)
			_ = s.tg.SendMessage(ctx, chatID, "⚠️ Falha ao executar pesquisa aprofundada.")
			return
		}

		if res.TotalTokens > 0 {
			_ = s.db.RecordTokenUsage(ctx, res.Provider, res.Model, res.PromptTokens, res.ComplTokens, res.TotalTokens)
		}

		cleanResponse := nlu.SanitizeHumanResponse(res.Text)
		_ = s.tg.SendMessage(ctx, chatID, cleanResponse)
		_ = s.db.RecordHistory(ctx, "assistant", cleanResponse)
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
		view := s.formatMonthlyCalendarView(ctx)
		_ = s.tg.SendMessage(ctx, chatID, view)

	case "/rotinas", "/lembretes":
		text := s.executeAppAction(ctx, &nlu.IntentResult{Acao: "listar_lembretes"})
		_ = s.tg.SendMessage(ctx, chatID, text)
		_ = s.db.RecordHistory(ctx, "assistant", text)

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

	case "/limpar_lembretes", "/limpar_rotinas":
		count, _ := s.db.CountAllRoutines(ctx)
		if count == 0 {
			_ = s.tg.SendMessage(ctx, chatID, "⏰ Nenhum lembrete ou rotina cadastrada no momento.")
			return
		}
		_ = s.db.SavePendingConfirmation(ctx, chatID, "limpar_lembretes", 0, "")
		msg := fmt.Sprintf("⚠️ *Atenção:* Você tem certeza de que deseja *apagar todos os %d lembretes e rotinas diárias*?\n\nEsta ação é permanente e irreversível.\n\n👉 Responda com *Sim* para confirmar ou *Não* para cancelar.", count)
		_ = s.tg.SendMessage(ctx, chatID, msg)

	case "/limpar_tudo", "/delete_tudo":
		countEv, _ := s.db.CountAllEventos(ctx)
		countRt, _ := s.db.CountAllRoutines(ctx)
		if countEv == 0 && countRt == 0 {
			_ = s.tg.SendMessage(ctx, chatID, "✨ O sistema já está completamente limpo. Não há compromissos nem lembretes registrados.")
			return
		}
		_ = s.db.SavePendingConfirmation(ctx, chatID, "limpar_tudo", 0, "")
		msg := fmt.Sprintf("⚠️ *Atenção:* Você tem certeza de que deseja *apagar TUDO* (%d compromisso(s) e %d lembrete(s))?\n\nEsta ação é permanente e irreversível.\n\n👉 Responda com *Sim* para confirmar ou *Não* para cancelar.", countEv, countRt)
		_ = s.tg.SendMessage(ctx, chatID, msg)

	case "/sonar", "$":
		cmdStr := strings.TrimSpace(strings.TrimPrefix(cmdText, cmd))
		if cmdStr == "" {
			_ = s.tg.SendMessage(ctx, chatID, "🛰️ *ATLAS ⇄ SONAR Bridge (Terminal Linux)*\n\nExecute comandos bash diretamente na sua máquina local com zero tokens:\n\n• `/sonar git status`\n• `/sonar git diff`\n• `/sonar ls -la`\n• `/sonar df -h`\n• `$ <comando bash>`\n\n_Atalho rápido: use `$ comando` para executar direto!_")
			return
		}

		res, err := s.sonarClient.Execute(ctx, cmdStr, "")
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ *Falha na execução Sonar:*\n`%v`", err))
			return
		}

		header := fmt.Sprintf("🛰️ *SONAR CLI* (`$ %s` • Zero Tokens)\n", cmdStr)
		cleanOut := strings.TrimSpace(res.Output)
		if cleanOut == "" {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("%s✅ Executado com sucesso (sem retorno de texto).", header))
			return
		}

		if len(cleanOut) > 3500 {
			cleanOut = cleanOut[:3450] + "\n... [saída truncada]"
		}
		_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("%s```text\n%s\n```", header, cleanOut))

	case "/py", "/python":
		_ = s.tg.SendChatAction(ctx, chatID, "typing")
		code := strings.TrimSpace(strings.TrimPrefix(cmdText, cmd))
		if code == "" {
			_ = s.tg.SendMessage(ctx, chatID, "🐍 *ATLAS — Interpretador Python*\n\nExecute código Python diretamente no ambiente local.\n\nUso:\n`/py print('Olá mundo')`\nou envie blocos de código com `/py`.")
			return
		}
		code = strings.TrimPrefix(code, "```python")
		code = strings.TrimPrefix(code, "```py")
		code = strings.TrimPrefix(code, "```")
		code = strings.TrimSuffix(code, "```")
		code = strings.TrimSpace(code)

		res, err := s.interpreter.RunPython(ctx, code)
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Falha ao executar script: %v", err))
			return
		}
		_ = s.tg.SendMessage(ctx, chatID, res.FormatTelegramHUD())

	case "/crawl", "/rastrear":
		targetURL := strings.TrimSpace(strings.TrimPrefix(cmdText, cmd))
		if targetURL == "" {
			_ = s.tg.SendMessage(ctx, chatID, "🕷️ *ATLAS — Web Crawler*\n\nRastreia múltiplas páginas internas de um site e consolida o conteúdo.\n\nUso: `/crawl https://exemplo.com`")
			return
		}
		if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
			targetURL = "https://" + targetURL
		}

		_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("🕷️ *Iniciando Rastreamento Web...*\nExplorando páginas internas de `%s` (limite: 5 páginas)...", targetURL))

		res, err := s.crawler.Crawl(ctx, targetURL, crawler.DefaultOptions())
		if err != nil || len(res.Pages) == 0 {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Falha ao rastrear site: %v", err))
			return
		}

		summaryMsg := fmt.Sprintf("🕷️ *Rastreamento Concluído!* (`%s`)\n📄 *Páginas varridas:* %d\n⏱️ *Tempo:* %dms\n\n", res.Host, len(res.Pages), res.Duration.Milliseconds())
		for i, p := range res.Pages {
			summaryMsg += fmt.Sprintf("• *%d. %s*\n  `%s`\n", i+1, p.Title, p.URL)
		}
		summaryMsg += "\n_Envie `/pesquisa <termo>` ou faça perguntas sobre o conteúdo dessas páginas._"
		_ = s.tg.SendMessage(ctx, chatID, summaryMsg)

	case "/swarm", "/deliberar", "/conselho":
		topic := strings.TrimSpace(strings.TrimPrefix(cmdText, cmd))
		if topic == "" {
			_ = s.tg.SendMessage(ctx, chatID, "🏛️ *ATLAS — Conselho de Especialistas (Multi-Agentes Swarm)*\n\nDespacha três agentes simultâneos (Pesquisador, Analista e Crítico Socrático) para uma deliberação aprofundada.\n\nUso: `/swarm Devo lançar o produto agora?`")
			return
		}

		_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("🏛️ *Conselho de Inteligência Convocado!*\n\nAcionando *Pesquisador*, *Analista* e *Crítico Socrático* em paralelo sobre:\n👉 _%s_...", topic))

		delib, err := s.swarm.Deliberate(ctx, topic)
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Falha na deliberação dos especialistas: %v", err))
			return
		}

		if delib.TotalTokens > 0 {
			_ = s.db.RecordTokenUsage(ctx, "nvidia-swarm", "nemotron-council", 0, 0, delib.TotalTokens)
		}

		hudMsg := delib.FormatTelegramHUD()
		_ = s.tg.SendMessage(ctx, chatID, hudMsg)
		_ = s.db.RecordHistory(ctx, "assistant", hudMsg)

	case "/briefing":
		text, err := s.engine.BuildBriefing(ctx, s.today())
		if err != nil {
			text = "⚠️ Não foi possível consultar os dados do briefing."
		}
		_ = s.tg.SendMessage(ctx, chatID, text)
		_ = s.db.RecordHistory(ctx, "assistant", text)

	case "/review":
		now := time.Now().In(s.cfg.Location)
		todayStr := now.Format("2006-01-02")
		events, _ := s.db.GetEventsForDay(ctx, todayStr)
		goals, _ := s.db.GetActiveGoals(ctx, 5)

		_ = s.tg.SendMessage(ctx, chatID, "🌙 *Compilando sua Auditoria do Dia...*")

		aiReview, err := s.nluClient.GenerateReview(ctx, todayStr, events, goals)
		if err == nil && aiReview != "" {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("🌙 *ATLAS — Fechamento do Dia*\n\n%s", aiReview))
		} else {
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("🌙 *ATLAS — Fechamento do Dia*\n\nO dia está encerrando. Suas tarefas foram cumpridas ou houve procrastinação hoje?"))
		}

	case "/email", "/emails":
		if !s.emailReader.IsConfigured() {
			msg := "📬 *Configuração de E-mail (Somente Leitura)*\n\nPara o ATLAS monitorar seus e-mails e destacar assuntos importantes, adicione no seu arquivo `.env`:\n\n```env\nIMAP_HOST=imap.gmail.com\nIMAP_PORT=993\nIMAP_USER=seu_email@gmail.com\nIMAP_PASS=sua_senha_de_app\n```\n\n*(Dica Gmail: gere uma Senha de App em: Gerenciar Conta Google ➔ Segurança ➔ Senhas de App)*"
			_ = s.tg.SendMessage(ctx, chatID, msg)
			return
		}

		_ = s.tg.SendMessage(ctx, chatID, "📬 *Acessando sua caixa de entrada (modo seguro somente-leitura)...*")

		query := strings.TrimSpace(strings.TrimPrefix(cmdText, cmd))
		curated, err := s.readEmails(ctx, query)
		if err != nil {
			log.Printf("[Erro Email] %v\n", err)
			_ = s.tg.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Falha ao ler e-mails: %v", err))
			return
		}

		_ = s.tg.SendMessage(ctx, chatID, curated)
		_ = s.db.RecordHistory(ctx, "assistant", curated)

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
					if desloc < 0 {
						desloc = 25
					}
					prep := ev.PreparoMin
					if prep < 0 {
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

func (s *Server) formatMonthlyCalendarView(ctx context.Context) string {
	now := time.Now().In(s.cfg.Location)
	counts, _ := s.db.GetMonthEventCounts(ctx, now.Year(), now.Month())
	grid := calendar.RenderEmojiCalendar(now.Year(), now.Month(), now, counts)

	var sb strings.Builder
	sb.WriteString(grid)

	lastDay := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, s.cfg.Location).Day()
	var monthEvents []string
	for d := 1; d <= lastDay; d++ {
		dateStr := fmt.Sprintf("%04d-%02d-%02d", now.Year(), int(now.Month()), d)
		evts, err := s.db.GetEventsForDay(ctx, dateStr)
		if err == nil && len(evts) > 0 {
			for _, ev := range evts {
				badge := ""
				switch ev.Frequencia {
				case "diario":
					badge = " 🔄"
				case "semanal":
					badge = " 🔁"
				case "mensal":
					badge = " 🗓️"
				}
				horarioDisplay := ev.HorarioTime
				if horarioDisplay == "" && ev.HorarioOficial != "" {
					if t, parseErr := parseTimeFlexible(ev.HorarioOficial, s.cfg.Location); parseErr == nil {
						horarioDisplay = t.Format("15:04")
					}
				}
				monthEvents = append(monthEvents, fmt.Sprintf("• *%02d/%02d* às *%s* — %s%s", d, int(now.Month()), horarioDisplay, ev.Descricao, badge))
			}
		}
	}

	if len(monthEvents) > 0 {
		sb.WriteString("\n\n📌 *Compromissos Agendados Deste Mês:*\n")
		maxShow := 15
		if len(monthEvents) > maxShow {
			for i := 0; i < maxShow; i++ {
				sb.WriteString(monthEvents[i] + "\n")
			}
			sb.WriteString(fmt.Sprintf("_... e mais %d compromisso(s) recorrente(s)_\n", len(monthEvents)-maxShow))
		} else {
			for _, me := range monthEvents {
				sb.WriteString(me + "\n")
			}
		}
	} else {
		sb.WriteString("\n\n✨ _Nenhum compromisso agendado para este mês._")
	}

	calURL := fmt.Sprintf("%s/calendar.ics?token=%s", s.getPublicURL(), s.cfg.CalendarToken)
	sb.WriteString(fmt.Sprintf("\n\n🔗 _Sync Google/Apple Calendar:_ `%s`", calURL))

	return sb.String()
}

func (s *Server) formatContasList(ctx context.Context) string {
	contas, err := s.db.GetActiveContas(ctx)
	if err != nil || len(contas) == 0 {
		return "💳 *Contas e Assinaturas*\n\nNenhuma conta cadastrada no momento.\n\n_Para adicionar, envie:_ `/conta Netflix dia 10 55.90` _ou em linguagem natural:_ 'anotar conta nubank dia 15 valor 400'."
	}

	now := time.Now().In(s.cfg.Location)
	currMonthStr := now.Format("2006-01")
	todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.cfg.Location)

	var sb strings.Builder
	sb.WriteString("💳 *Contas e Assinaturas Monitoradas*\n")
	sb.WriteString("_(Avisos contínuos a partir de 3 dias antes do vencimento)_\n\n")

	for _, c := range contas {
		year := now.Year()
		month := now.Month()
		day := c.DiaVencimento
		maxDays := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
		if day > maxDays {
			day = maxDays
		}
		dueDate := time.Date(year, month, day, 0, 0, 0, 0, s.cfg.Location)
		diffDays := int(dueDate.Sub(todayMidnight).Hours() / 24)

		var statusBadge string
		if c.UltimoMesPago != nil && *c.UltimoMesPago == currMonthStr {
			statusBadge = "🟢 Pago este mês"
		} else {
			switch {
			case diffDays < 0:
				statusBadge = fmt.Sprintf("🔴 Venceu há %d dia(s)", -diffDays)
			case diffDays == 0:
				statusBadge = "🚨 VENCE HOJE!"
			case diffDays == 1:
				statusBadge = "⏳ Vence amanhã!"
			case diffDays <= 3:
				statusBadge = fmt.Sprintf("⚠️ Faltam %d dias", diffDays)
			default:
				statusBadge = fmt.Sprintf("📅 Faltam %d dias", diffDays)
			}
		}

		valorStr := ""
		if c.Valor > 0 {
			valorStr = fmt.Sprintf(" — R$ %.2f", c.Valor)
		}

		sb.WriteString(fmt.Sprintf("• [%d] *%s*%s\n  🗓️ Dia %02d | Status: %s\n\n",
			c.ID, c.Descricao, valorStr, c.DiaVencimento, statusBadge))
	}

	sb.WriteString("_Comandos úteis:_\n")
	sb.WriteString("👉 `/pagar <nome>` — Marcar conta paga neste mês\n")
	sb.WriteString("👉 `/apagar_conta <nome>` — Remover do monitoramento\n")
	sb.WriteString("👉 `/conta <nome> dia <dia> [valor]` — Adicionar nova conta")

	return sb.String()
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
	clean := strings.Trim(synonyms.RemoveAccents(norm), ".,!?\"'#;:")
	// Mutations and recurrence must retain their parameters for automation/NLU.
	tokensForOperation := synonyms.TokenizeAndCanonicalize(clean)
	bulkCleanup := synonyms.MatchCanonical(tokensForOperation, "apagar") && (synonyms.MatchCanonical(tokensForOperation, "todo") || len(tokensForOperation) <= 3 && (synonyms.MatchCanonical(tokensForOperation, "agenda") || synonyms.MatchCanonical(tokensForOperation, "lembrete")))
	deleteBill := synonyms.MatchCanonical(tokensForOperation, "apagar") && synonyms.MatchCanonical(tokensForOperation, "conta")

	// 1. Terminal Sonar / Bash Shortcuts
	if strings.HasPrefix(clean, "sonar ") {
		return "/sonar " + strings.TrimSpace(text[len("sonar "):]), true
	}
	if strings.HasPrefix(clean, "$") {
		return "/sonar " + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "$")), true
	}
	if clean == "git status" || clean == "git diff" || clean == "git branch" || clean == "git log" ||
		clean == "pwd" || clean == "ls" || clean == "ls -la" || clean == "df -h" || clean == "free -m" || clean == "uptime" {
		return "/sonar " + clean, true
	}

	// 1.1 Ferramentas Executivas Globais (Python Sandbox, Web Crawler & Swarm)
	if strings.Contains(text, "```python") || strings.Contains(text, "```py") {
		// Bloco de código python formatado
		code := text
		if idx := strings.Index(code, "```python"); idx != -1 {
			code = code[idx+len("```python"):]
		} else if idx := strings.Index(code, "```py"); idx != -1 {
			code = code[idx+len("```py"):]
		}
		if endIdx := strings.LastIndex(code, "```"); endIdx != -1 {
			code = code[:endIdx]
		}
		if strings.TrimSpace(code) != "" {
			return "/py " + strings.TrimSpace(code), true
		}
	}
	if strings.HasPrefix(clean, "py ") || strings.HasPrefix(clean, "py:") {
		return "/py " + strings.TrimSpace(text[len("py "):]), true
	}
	if strings.HasPrefix(clean, "python ") || strings.HasPrefix(clean, "python:") {
		return "/py " + strings.TrimSpace(text[len("python "):]), true
	}
	for _, p := range []string{"executar python ", "rodar python ", "executa python ", "roda python ", "executar script ", "rodar script ", "executa script ", "roda script "} {
		if strings.HasPrefix(clean, p) {
			return "/py " + strings.TrimSpace(text[len(p):]), true
		}
	}
	if strings.HasPrefix(clean, "crawl ") {
		return "/crawl " + strings.TrimSpace(text[len("crawl "):]), true
	}
	if strings.HasPrefix(clean, "crawler ") {
		return "/crawl " + strings.TrimSpace(text[len("crawler "):]), true
	}
	if strings.HasPrefix(clean, "rastrear ") {
		return "/crawl " + strings.TrimSpace(text[len("rastrear "):]), true
	}
	if strings.HasPrefix(clean, "swarm ") {
		return "/swarm " + strings.TrimSpace(text[len("swarm "):]), true
	}
	if strings.HasPrefix(clean, "deliberar ") {
		return "/swarm " + strings.TrimSpace(text[len("deliberar "):]), true
	}
	if strings.HasPrefix(clean, "conselho ") {
		return "/swarm " + strings.TrimSpace(text[len("conselho "):]), true
	}
	if automationOperation(clean) && (!bulkCleanup && !deleteBill || synonyms.MatchCanonical(tokensForOperation, "nao")) {
		return "", false
	}
	if clean == "briefing" || clean == "meu briefing" || clean == "briefing matinal" {
		return "/briefing", true
	}
	if clean == "review" || clean == "fechamento" || clean == "fechamento do dia" || clean == "auditoria do dia" {
		return "/review", true
	}

	// E-mail (somente leitura e curadoria de mensagens)
	emailTerm := synonyms.MatchCanonical(tokensForOperation, "email") || strings.Contains(clean, "email") || strings.Contains(clean, "e-mail") || strings.Contains(clean, "e mail") ||
		strings.Contains(clean, "inbox") || strings.Contains(clean, "caixa de entrada") || strings.Contains(clean, "correio")
	if emailTerm {
		isConfigIntent := strings.Contains(clean, "meu email e ") || strings.Contains(clean, "meu email:") ||
			strings.Contains(clean, "mudar email") || strings.Contains(clean, "alterar email") || strings.Contains(clean, "trocar email")
		if !isConfigIntent {
			if email.HasQueryFilters(clean) {
				return "/emails " + text, true
			}
			return "/emails", true
		}
	}

	tokens := synonyms.TokenizeAndCanonicalize(clean)

	// 2. Limpeza com Confirmação (Prioridade para segurança)
	// Aceita sinônimos de exclusão: apagar, deletar, limpar, remover, excluir, zerar, eliminar, resetar...
	if synonyms.MatchCanonical(tokens, "apagar") {
		// Se for apagar tudo / deletar tudo / limpar tudo (sem especificar apenas agenda ou lembrete)
		if clean == "apagar tudo" || clean == "deletar tudo" || clean == "limpar tudo" || clean == "zerar tudo" || clean == "excluir tudo" ||
			(synonyms.MatchCanonical(tokens, "todo") && len(tokens) <= 2) {
			return "/limpar_tudo", true
		}
		if synonyms.MatchCanonical(tokens, "lembrete") {
			if strings.Contains(clean, "limpar") || strings.Contains(clean, "zerar") || synonyms.MatchCanonical(tokens, "todo") || len(tokens) <= 3 {
				return "/limpar_lembretes", true
			}
		}
		if synonyms.MatchCanonical(tokens, "agenda") || synonyms.MatchCanonical(tokens, "todo") {
			if strings.Contains(clean, "limpar") || strings.Contains(clean, "zerar") || synonyms.MatchCanonical(tokens, "todo") || len(tokens) <= 3 {
				return "/limpar_agenda", true
			}
		}
	}

	// 3. Santo do Dia, Bíblia Católica & Vulgata, Liturgia
	if synonyms.MatchCanonical(tokens, "santo") || strings.Contains(clean, "santo do dia") || strings.Contains(clean, "santo de hoje") ||
		strings.Contains(clean, "qual o santo") || strings.Contains(clean, "quem e o santo") || strings.Contains(clean, "padroeiro de hoje") {
		return "/santo", true
	}

	if synonyms.MatchCanonical(tokens, "versiculo") || strings.Contains(clean, "versiculo") || strings.Contains(clean, "vulgata") ||
		strings.Contains(clean, "passagem biblica") || strings.HasPrefix(clean, "biblia") {
		trimmedRef := clean
		for _, prefix := range []string{"versiculo do dia", "versiculo de hoje", "versiculo aleatorio", "me de um versiculo", "leia o versiculo", "versiculo", "biblia", "passagem biblica", "vulgata de", "vulgata"} {
			trimmedRef = strings.TrimSpace(strings.TrimPrefix(trimmedRef, prefix))
		}
		if trimmedRef != "" {
			return "/versiculo " + trimmedRef, true
		}
		return "/versiculo", true
	}

	// Referência bíblica direta (ex: "João 3:16", "Sl 23:1", "Mt 6:9-13", "Sabedoria 3:1")
	if clean != "hoje" && clean != "dia" && clean != "aleatorio" {
		if _, _, _, _, err := catholic.ParseReference(clean); err == nil && len(tokens) <= 5 && !strings.Contains(clean, "marcar") && !strings.Contains(clean, "agendar") {
			return "/versiculo " + clean, true
		}
	}

	// Liturgia das Horas
	if strings.Contains(clean, "liturgia das horas") || strings.Contains(clean, "liturgiadashoras") ||
		strings.Contains(clean, "laudes") || strings.Contains(clean, "vesperas") || strings.Contains(clean, "completas") ||
		strings.Contains(clean, "oficio das leituras") {
		if strings.Contains(clean, "desativar") || strings.Contains(clean, "desligar") || strings.Contains(clean, "off") || strings.Contains(clean, "cancelar") {
			return "/horas desativar", true
		}
		if strings.Contains(clean, "ativar") || strings.Contains(clean, "ligar") || strings.Contains(clean, "on") {
			return "/horas ativar", true
		}
		if strings.Contains(clean, "status") {
			return "/horas status", true
		}
		if strings.Contains(clean, "laudes") {
			return "/horas laudes", true
		}
		if strings.Contains(clean, "vesperas") {
			return "/horas vesperas", true
		}
		if strings.Contains(clean, "completas") {
			return "/horas completas", true
		}
		if strings.Contains(clean, "oficio") {
			return "/horas oficio", true
		}
		return "/horas", true
	}

	if strings.Contains(clean, "liturgia") || strings.Contains(clean, "evangelho") {
		return "/liturgia", true
	}

	// 3.1 Contas, Cartões, Faturas e Assinaturas (Alerta contínuo 3 dias antes)
	if (synonyms.MatchCanonical(tokens, "conta") || strings.Contains(clean, "fatura") || strings.Contains(clean, "assinatura")) &&
		(strings.Contains(clean, "apagar") || strings.Contains(clean, "deletar") || strings.Contains(clean, "remover")) {
		term := clean
		for _, prefix := range []string{"apagar conta", "deletar conta", "remover conta", "apagar", "deletar", "remover"} {
			term = strings.TrimSpace(strings.TrimPrefix(term, prefix))
		}
		if term != "" {
			return "/apagar_conta " + term, true
		}
	}

	if clean == "contas a pagar" || clean == "contas para pagar" || clean == "contas" || clean == "minhas contas" ||
		clean == "ver contas" || clean == "listar contas" || clean == "quais contas vencem" || clean == "faturas" || clean == "assinaturas" ||
		((synonyms.MatchCanonical(tokens, "conta") || strings.Contains(clean, "fatura") || strings.Contains(clean, "assinatura")) && synonyms.MatchCanonical(tokens, "ver")) {
		if !strings.Contains(clean, "anotar") && !strings.Contains(clean, "cadastrar") && !strings.Contains(clean, "nova") && !strings.Contains(clean, "adicionar") && !strings.Contains(clean, "dia") {
			return "/contas", true
		}
	}

	if synonyms.MatchCanonical(tokens, "pagar") {
		term := clean
		for _, prefix := range []string{"paguei o", "paguei a", "paguei", "pagar o", "pagar a", "pagar", "marcar como pago", "marcar como paga", "quitei o", "quitei a", "quitei", "baixar conta", "baixar a conta", "baixar"} {
			term = strings.TrimSpace(strings.TrimPrefix(term, prefix))
		}
		for _, sfx := range []string{" pago", " paga", " quitado", " quitada"} {
			term = strings.TrimSpace(strings.TrimSuffix(term, sfx))
		}
		if term != "" && !strings.Contains(term, "quanto") && !strings.Contains(term, "quando") && !strings.Contains(term, "como") && term != "contas a pagar" {
			return "/pagar " + term, true
		}
	}

	// 4. CALENDÁRIO / MÊS COMPLETO (Separado categoricamente de "hoje")
	// Captura: "calendario", "me mostre o calendario", "ver calendario", "calendario do mes", "ver mes", "grade", "mensal", etc.
	if synonyms.MatchCanonical(tokens, "calendario") || strings.Contains(clean, "do mes") || strings.Contains(clean, "deste mes") {
		if !strings.Contains(clean, "marcar") && !strings.Contains(clean, "agendar") && !strings.Contains(clean, "adicionar") {
			return "/mes", true
		}
	}

	// 5. HOJE (Apenas a agenda do dia atual com tempos de preparo e saída)
	if strings.Contains(clean, "hoje") || strings.Contains(clean, "pra hoje") || strings.Contains(clean, "de hoje") ||
		strings.Contains(clean, "o que tenho hoje") || strings.Contains(clean, "agenda de hoje") ||
		strings.Contains(clean, "compromissos de hoje") {
		if !strings.Contains(clean, "marcar") && !strings.Contains(clean, "agendar") && !strings.Contains(clean, "lembre-me") && !strings.Contains(clean, "anotar") {
			return "/hoje", true
		}
	}
	// Consulta direta de agenda / compromissos sem especificar outra coisa: "agenda", "minha agenda", "compromissos", "ver agenda", "listar tarefas"
	if synonyms.MatchCanonical(tokens, "agenda") && (len(tokens) <= 3 || synonyms.MatchCanonical(tokens, "ver")) {
		if !strings.Contains(clean, "marcar") && !strings.Contains(clean, "agendar") && !strings.Contains(clean, "adicionar") && !strings.Contains(clean, "nova") {
			return "/hoje", true
		}
	}

	// 6. Perfil / Memória Permanente
	if strings.Contains(clean, "perfil") || strings.Contains(clean, "minha memoria") || strings.Contains(clean, "minhas memorias") ||
		strings.Contains(clean, "o que sabe sobre mim") || strings.Contains(clean, "o que voce sabe") || clean == "quem sou eu" {
		return "/perfil", true
	}

	// 7. Metas / Objetivos
	// Captura: "metas", "minhas metas", "ver metas", "listar objetivos", "consultar alvos"
	if synonyms.MatchCanonical(tokens, "meta") && (clean == "metas" || clean == "objetivos" || synonyms.MatchCanonical(tokens, "ver") || len(tokens) <= 3) {
		if !strings.Contains(clean, "criar") && !strings.Contains(clean, "nova") && !strings.Contains(clean, "definir") {
			return "/metas", true
		}
	}

	// 8. Ideias / Projetos (quando NÃO for anotação de nova ideia)
	// Captura: "ideias", "minhas ideias", "ver projetos", "listar ideias"
	if synonyms.MatchCanonical(tokens, "ideia") && (clean == "ideias" || clean == "projetos" || synonyms.MatchCanonical(tokens, "ver") || len(tokens) <= 3) {
		if !strings.Contains(clean, "anotar") && !strings.Contains(clean, "nova") && !strings.Contains(clean, "tive") {
			return "/ideias", true
		}
	}

	// 9. Lembretes / Rotinas (quando NÃO for criação)
	// Captura: "lembretes", "minhas rotinas", "ver rotinas", "listar avisos", "mostrar alarmes"
	if synonyms.MatchCanonical(tokens, "lembrete") && (clean == "lembretes" || clean == "rotinas" || synonyms.MatchCanonical(tokens, "ver") || len(tokens) <= 3) {
		if !strings.Contains(clean, "criar") && !strings.Contains(clean, "novo") && !strings.Contains(clean, "lembrar") {
			return "/lembretes", true
		}
	}

	// 10. Tokens / Telemetria
	if strings.Contains(clean, "token") || strings.Contains(clean, "consumo") || strings.Contains(clean, "gasto de tokens") {
		return "/tokens", true
	}

	// 11. HUD / Painel
	if clean == "hud" || clean == "painel" || strings.Contains(clean, "abrir hud") || strings.Contains(clean, "abrir painel") {
		return "/hud", true
	}

	// 12. Ajuda / Menu
	if clean == "ajuda" || clean == "help" || clean == "menu" || clean == "como usar" || clean == "comandos" {
		return "/ajuda", true
	}

	// 13. Backup
	if clean == "backup" || strings.Contains(clean, "fazer backup") {
		return "/backup", true
	}

	return "", false
}
