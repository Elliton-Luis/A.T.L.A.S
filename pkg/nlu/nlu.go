package nlu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"atlas/pkg/database"
	"atlas/pkg/pool"
)

type Client struct {
	groqPool          *pool.KeyPool
	groqModel         string
	nvidiaPool        *pool.KeyPool
	nvidiaModel       string
	maxTokensQuick    int
	maxTokensHeavy    int
	maxTokensResearch int
	httpClient        *http.Client
}

type IntentResult struct {
	Tipo                      string `json:"tipo"`           // acao, evento, ideia, adicionar_ideia, lembrar_fato, esquecer_fato, objetivo, lembrete_diario, conversa
	Acao                      string `json:"acao,omitempty"` // apagar_rotina, alterar_rotina, toggle_rotina, concluir_evento, apagar_evento, atualizar_meta, fazer_backup, listar_agenda, listar_lembretes
	ID                        int64  `json:"id,omitempty"`
	Termo                     string `json:"termo,omitempty"`
	Progresso                 int    `json:"progresso,omitempty"`
	Complexidade              string `json:"complexidade,omitempty"` // "simples" ou "pesada"
	Titulo                    string `json:"titulo,omitempty"`
	Conteudo                  string `json:"conteudo,omitempty"`
	SumaIdeia                 string `json:"suma_ideia,omitempty"`
	PerguntaSocratica         string `json:"pergunta_socratica,omitempty"`
	ChaveMemoria              string `json:"chave_memoria,omitempty"`
	ValorMemoria              string `json:"valor_memoria,omitempty"`
	Descricao                 string `json:"descricao,omitempty"`
	Frequencia                string `json:"frequencia,omitempty"` // "pontual", "diario", "semanal", "mensal"
	DiaSemana                 *int   `json:"dia_semana,omitempty"` // 0=Domingo, 1=Segunda, ..., 6=Sábado
	DiasSemana                []int  `json:"dias_semana,omitempty"` // Múltiplos dias da semana (0=Dom a 6=Sáb) para semanas com exceções
	DiaMes                    *int   `json:"dia_mes,omitempty"`    // 1 a 31
	HorarioOficial            string `json:"horario_oficial,omitempty"`
	EstimativaDeslocamentoMin int    `json:"estimativa_deslocamento_min,omitempty"`
	EstimativaPreparoMin      int    `json:"estimativa_preparo_min,omitempty"`
	Prazo                     string `json:"prazo,omitempty"`
	Resposta                  string `json:"resposta,omitempty"`
	Horario                   string `json:"horario,omitempty"`
	Nome                      string `json:"nome,omitempty"`
	Mensagem                  string `json:"mensagem,omitempty"`
	URL                       string `json:"url,omitempty"`
	SubstituirEventoID        int64  `json:"substituir_evento_id,omitempty"`

	// Token usage telemetry
	PromptTokens     int `json:"-"`
	CompletionTokens int `json:"-"`
	TotalTokens      int `json:"-"`
	ProvedorUsado    string `json:"-"`
	ModeloUsado      string `json:"-"`
}

var Curiosidades = []string{
	"Você sabia que os flamingos nascem cinzas e só ficam cor-de-rosa porque se alimentam de algas e camarões ricos em carotenoides?",
	"Você sabia que o mel puro é o único alimento da Terra que nunca estraga? Potes de 3.000 anos achados no Egito continuam perfeitamente comestíveis.",
	"Você sabia que os polvos possuem três corações, sangue azul e que dois terços dos seus neurônios ficam espalhados nos próprios tentáculos?",
	"Você sabia que as lontras-marinhas dormem de mãos dadas na água para que a correnteza não as separe durante o sono?",
	"Você sabia que a cada dois minutos tiramos mais fotos no mundo do que toda a humanidade tirou ao longo de todo o século XIX?",
	"Você sabia que o tubarão-da-groenlândia pode viver mais de 400 anos, o que significa que alguns vivos hoje nasceram antes da Revolução Industrial?",
	"Você sabia que as árvores compartilham nutrientes e se avisam sobre pragas no subsolo usando uma rede de fungos conhecida como 'Wood Wide Web'?",
}

