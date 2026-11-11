package nlu

import (
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
