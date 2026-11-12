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
)

type Client struct {
	groqKey        string
	groqModel      string
	nvidiaKey      string
	nvidiaModel    string
	maxTokensQuick int
	maxTokensHeavy int
	httpClient     *http.Client
}

type IntentResult struct {
	Tipo                      string `json:"tipo"`           // acao, evento, ideia, objetivo, lembrete_diario, conversa
	Acao                      string `json:"acao,omitempty"` // apagar_rotina, alterar_rotina, toggle_rotina, concluir_evento, apagar_evento, atualizar_meta, fazer_backup, listar_agenda, listar_lembretes
	ID                        int64  `json:"id,omitempty"`
	Termo                     string `json:"termo,omitempty"`
	Progresso                 int    `json:"progresso,omitempty"`
	Complexidade              string `json:"complexidade,omitempty"` // "simples" ou "pesada"
	Titulo                    string `json:"titulo,omitempty"`
	Conteudo                  string `json:"conteudo,omitempty"`
	PerguntaSocratica         string `json:"pergunta_socratica,omitempty"`
	Descricao                 string `json:"descricao,omitempty"`
	HorarioOficial            string `json:"horario_oficial,omitempty"`
	EstimativaDeslocamentoMin int    `json:"estimativa_deslocamento_min,omitempty"`
	EstimativaPreparoMin      int    `json:"estimativa_preparo_min,omitempty"`
	Prazo                     string `json:"prazo,omitempty"`
	Resposta                  string `json:"resposta,omitempty"`
	Horario                   string `json:"horario,omitempty"`
	Nome                      string `json:"nome,omitempty"`
	Mensagem                  string `json:"mensagem,omitempty"`
	URL                       string `json:"url,omitempty"`
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
}

