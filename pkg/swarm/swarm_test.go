package swarm

import (
	"context"
	"strings"
	"testing"
	"time"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
)

type mockLLM struct{}

func (m *mockLLM) DeepAnalyze(ctx context.Context, text string, history []database.ChatMessage) (*nlu.APIResponse, error) {
	respText := "Perspective response for: " + text[:min(len(text), 30)]
	if strings.Contains(text, "Coordenador Chefe") {
		respText = "Veredito Executivo: Avançar com cautela e mitigar os riscos identificados."
	}
	return &nlu.APIResponse{
		Text:        respText,
		TotalTokens: 42,
		Provider:    "mock",
		Model:       "mock-model",
	}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestSwarmDeliberation(t *testing.T) {
	orchestrator := NewOrchestrator(&mockLLM{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := orchestrator.Deliberate(ctx, "Devo migrar o banco de dados para Turso?")
	if err != nil {
		t.Fatalf("Deliberate failed: %v", err)
	}

	if len(res.Outputs) != 3 {
		t.Fatalf("expected 3 specialist perspectives, got %d", len(res.Outputs))
	}

	for _, o := range res.Outputs {
		if !o.Success {
			t.Errorf("expected subagent %s to succeed", o.Role)
		}
	}

	if !strings.Contains(res.Consensus, "Veredito Executivo") {
		t.Errorf("expected consensus to contain executive verdict, got: %s", res.Consensus)
	}

	hud := res.FormatTelegramHUD()
	if !strings.Contains(hud, "Conselho de Especialistas") || !strings.Contains(hud, "Pesquisador") {
		t.Errorf("unexpected HUD formatting: %s", hud)
	}
}