func RandomCuriosity() string {
	idx := time.Now().UnixNano() % int64(len(Curiosidades))
	if idx < 0 {
		idx = -idx
	}
	return Curiosidades[idx]
}

type ChatMessagePayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionRequest struct {
	Model       string               `json:"model"`
	Messages    []ChatMessagePayload `json:"messages"`
	Temperature float64              `json:"temperature"`
	MaxTokens   int                  `json:"max_tokens"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type APIResponse struct {
	Text         string
	Provider     string
	Model        string
	PromptTokens int
	ComplTokens  int
	TotalTokens  int
}

func NewClient(groqKeys, nvidiaKeys, nvidiaModel string, maxTokensQuick, maxTokensHeavy, maxTokensResearch int) *Client {
	if nvidiaModel == "" {
		nvidiaModel = "meta/llama-3.2-90b-vision-instruct"
	}
	if maxTokensQuick <= 0 {
		maxTokensQuick = 450
	}
	if maxTokensHeavy <= 0 {
		maxTokensHeavy = 2048
	}
	if maxTokensResearch <= 0 {
		maxTokensResearch = 4096
	}
	return &Client{
		groqPool:          pool.New(groqKeys),
		groqModel:         "qwen/qwen3.8-27b",
		nvidiaPool:        pool.New(nvidiaKeys),
		nvidiaModel:       nvidiaModel,
		maxTokensQuick:    maxTokensQuick,
		maxTokensHeavy:    maxTokensHeavy,
		maxTokensResearch: maxTokensResearch,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

var (
	jsonBlockRegex = regexp.MustCompile(`(?s)\{.*\}`)
	respostaRegex  = regexp.MustCompile(`(?s)"resposta"\s*:\s*"((?:[^"\\]|\\.)*)`)
)

// SanitizeHumanResponse strips JSON wrappers, escaped quotes and backslashes, ensuring clean human text.
func SanitizeHumanResponse(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// If contains JSON artifacts like "resposta": "...", extract the inner message
	if strings.Contains(s, `"resposta"`) {
		if m := respostaRegex.FindStringSubmatch(s); len(m) > 1 {
			s = m[1]
		}
	}

	// Unescape JSON escape sequences to real formatting
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\t`, "  ")

	// If string still starts with JSON curly brace, strip lines that look like JSON keys
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		var cleanLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") || trimmed == "{" || trimmed == "}" ||
				strings.HasPrefix(trimmed, `"tipo":`) || strings.HasPrefix(trimmed, `"complexidade":`) ||
				strings.HasPrefix(trimmed, `"resposta":`) {
				continue
			}
			cleanLines = append(cleanLines, line)
		}
		s = strings.Join(cleanLines, "\n")
	}

	return strings.TrimSpace(s)
}

// buildCleanHistory ensures an alternating user/assistant history without duplicates or orphan backlogs.
func buildCleanHistory(history []database.ChatMessage, currentInput string) []ChatMessagePayload {
	var clean []ChatMessagePayload
	for _, h := range history {
		if strings.TrimSpace(h.Content) == "" {
			continue
		}
		// Prevent consecutive turns of the same role (deduplicate)
		if len(clean) > 0 && clean[len(clean)-1].Role == h.Role {
			clean[len(clean)-1].Content = h.Content
			continue
		}
		clean = append(clean, ChatMessagePayload{
			Role:    h.Role,
			Content: h.Content,
		})
	}

	// Remove trailing user turn from history to prevent duplication when currentInput is added
	if len(clean) > 0 && clean[len(clean)-1].Role == "user" {
		clean = clean[:len(clean)-1]
	}

	if currentInput != "" {
		clean = append(clean, ChatMessagePayload{
			Role:    "user",
			Content: currentInput,
		})
	}

	return clean
}

