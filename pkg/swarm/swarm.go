package swarm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
)

// LLMClient is the interface needed by the swarm orchestrator (adheres to Dependency Inversion).
type LLMClient interface {
	DeepAnalyze(ctx context.Context, text string, history []database.ChatMessage) (*nlu.APIResponse, error)
}

// Role defines the specialization of a swarm subagent.
type Role string

const (
	RoleResearcher Role = "Pesquisador (Fatos & Evidências)"
	RoleAnalyst    Role = "Analista (Lógica & Viabilidade)"
	RoleCritic     Role = "Crítico Socrático (Riscos & Suposições)"
)

// AgentOutput contains the specific insight provided by a subagent.
type AgentOutput struct {
	Role        Role
	Perspective string
	Duration    time.Duration
	Success     bool
	Tokens      int
}

// Deliberation is the master synthesized report combining all subagents.
type Deliberation struct {
	Topic       string
	Outputs     []AgentOutput
	Consensus   string
	Duration    time.Duration
	TotalTokens int
}

// FormatTelegramHUD returns a structured, elegant markdown view for Telegram.
func (d *Deliberation) FormatTelegramHUD() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🏛️ *Conselho de Especialistas ATLAS* `[Swarm: 3 Agentes]`\n"))
	sb.WriteString(fmt.Sprintf("📌 *Tema:* _%s_\n", d.Topic))
	sb.WriteString(fmt.Sprintf("⏱️ *Tempo de Deliberação:* %dms | *Tokens:* %d\n\n", d.Duration.Milliseconds(), d.TotalTokens))

	for _, o := range d.Outputs {
		icon := "🔬"
		switch o.Role {
		case RoleAnalyst:
			icon = "⚖️"
		case RoleCritic:
			icon = "⚔️"
		}
		sb.WriteString(fmt.Sprintf("%s *%s:*\n%s\n\n", icon, o.Role, strings.TrimSpace(o.Perspective)))
	}

	if d.Consensus != "" {
		sb.WriteString("────────────────────\n")
		sb.WriteString("🎯 *Síntese & Veredito Executivo:*\n")
		sb.WriteString(strings.TrimSpace(d.Consensus))
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

// Orchestrator coordinates parallel subagent execution and final synthesis.
type Orchestrator struct {
	client LLMClient
}

// NewOrchestrator creates a new multi-agent swarm orchestrator.
func NewOrchestrator(client LLMClient) *Orchestrator {
	return &Orchestrator{
		client: client,
	}
}

// Deliberate triggers concurrent analysis from the 3 specialist agents and synthesizes the final consensus.
func (o *Orchestrator) Deliberate(ctx context.Context, topic string) (*Deliberation, error) {
	startTime := time.Now()

	roles := []struct {
		role   Role
		prompt string
	}{
		{
			role: RoleResearcher,
			prompt: fmt.Sprintf(`Você é o Agente Pesquisador em um conselho de inteligência.
Analise a questão abaixo sob uma ótica estritamente empírica, factual e objetiva. Aponte dados, precedentes e fatos comprovados. Seja conciso (máximo 2 parágrafos).
Tema: %s`, topic),
		},
		{
			role: RoleAnalyst,
			prompt: fmt.Sprintf(`Você é o Agente Analista Lógico em um conselho de inteligência.
Analise a questão abaixo sob uma ótica de viabilidade, métricas, estrutura de custo-benefício e trade-offs pragmáticos. Seja conciso (máximo 2 parágrafos).
Tema: %s`, topic),
		},
		{
			role: RoleCritic,
			prompt: fmt.Sprintf(`Você é o Agente Crítico Socrático em um conselho de inteligência.
Analise a questão abaixo procurando falhas ocultas, premissas ingênuas, pontos cegos e os piores cenários possíveis de risco. Seja implacável e conciso (máximo 2 parágrafos).
Tema: %s`, topic),
		},
	}

	var wg sync.WaitGroup
	results := make([]AgentOutput, len(roles))
	var mu sync.Mutex
	totalTokens := 0

	for i, r := range roles {
		wg.Add(1)
		go func(idx int, rConfig struct {
			role   Role
			prompt string
		}) {
			defer wg.Done()
			subStart := time.Now()

			subCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()

			resp, err := o.client.DeepAnalyze(subCtx, rConfig.prompt, nil)
			subDuration := time.Since(subStart)

			if err != nil {
				results[idx] = AgentOutput{
					Role:        rConfig.role,
					Perspective: fmt.Sprintf("⚠️ Análise indisponível: %v", err),
					Duration:    subDuration,
					Success:     false,
				}
				return
			}

			mu.Lock()
			totalTokens += resp.TotalTokens
			mu.Unlock()

			results[idx] = AgentOutput{
				Role:        rConfig.role,
				Perspective: nlu.SanitizeHumanResponse(resp.Text),
				Duration:    subDuration,
				Success:     true,
				Tokens:      resp.TotalTokens,
			}
		}(i, r)
	}

	wg.Wait()

	// Final Step: Synthesize Consensus
	var perspectivesText strings.Builder
	for _, res := range results {
		if res.Success {
			perspectivesText.WriteString(fmt.Sprintf("[%s]: %s\n\n", res.Role, res.Perspective))
		}
	}

	synthesisPrompt := fmt.Sprintf(`Você é o Coordenador Chefe (ATLAS).
Três especialistas analisaram a seguinte questão: "%s"

Visões dos especialistas:
%s

Com base nas perspectivas acima (fatos, viabilidade e riscos), sintetize um veredito executivo definitivo, pragmático e com recomendações claras de ação em até 3 tópicos diretos.`, topic, perspectivesText.String())

	synthCtx, synthCancel := context.WithTimeout(ctx, 15*time.Second)
	defer synthCancel()

	consensusResp, err := o.client.DeepAnalyze(synthCtx, synthesisPrompt, nil)
	consensusText := ""
	if err == nil && consensusResp != nil {
		consensusText = nlu.SanitizeHumanResponse(consensusResp.Text)
		totalTokens += consensusResp.TotalTokens
	} else {
		consensusText = "Veredito executivo: Analise os pareceres do Pesquisador, Analista e Crítico acima para direcionar a tomada de decisão com equilíbrio entre fatos, viabilidade e mitigação de riscos."
	}

	return &Deliberation{
		Topic:       topic,
		Outputs:     results,
		Consensus:   consensusText,
		Duration:    time.Since(startTime),
		TotalTokens: totalTokens,
	}, nil
}
