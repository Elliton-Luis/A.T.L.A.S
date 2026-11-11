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
	groqKey     string
	groqModel   string
	nvidiaKey   string
	nvidiaModel string
	httpClient  *http.Client
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

func NewClient(groqKey, nvidiaKey, nvidiaModel string) *Client {
	if nvidiaModel == "" {
		nvidiaModel = "meta/llama-3.2-90b-vision-instruct"
	}
	return &Client{
		groqKey:     groqKey,
		groqModel:   "qwen/qwen3.8-27b",
		nvidiaKey:   nvidiaKey,
		nvidiaModel: nvidiaModel,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

var jsonBlockRegex = regexp.MustCompile(`(?s)\{.*\}`)

// ParseIntent sends the user's message along with sliding history to NLU (Groq LPU ultra-fast / NVIDIA fallback).
func (c *Client) ParseIntent(ctx context.Context, text, currentDatetime string, history []database.ChatMessage) (*IntentResult, error) {
	if c.groqKey == "" && c.nvidiaKey == "" {
		return nil, fmt.Errorf("nenhuma chave de IA (Groq ou NVIDIA) configurada")
	}

	systemPrompt := fmt.Sprintf(`Você é o ATLAS: assistente pessoal executivo de alto nível, inspirado no JARVIS.
Horário atual de referência: %s.

PERSONALIDADE (ESTILO JARVIS):
- Calmo, sofisticado, sóbrio, educado, altamente competente e respeitoso.
- JAMAIS seja grosseiro, arrogante, sarcástico, pedante ou combativo.
- Em tarefas e pedidos operacionais (lembretes, horários, rotinas, tarefas): responda com eficiência, clareza e presteza, sem lições de moral.
- Em ideias, planos ou reflexões (MÉTODO SOCRÁTICO): atue como um conselheiro estratégico experiente. Valide o raciocínio e levante com elegância possíveis pontos cegos, custos de oportunidade, viabilidade prática ou trade-offs com uma pergunta cirúrgica e inteligente, que ajude o usuário a aprimorar o pensamento.
- Extração de Texto: ao receber mensagens encaminhadas ou artigos, sintetize com clareza o cerne da mensagem de forma limpa.

Classifique a intenção estritamente em uma das opções abaixo e responda EXCLUSIVAMENTE em formato JSON puro, sem blocos de código ou texto fora:

1. Para ideias, pensamentos, projetos ou planos:
{
  "tipo": "ideia",
  "titulo": "Título conciso da ideia",
  "conteudo": "A essência da ideia",
  "pergunta_socratica": "Pergunta estratégica e reflexiva que aponta um ponto cego, risco ou aspecto a ser considerado para aperfeiçoar o plano"
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