// ParseIntent sends the user's message along with sliding history, active events and long-term memory to NLU.
func (c *Client) ParseIntent(ctx context.Context, text, currentDatetime string, history []database.ChatMessage, userMemories []database.MemoriaUsuario, latestIdeia *database.Ideia, activeEvents []database.Evento) (*IntentResult, error) {
	if c.groqPool.IsEmpty() && c.nvidiaPool.IsEmpty() {
		return nil, fmt.Errorf("nenhuma chave de IA (Groq ou NVIDIA) configurada")
	}

	var memSb strings.Builder
	if len(userMemories) > 0 {
		memSb.WriteString("\n\nPERFIL E MEMÓRIAS PERMANENTES DO USUÁRIO (USE SEMPRE ESTE CONTEXTO PARA PERSONALIZAR):\n")
		for _, m := range userMemories {
			memSb.WriteString(fmt.Sprintf("- %s: %s\n", m.Chave, m.Valor))
		}
	}

	var ideiaSb strings.Builder
	if latestIdeia != nil {
		ideiaSb.WriteString(fmt.Sprintf("\n\nÚLTIMA IDEIA EM DESENVOLVIMENTO (ID %d - '%s'):\nSuma atual:\n%s\nSe o usuário estiver trazendo mais argumentos, detalhes ou complementos a essa ideia, use 'tipo': 'adicionar_ideia'.\n", latestIdeia.ID, latestIdeia.Titulo, latestIdeia.SumaExecutiva))
	}

	var agendaSb strings.Builder
	if len(activeEvents) > 0 {
		agendaSb.WriteString("\n\nCOMPROMISSOS ATIVOS NA AGENDA DO USUÁRIO:\n")
		for _, ev := range activeEvents {
			timeStr := ev.HorarioOficial
			if ev.HorarioTime != "" {
				timeStr = ev.HorarioTime
			}
			agendaSb.WriteString(fmt.Sprintf("- ID %d: \"%s\" às %s (frequência: %s)\n", ev.ID, ev.Descricao, timeStr, ev.Frequencia))
		}
		agendaSb.WriteString("Se o usuário estiver corrigindo, ajustando, refazendo ou alterando o horário de um compromisso acima (ou disser que errou), defina 'substituir_evento_id' com o ID correspondente.\n")
	}

	systemPrompt := fmt.Sprintf(`Você é o ATLAS: assistente pessoal executivo de alto nível, inspirado no JARVIS.
Horário atual de referência: %s.%s%s%s

SEUS ALGORITMOS E CAPACIDADES INTERNAS (REAIS):
1. Algoritmo Anti-Atraso (Margem Determinística de +15%%):
   - Aplica matematicamente: Tempo_Real = t * 1.15 para deslocamento e preparo.
   - Buffer 2 (Saída): Horário Oficial menos tempo de deslocamento com margem.
   - Buffer 1 (Preparo): Buffer 2 menos tempo de preparo com margem.
2. Motor de Proatividade em Goroutines Concorrentes (Go Ticker de 30s):
   - Notificações automáticas nos minutos exatos dos Buffers de saída e preparo.
   - Disparo pontual de rotinas diárias e lembretes programados.
3. Auditoria Pós-Evento:
   - 15 minutos após o encerramento previsto de um compromisso, pergunta educadamente se foi concluído.
4. Sincronização de Calendário Universal (RFC 5545 iCal):
   - Gera feeds .ics seguros com alarmes sonoros embutidos (VALARM) para Google Calendar, Apple e Android.
5. Painel HUD Visual Integrado (Telegram WebApp):
   - Interface terminal escura para controle de rotinas, metas e agenda.
6. Memória Permanente e Perfil:
   - Armazena fatos, profissão, rotinas e preferências do usuário para nunca esquecer quem ele é.
7. Gestão de Ideias Contínua com Suma Acumulada:
   - Toda vez que o usuário traz uma ideia ou adiciona pensamentos, concatena o resumo fiel e mantém uma Suma Executiva estruturada sempre atualizada.
8. Radar de E-mails Somente-Leitura (IMAP Seguro):
   - Conexão de leitura na caixa de entrada para triagem de e-mails importantes, filtrando spams e destacando o que exige ação.

PERSONALIDADE (ESTILO JARVIS):
- Calmo, sofisticado, sóbrio, educado, altamente competente e respeitoso.
- JAMAIS seja grosseiro, arrogante, sarcástico ou combativo.
- Em ideias, planos ou reflexões (MÉTODO SOCRÁTICO): atue como um conselheiro estratégico experiente. Valide a ideia, gere a suma executiva e levante com polidez um ponto cego real ou custo de oportunidade com uma pergunta inteligente.

Classifique a intenção estritamente em uma das opções abaixo e responda EXCLUSIVAMENTE em formato JSON puro:

1. Para NOVA ideia, pensamento ou projeto (ou se disser "anotar uma ideia", "tive uma ideia"):
{
  "tipo": "ideia",
  "complexidade": "simples" ou "pesada",
  "titulo": "Título curto da ideia",
  "conteudo": "Resumo fiel e exato do que o usuário disse",
  "suma_ideia": "Suma executiva estruturada e clara da ideia em tópicos",
  "pergunta_socratica": "Pergunta reflexiva apontando ponto cego ou risco estratégico"
}

2. Para CONTINUAÇÃO ou COMPLEMENTO de uma ideia já discutida (ex: "adicione à ideia", "mais um ponto", "anote também que..."):
{
  "tipo": "adicionar_ideia",
  "id": 0,
  "conteudo": "Resumo exato do novo ponto adicionado",
  "suma_ideia": "Nova suma executiva acumulada integrando todos os pontos anteriores com o novo ponto",
  "pergunta_socratica": "Novo questionamento estratégico sobre o plano atualizado"
}

3. Para GUARDAR UM FATO OU INFORMAÇÃO SOBRE O USUÁRIO (ex: "sou programador", "acordo às 6h", "minha meta é faturar 10k", "não como carne", "lembre-se que..."):
{
  "tipo": "lembrar_fato",
  "chave_memoria": "palavra_chave_curta (ex: profissao, rotina_sono, alimentacao, cidade, meta_financeira)",
  "valor_memoria": "O fato ou informação exata a ser lembrada",
  "resposta": "Confirmação respeitosa de que o fato foi gravado em sua memória permanente"
}

4. Para ESQUECER UM FATO gravado:
{
  "tipo": "esquecer_fato",
  "chave_memoria": "palavra_chave_a_remover"
}

5. Para compromissos / eventos na agenda (PONTUAIS ou RECORRENTES):
Aceite variações naturais como: "marque", "anote", "agende", "crie um compromisso", "coloque na minha agenda", "tenho X amanhã às Y", "anotar compromisso toda semana exceto...":
{
  "tipo": "evento",
  "complexidade": "simples",
  "descricao": "Nome do compromisso",
  "frequencia": "pontual" ou "diario" ou "semanal" ou "mensal",
  "horario": "HH:MM",
  "horario_oficial": "YYYY-MM-DD HH:MM:SS" (obrigatório se frequencia for "pontual"),
  "dia_semana": 0 a 6 (se for apenas um dia da semana, 0=Dom, 1=Seg, 2=Ter, 3=Qua, 4=Qui, 5=Sex, 6=Sáb),
  "dias_semana": [1, 2, 3] (obrigatório quando o usuário indicar múltiplos dias ou exceções na semana, ex: "segunda a sexta" -> [1,2,3,4,5], "exceto domingo e segunda" -> [2,3,4,5,6], "terça e quinta" -> [2,4]),
  "dia_mes": 1 a 31 (use apenas se frequencia for "mensal"),
  "estimativa_deslocamento_min": 25,
  "estimativa_preparo_min": 35,
  "substituir_evento_id": 0
}

6. Para metas e objetivos:
{
  "tipo": "objetivo",
  "complexidade": "simples",
  "titulo": "Meta declarada",
  "prazo": "YYYY-MM-DD HH:MM:SS" ou ""
}

7. Para lembretes diários recorrentes (ex: "todo dia às 09:00 mande uma mensagem...", "crie um lembrete..."):
{
  "tipo": "lembrete_diario",
  "complexidade": "simples",
  "horario": "HH:MM",
  "mensagem": "Mensagem exata a ser disparada todo dia"
}

8. Para ações do app, buscas e exclusões ("delete o dentista", "apague a reunião", "delete isso", "cancele o último", "apagar tudo", "limpar tudo"):
{
  "tipo": "acao",
  "acao": "ler_emails" (para checar a caixa de entrada, ler e-mails ou resumir o que há de interessante), "apagar_evento" (para apagar 1 evento por nome ou "isso"), "apagar_rotina" (para apagar 1 rotina/lembrete), "limpar_agenda" (apagar todos os eventos), "limpar_lembretes" (apagar todos os lembretes), "limpar_tudo" (apagar agenda E lembretes), "liturgia", "noticias", "resumir_link", "alterar_rotina", "toggle_rotina", "concluir_evento", "atualizar_meta", "fazer_backup", "listar_agenda", "ver_calendario", "listar_lembretes", "ver_perfil",
  "id": 0,
  "termo": "palavra-chave" (ex: "dentista", "reuniao", ou "isso" se for para apagar o último criado),
  "url": "https://...",
  "horario": "HH:MM",
  "progresso": 80
}

9. Para conversa, reflexão, debate ou pesquisa aprofundada:
{
  "tipo": "conversa" ou "pesquisa",
  "complexidade": "simples" ou "pesada" ou "pesquisa" (use "pesquisa" quando for solicitação de estudo aprofundado, pesquisa extensa, artigos, dossiê ou resumo detalhado),
  "resposta": "Sua resposta equilibrada, polida e perspicaz (apenas se complexidade for simples)"
}
`, currentDatetime, memSb.String(), ideiaSb.String(), agendaSb.String())

	messages := []ChatMessagePayload{
		{Role: "system", Content: systemPrompt},
	}
	messages = append(messages, buildCleanHistory(history, text)...)

	reqPayload := ChatCompletionRequest{
		Model:       c.groqModel,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   c.maxTokensQuick,
	}

	apiRes, err := c.doRequest(ctx, reqPayload)
	if err != nil {
		return nil, err
	}

	rawResp := apiRes.Text
	match := jsonBlockRegex.FindString(rawResp)
	if match == "" {
		return &IntentResult{
			Tipo:             "conversa",
			Resposta:         SanitizeHumanResponse(rawResp),
			PromptTokens:     apiRes.PromptTokens,
			CompletionTokens: apiRes.ComplTokens,
			TotalTokens:      apiRes.TotalTokens,
			ProvedorUsado:    apiRes.Provider,
			ModeloUsado:      apiRes.Model,
		}, nil
	}

	var res IntentResult
	if err := json.Unmarshal([]byte(match), &res); err != nil {
		return &IntentResult{
			Tipo:             "conversa",
			Resposta:         SanitizeHumanResponse(rawResp),
			PromptTokens:     apiRes.PromptTokens,
			CompletionTokens: apiRes.ComplTokens,
			TotalTokens:      apiRes.TotalTokens,
			ProvedorUsado:    apiRes.Provider,
			ModeloUsado:      apiRes.Model,
		}, nil
	}

	res.Resposta = SanitizeHumanResponse(res.Resposta)
	res.PromptTokens = apiRes.PromptTokens
	res.CompletionTokens = apiRes.ComplTokens
	res.TotalTokens = apiRes.TotalTokens
	res.ProvedorUsado = apiRes.Provider
	res.ModeloUsado = apiRes.Model
	return &res, nil
}