func NewClient(groqKey, nvidiaKey, nvidiaModel string, maxTokensQuick, maxTokensHeavy int) *Client {
	if nvidiaModel == "" {
		nvidiaModel = "meta/llama-3.2-90b-vision-instruct"
	}
	if maxTokensQuick <= 0 {
		maxTokensQuick = 250
	}
	if maxTokensHeavy <= 0 {
		maxTokensHeavy = 800
	}
	return &Client{
		groqKey:        groqKey,
		groqModel:      "qwen/qwen3.8-27b",
		nvidiaKey:      nvidiaKey,
		nvidiaModel:    nvidiaModel,
		maxTokensQuick: maxTokensQuick,
		maxTokensHeavy: maxTokensHeavy,
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

// ParseIntent sends the user's message along with sliding history to NLU (Groq LPU ultra-fast / NVIDIA fallback).
func (c *Client) ParseIntent(ctx context.Context, text, currentDatetime string, history []database.ChatMessage) (*IntentResult, error) {
	if c.groqKey == "" && c.nvidiaKey == "" {
		return nil, fmt.Errorf("nenhuma chave de IA (Groq ou NVIDIA) configurada")
	}

	systemPrompt := fmt.Sprintf(`Você é o ATLAS: assistente pessoal executivo de alto nível, inspirado no JARVIS.
Horário atual de referência: %s.

SEUS ALGORITMOS E CAPACIDADES INTERNAS (REAIS):
1. Algoritmo Anti-Atraso (Margem Determinística de +15%%):
   - Aplica matematicamente: Tempo_Real = t * 1.15 para deslocamento e preparo.
   - Buffer 2 (Saída): Horário Oficial menos tempo de deslocamento com margem.
   - Buffer 1 (Preparo): Buffer 2 menos tempo de preparo com margem.
2. Motor de Proatividade em Goroutines Concorrentes (Go Ticker de 30s):
   - Notificações automáticas nos minutos exatos dos Buffers de saída e preparo.
   - Disparo pontual de rotinas diárias e lembretes programados.
3. Auditoria Pós-Evento:
   - 15 minutos após o encerramento previsto de um compromisso, pergunta educadamente se foi concluído ou se precisa de reagendamento.
4. Sincronização de Calendário Universal (RFC 5545 iCal):
   - Gera feeds .ics seguros com alarmes sonoros embutidos (VALARM) para Google Calendar, Apple e Android.
5. Painel HUD Visual Integrado (Telegram WebApp):
   - Interface terminal escura autenticada com HMAC-SHA256 para controle de rotinas, metas e agenda.
6. Memória Contextual com Janela Deslizante:
   - Histórico contínuo armazenado localmente em SQLite (WAL mode) para diálogo fluido sem amnésia.

PERSONALIDADE (ESTILO JARVIS):
- Calmo, sofisticado, sóbrio, educado, altamente competente e respeitoso.
- JAMAIS seja grosseiro, arrogante, sarcástico, pedante ou combativo. Não use frases agressivas como "a inércia é seu inimigo" nem mande o usuário "parar de perguntar".
- Em dúvidas sobre você ou seus recursos: explique com precisão cirúrgica e elegância os algoritmos e funções que você opera.
- Em tarefas cotidianas (lembretes, horários, rotinas, tarefas): responda com eficiência e clareza.
- Em ideias, planos ou reflexões (MÉTODO SOCRÁTICO): atue como um conselheiro estratégico experiente. Valide a ideia e levante com polidez um ponto cego real, risco operacional ou custo de oportunidade com uma pergunta inteligente para aperfeiçoar o plano.
- Extração de Texto: ao receber mensagens encaminhadas ou artigos, sintetize com clareza o cerne da mensagem de forma limpa.

Classifique a intenção estritamente em uma das opções abaixo e responda EXCLUSIVAMENTE em formato JSON puro.
Indique sempre "complexidade": "pesada" se a solicitação for uma pesquisa aprofundada, análise estratégica complexa ou redação longa de projeto. Para todo o resto, use "complexidade": "simples":

1. Para ideias, pensamentos, projetos ou planos:
{
  "tipo": "ideia",
  "complexidade": "simples" ou "pesada",
  "titulo": "Título conciso da ideia",
  "conteudo": "A essência da ideia",
  "pergunta_socratica": "Pergunta estratégica e reflexiva que aponta um ponto cego, risco ou aspecto a ser considerado para aperfeiçoar o plano"
}

2. Para compromissos / eventos na agenda:
{
  "tipo": "evento",
  "complexidade": "simples",
  "descricao": "Nome do compromisso",
  "horario_oficial": "YYYY-MM-DD HH:MM:SS",
  "estimativa_deslocamento_min": 25,
  "estimativa_preparo_min": 35
}

3. Para metas e objetivos:
{
  "tipo": "objetivo",
  "complexidade": "simples",
  "titulo": "Meta declarada",
  "prazo": "YYYY-MM-DD HH:MM:SS" ou ""
}

4. Para lembretes diários recorrentes ou programação de rotinas (ex: "todo dia às 09:00 mande uma mensagem dizendo...", "me lembre todo dia às 20:00 de..."):
{
  "tipo": "lembrete_diario",
  "complexidade": "simples",
  "horario": "HH:MM",
  "mensagem": "Mensagem exata a ser disparada todo dia"
}

5. Para ações do aplicativo e buscas na internet (apagar/alterar lembretes, concluir eventos, liturgia diária, notícias, resumir links):
{
  "tipo": "acao",
  "acao": "liturgia" ou "noticias" ou "resumir_link" ou "apagar_rotina" ou "alterar_rotina" ou "toggle_rotina" ou "concluir_evento" ou "apagar_evento" ou "atualizar_meta" ou "fazer_backup" ou "listar_agenda" ou "listar_lembretes",
  "id": 0 (ou o número do ID se mencionado),
  "termo": "palavra-chave (ex: tema da notícia 'inteligência artificial', ou da meta/lembrete/evento)",
  "url": "https://..." (se o usuário forneceu um link para ler/resumir),
  "horario": "HH:MM" (se for alterar horário),
  "progresso": 80 (número de 0 a 100 se for atualizar meta)
}

6. Para conversa, reflexão ou debate geral (ou perguntas sobre o que você faz):
{
  "tipo": "conversa",
  "complexidade": "simples" ou "pesada",
  "resposta": "Sua resposta equilibrada, polida, inteligente e perspicaz"
}
`, currentDatetime)

	messages := []ChatMessagePayload{
		{Role: "system", Content: systemPrompt},
	}

	for _, h := range history {
		messages = append(messages, ChatMessagePayload{
			Role:    h.Role,
			Content: h.Content,
		})
	}
	messages = append(messages, ChatMessagePayload{
		Role:    "user",
		Content: text,
	})

	reqPayload := ChatCompletionRequest{
		Model:       c.groqModel,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   c.maxTokensQuick,
	}

	rawResp, err := c.doRequest(ctx, reqPayload)
	if err != nil {
		return nil, err
	}

	match := jsonBlockRegex.FindString(rawResp)
	if match == "" {
		// Fallback as general conversation
		return &IntentResult{
			Tipo:     "conversa",
			Resposta: SanitizeHumanResponse(rawResp),
		}, nil
	}

	var res IntentResult
	if err := json.Unmarshal([]byte(match), &res); err != nil {
		return &IntentResult{
			Tipo:     "conversa",
			Resposta: SanitizeHumanResponse(rawResp),
		}, nil
	}

	res.Resposta = SanitizeHumanResponse(res.Resposta)
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

	return c.doRequest(ctx, reqPayload)
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

	return c.doRequest(ctx, reqPayload)
}

// DeepAnalyze delegates heavy research, complex analysis or strategic evaluations to NVIDIA NIM (90B).
func (c *Client) DeepAnalyze(ctx context.Context, text string, history []database.ChatMessage) (string, error) {
	systemPrompt := `Você é o ATLAS: assistente executivo e estrategista de alto nível estilo JARVIS.
Você foi acionado para uma análise aprofundada, pesquisa ou raciocínio complexo.
Responda com extrema sofisticação, profundidade analítica, clareza cirúrgica e elegância, apontando dados, riscos, custos de oportunidade e passos recomendados.`

	messages := []ChatMessagePayload{
		{Role: "system", Content: systemPrompt},
	}
	for _, h := range history {
		messages = append(messages, ChatMessagePayload{Role: h.Role, Content: h.Content})
	}
	messages = append(messages, ChatMessagePayload{Role: "user", Content: text})

	payload := ChatCompletionRequest{
		Model:       c.nvidiaModel,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   c.maxTokensHeavy,
	}

	return c.executeAPI(ctx, "https://integrate.api.nvidia.com/v1/chat/completions", c.nvidiaKey, payload)
}

func (c *Client) doRequest(ctx context.Context, payload ChatCompletionRequest) (string, error) {
	// 1. Tenta Groq primeiro (velocidade relâmpago de ~0.3s)
	if c.groqKey != "" {
		groqPayload := payload
		groqPayload.Model = c.groqModel
		res, err := c.executeAPI(ctx, "https://api.groq.com/openai/v1/chat/completions", c.groqKey, groqPayload)
		if err == nil && res != "" {
			return res, nil
		}
	}

	// 2. Fallback para NVIDIA NIM caso Groq oscile
	if c.nvidiaKey != "" {
		nvidiaPayload := payload
		nvidiaPayload.Model = c.nvidiaModel
		return c.executeAPI(ctx, "https://integrate.api.nvidia.com/v1/chat/completions", c.nvidiaKey, nvidiaPayload)
	}

	return "", fmt.Errorf("nenhum provedor de IA (Groq ou NVIDIA) configurado com sucesso")
}

func (c *Client) executeAPI(ctx context.Context, url, apiKey string, payload ChatCompletionRequest) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "AtlasBot/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("do req to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("api status %d: %s", resp.StatusCode, string(respBody))
	}

	var res ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(res.Choices) == 0 {
		return "", fmt.Errorf("api returned 0 choices")
	}

	return strings.TrimSpace(res.Choices[0].Message.Content), nil
}
