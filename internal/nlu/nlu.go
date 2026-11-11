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

	"atlas/internal/database"
)

type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

type IntentResult struct {
	Tipo                      string `json:"tipo"`
	Titulo                    string `json:"titulo,omitempty"`
	Conteudo                  string `json:"conteudo,omitempty"`
	PerguntaSocratica         string `json:"pergunta_socratica,omitempty"`
	Descricao                 string `json:"descricao,omitempty"`
	HorarioOficial            string `json:"horario_oficial,omitempty"`
	EstimativaDeslocamentoMin int    `json:"estimativa_deslocamento_min,omitempty"`
	EstimativaPreparoMin      int    `json:"estimativa_preparo_min,omitempty"`
	Prazo                     string `json:"prazo,omitempty"`
	Resposta                  string `json:"resposta,omitempty"`
	Acao                      string `json:"acao,omitempty"`
	Horario                   string `json:"horario,omitempty"`
	Nome                      string `json:"nome,omitempty"`
	Mensagem                  string `json:"mensagem,omitempty"`
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

func NewClient(apiKey, model string) *Client {
	if model == "" {
		model = "meta/llama-3.2-90b-vision-instruct"
	}
	return &Client{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 25 * time.Second,
		},
	}
}

var jsonBlockRegex = regexp.MustCompile(`(?s)\{.*\}`)

// ParseIntent sends the user's message along with sliding history to NVIDIA NIM.
func (c *Client) ParseIntent(ctx context.Context, text, currentDatetime string, history []database.ChatMessage) (*IntentResult, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("NVIDIA_API_KEY is not configured")
	}

	systemPrompt := fmt.Sprintf(`Você é o ATLAS: assistente pessoal executivo, socrático, combativo, direto e hiper-racional.
Horário atual de referência: %s.

PERSONALIDADE:
- Nunca seja passivo, subserviente ou dê respostas evasivas.
- Aplique o Método Socrático: confronte premissas frágeis, questione o custo de oportunidade, identifique autoengano e exija clareza de execução.
- Toda ideia nova deve ser desafiada com uma pergunta afiada sobre viabilidade prática e relevância real.
- Extração de Texto: se o usuário encaminhar um e-mail, mensagem de terceiro ou texto com datas/horários, extraia o compromisso principal para a agenda. Se for um artigo ou link, faça um resumo executivo direto e sem rodeios.

Classifique a intenção estritamente em uma das opções abaixo e responda EXCLUSIVAMENTE em formato JSON puro, sem blocos de código ou texto antes/depois:

1. Para ideias, pensamentos ou insights:
{
  "tipo": "ideia",
  "titulo": "Resumo de 3 a 5 palavras",
  "conteudo": "A ideia expressa",
  "pergunta_socratica": "Pergunta afiada confrontando a lógica, premissas ou viabilidade"
}

2. Para compromissos / eventos na agenda:
{
  "tipo": "evento",
  "descricao": "Nome do compromisso",
  "horario_oficial": "YYYY-MM-DD HH:MM:SS",
  "estimativa_deslocamento_min": 25,
  "estimativa_preparo_min": 35
}

3. Para metas e objetivos:
{
  "tipo": "objetivo",
  "titulo": "Meta declarada",
  "prazo": "YYYY-MM-DD HH:MM:SS" ou ""
}

4. Para lembretes diários recorrentes ou programação de rotinas (ex: "todo dia às 09:00 mande uma mensagem dizendo...", "me lembre todo dia às 20:00 de..."):
{
  "tipo": "lembrete_diario",
  "horario": "HH:MM",
  "mensagem": "Mensagem exata a ser disparada todo dia"
}

5. Para conversa, reflexão ou debate geral:
{
  "tipo": "conversa",
  "resposta": "Sua resposta socrática, incisiva, densa e concisa"
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
		Model:       c.model,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   450,
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
			Resposta: strings.TrimSpace(rawResp),
		}, nil
	}

	var res IntentResult
	if err := json.Unmarshal([]byte(match), &res); err != nil {
		return &IntentResult{
			Tipo:     "conversa",
			Resposta: strings.TrimSpace(rawResp),
		}, nil
	}

	return &res, nil
}

// GenerateBriefing generates a proactive morning briefing based on today's events and goals.
func (c *Client) GenerateBriefing(ctx context.Context, todayStr string, events []database.Evento, goals []database.Objetivo) (string, error) {
	prompt := fmt.Sprintf(`Você é o ATLAS. Gere um briefing matinal conciso, hiper-focado e direto para o dia %s.
Compromissos agendados para hoje: %d
Objetivos ativos em andamento: %d

Diretrizes:
- Exija foco implacável nas prioridades.
- Destaque os horários oficiais e alerte para não negligenciar os preparos.
- Faça 1 pergunta socrática provocativa para guiar o dia.
- Máximo 3 parágrafos curtos. Sem introduções vazias.`, todayStr, len(events), len(goals))

	reqPayload := ChatCompletionRequest{
		Model: c.model,
		Messages: []ChatMessagePayload{
			{Role: "system", Content: "Você é o ATLAS: assistente executivo e socrático estilo JARVIS."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.4,
		MaxTokens:   350,
	}

	return c.doRequest(ctx, reqPayload)
}

// GenerateReview generates an incisive evening review confronting the user's progress.
func (c *Client) GenerateReview(ctx context.Context, todayStr string, events []database.Evento, goals []database.Objetivo) (string, error) {
	prompt := fmt.Sprintf(`Você é o ATLAS. É o fechamento do dia %s.
Gere uma mensagem incisiva de auditoria do dia:
- Pergunte se os compromissos do dia foram honrados com excelência ou se houve desculpas.
- Confronte o progresso nas metas de longo prazo.
- Termine com um questionamento socrático sobre como evitar autoengano amanhã.
- Máximo 3 parágrafos curtos, tom combativo e racional.`, todayStr)

	reqPayload := ChatCompletionRequest{
		Model: c.model,
		Messages: []ChatMessagePayload{
			{Role: "system", Content: "Você é o ATLAS: assistente executivo e socrático estilo JARVIS."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.4,
		MaxTokens:   350,
	}

	return c.doRequest(ctx, reqPayload)
}

func (c *Client) doRequest(ctx context.Context, payload ChatCompletionRequest) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := "https://integrate.api.nvidia.com/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("do nvidia req: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("nvidia nim status %d: %s", resp.StatusCode, string(respBody))
	}

	var res ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("decode nvidia response: %w", err)
	}

	if len(res.Choices) == 0 {
		return "", fmt.Errorf("nvidia returned 0 choices")
	}

	return strings.TrimSpace(res.Choices[0].Message.Content), nil
}
