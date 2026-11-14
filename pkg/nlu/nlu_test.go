package nlu

import (
	"atlas/pkg/database"
	"strings"
	"testing"
)

func TestSanitizeHumanResponse(t *testing.T) {
	// Case 1: Truncated JSON leak
	rawTruncated := `{
  "tipo": "conversa",
  "complexidade": "simples",
  "resposta": "Senhor, sou o ATLAS, seu assistente executivo pessoal. Minhas capacidades operam em três frentes principais:\n\n1. Gestão de Tempo...\n2. Proatividade e Rotinas... e foi cortado`

	clean := SanitizeHumanResponse(rawTruncated)
	if strings.Contains(clean, `"tipo"`) || strings.Contains(clean, `"resposta"`) || strings.Contains(clean, "{") {
		t.Errorf("SanitizeHumanResponse leaked JSON tags in truncated case: %s", clean)
	}
	if !strings.Contains(clean, "Senhor, sou o ATLAS") {
		t.Errorf("SanitizeHumanResponse lost content: %s", clean)
	}

	// Case 2: Full JSON string
	rawFull := `{"tipo": "conversa", "resposta": "Olá! Como posso ajudar hoje?"}`
	cleanFull := SanitizeHumanResponse(rawFull)
	if cleanFull != "Olá! Como posso ajudar hoje?" {
		t.Errorf("expected 'Olá! Como posso ajudar hoje?', got: %s", cleanFull)
	}

	// Case 3: Already clean string
	normal := "Olá, sou o ATLAS."
	if SanitizeHumanResponse(normal) != normal {
		t.Errorf("clean string was modified unexpectedly")
	}
}

func TestBuildCleanHistory(t *testing.T) {
	// Scenario: multiple orphan user messages in a row
	history := []database.ChatMessage{
		{Role: "user", Content: "Olá"},
		{Role: "user", Content: "Vou para o Rio"},
		{Role: "assistant", Content: "Ótima viagem."},
		{Role: "user", Content: "Nova mensagem"},
	}

	clean := buildCleanHistory(history, "Mais uma pergunta")
	// Expected:
	// Preserve the unanswered user turn together with its follow-up.
	if len(clean) != 3 {
		t.Fatalf("expected 3 clean messages, got %d", len(clean))
	}
	if clean[0].Role != "user" || clean[0].Content != "Vou para o Rio" {
		t.Errorf("unexpected first message: %+v", clean[0])
	}
	if clean[1].Role != "assistant" || clean[1].Content != "Ótima viagem." {
		t.Errorf("unexpected second message: %+v", clean[1])
	}
	if clean[2].Role != "user" || !strings.Contains(clean[2].Content, "Nova mensagem") || !strings.Contains(clean[2].Content, "Mais uma pergunta") {
		t.Errorf("unexpected third message: %+v", clean[2])
	}
}