// GenerateBriefing generates a proactive morning briefing based on today's events and goals.
func (c *Client) GenerateBriefing(ctx context.Context, todayStr string, events []database.Evento, goals []database.Objetivo) (string, error) {
	prompt := fmt.Sprintf(`Você é o ATLAS, assistente executivo estilo JARVIS. Apresente um briefing matinal refinado, claro e direto para o dia %s.
Compromissos agendados: %d
Metas ativas: %d

Diretrizes:
- Tom sereno, polido, focado e profissional.
- Destaque os compromissos principais e horários de saída com calma.
- Deixe 1 reflexão estratégica para guiar a atenção do dia.
- Máximo 2 parágrafos.`, todayStr, len(events), len(goals))

	reqPayload := ChatCompletionRequest{
		Model: c.groqModel,
		Messages: []ChatMessagePayload{
			{Role: "system", Content: "Você é o ATLAS: assistente executivo refinado e inteligente estilo JARVIS."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
		MaxTokens:   350,
	}

	res, err := c.doRequest(ctx, reqPayload)
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

// GenerateReview generates a calm, insightful evening review.
func (c *Client) GenerateReview(ctx context.Context, todayStr string, events []database.Evento, goals []database.Objetivo) (string, error) {
	prompt := fmt.Sprintf(`Você é o ATLAS, assistente executivo estilo JARVIS. É o fechamento do dia %s.
Gere um balanço do dia:
- Tom elegante, respeitoso, sóbrio e reflexivo.
- Faça um breve resumo do dia e sugira uma reflexão madura para descanso e planejamento de amanhã.
- Máximo 2 parágrafos. Sem broncas ou agressividade.`, todayStr)

	reqPayload := ChatCompletionRequest{
		Model: c.groqModel,
		Messages: []ChatMessagePayload{
			{Role: "system", Content: "Você é o ATLAS: assistente executivo refinado e inteligente estilo JARVIS."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
		MaxTokens:   350,
	}

	res, err := c.doRequest(ctx, reqPayload)
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

// DeepAnalyze delegates heavy research, complex analysis or strategic evaluations to NVIDIA NIM.
func (c *Client) DeepAnalyze(ctx context.Context, text string, history []database.ChatMessage) (*APIResponse, error) {
	systemPrompt := `Você é o ATLAS: assistente executivo e estrategista de alto nível estilo JARVIS.
Você foi acionado para uma análise aprofundada, pesquisa ou raciocínio complexo.
Responda com extrema sofisticação, profundidade analítica, clareza cirúrgica e elegância, apontando dados, riscos, custos de oportunidade e passos recomendados.`

	messages := []ChatMessagePayload{
		{Role: "system", Content: systemPrompt},
	}
	messages = append(messages, buildCleanHistory(history, text)...)

	payload := ChatCompletionRequest{
		Model:       c.nvidiaModel,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   c.maxTokensHeavy,
	}

	// 1. Tenta NVIDIA NIM com rotação e fallback no pool de chaves
	if !c.nvidiaPool.IsEmpty() {
		res, err := c.executePoolAPI(ctx, "https://integrate.api.nvidia.com/v1/chat/completions", c.nvidiaPool, payload)
		if err == nil && res != nil {
			return res, nil
		}
	}

	// 2. Fallback para Groq se todas as chaves NVIDIA falharem
	if !c.groqPool.IsEmpty() {
		payload.Model = c.groqModel
		return c.executePoolAPI(ctx, "https://api.groq.com/openai/v1/chat/completions", c.groqPool, payload)
	}

	return nil, fmt.Errorf("nenhum provedor de IA disponível para análise profunda")
}

// ResearchAnalyze executes unconstrained deep research with expanded output tokens.
func (c *Client) ResearchAnalyze(ctx context.Context, text string, history []database.ChatMessage) (*APIResponse, error) {
	systemPrompt := `Você é o ATLAS no MODO PESQUISA & SÍNTESE APROFUNDADA (JARVIS Research Core).
Você tem autorização para produzir respostas completas, extensas, densas e minuciosas, sem cortes ou truncamentos prematuros.
Diretrizes do Perfil de Pesquisa:
1. Traga conceitos fundamentais, contexto histórico/técnico e o estado da arte.
2. Apresente múltiplos pontos de vista, referências, artigos e escolas de pensamento relevantes.
3. Estruture em tópicos claros, com introdução, desenvolvimento analítico, exemplos práticos e conclusões executivas.
4. Jamais economize explicações essenciais: priorize riqueza conceitual, precisão e clareza analítica.`

	messages := []ChatMessagePayload{
		{Role: "system", Content: systemPrompt},
	}
	messages = append(messages, buildCleanHistory(history, text)...)

	payload := ChatCompletionRequest{
		Model:       c.nvidiaModel,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   c.maxTokensResearch,
	}

	// 1. Tenta NVIDIA NIM
	if !c.nvidiaPool.IsEmpty() {
		res, err := c.executePoolAPI(ctx, "https://integrate.api.nvidia.com/v1/chat/completions", c.nvidiaPool, payload)
		if err == nil && res != nil {
			return res, nil
		}
	}

	// 2. Fallback para Groq
	if !c.groqPool.IsEmpty() {
		payload.Model = c.groqModel
		return c.executePoolAPI(ctx, "https://api.groq.com/openai/v1/chat/completions", c.groqPool, payload)
	}

	return nil, fmt.Errorf("nenhum provedor de IA disponível para pesquisa profunda")
}

func (c *Client) doRequest(ctx context.Context, payload ChatCompletionRequest) (*APIResponse, error) {
	// 1. Tenta Groq primeiro (velocidade relâmpago de ~0.3s) com pool de chaves
	if !c.groqPool.IsEmpty() {
		groqPayload := payload
		groqPayload.Model = c.groqModel
		res, err := c.executePoolAPI(ctx, "https://api.groq.com/openai/v1/chat/completions", c.groqPool, groqPayload)
		if err == nil && res != nil {
			return res, nil
		}
	}

	// 2. Fallback para NVIDIA NIM com pool de chaves caso Groq oscile
	if !c.nvidiaPool.IsEmpty() {
		nvidiaPayload := payload
		nvidiaPayload.Model = c.nvidiaModel
		return c.executePoolAPI(ctx, "https://integrate.api.nvidia.com/v1/chat/completions", c.nvidiaPool, nvidiaPayload)
	}

	return nil, fmt.Errorf("nenhum provedor de IA (Groq ou NVIDIA) configurado com sucesso")
}

func (c *Client) executePoolAPI(ctx context.Context, url string, keyPool *pool.KeyPool, payload ChatCompletionRequest) (*APIResponse, error) {
	if keyPool.IsEmpty() {
		return nil, fmt.Errorf("nenhuma chave disponível para %s", url)
	}

	keys := keyPool.OrderedKeys()
	var lastErr error

	for _, key := range keys {
		res, err := c.executeAPI(ctx, url, key, payload)
		if err == nil && res != nil {
			return res, nil
		}
		lastErr = err
	}

	return nil, lastErr
}

func (c *Client) executeAPI(ctx context.Context, url, apiKey string, payload ChatCompletionRequest) (*APIResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "AtlasBot/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do req to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api status %d: %s", resp.StatusCode, string(respBody))
	}

	var res ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(res.Choices) == 0 {
		return nil, fmt.Errorf("api returned 0 choices")
	}

	provider := "groq"
	if strings.Contains(url, "nvidia") {
		provider = "nvidia"
	}

	return &APIResponse{
		Text:         strings.TrimSpace(res.Choices[0].Message.Content),
		Provider:     provider,
		Model:        payload.Model,
		PromptTokens: res.Usage.PromptTokens,
		ComplTokens:  res.Usage.CompletionTokens,
		TotalTokens:  res.Usage.TotalTokens,
	}, nil
}
