package email

import (
	"context"
	"strings"
	"testing"
	"time"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
)

type digestLLM struct {
	calls  int
	answer string
	prompt string
}

func (m *digestLLM) DeepAnalyze(_ context.Context, text string, _ []database.ChatMessage) (*nlu.APIResponse, error) {
	m.calls++
	m.prompt = text
	return &nlu.APIResponse{Text: m.answer}, nil
}

func TestEmailCompositionUsesOneInferenceAndRealMessages(t *testing.T) {
	llm := &digestLLM{answer: `{"resumo":"Há uma fatura que exige atenção e uma promoção sem prioridade.","relevantes":["10"]}`}
	messages := []MessageSummary{{ID: "10", Subject: "Fatura", From: "Banco", Snippet: "Vence dia 20"}, {ID: "11", Subject: "Promoção", From: "Loja", Snippet: "Compre hoje"}}
	result, err := CurateWithSummary(context.Background(), llm, messages, "de hoje")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"E-mails relevantes", "Resumo", "Fatura", "Vence dia 20", "Há uma fatura", "2 mensagem(ns)"} {
		if !strings.Contains(result, expected) {
			t.Errorf("ausente %q: %s", expected, result)
		}
	}
	if strings.Contains(result, "UID: 11") || llm.calls != 1 {
		t.Fatalf("seleção ou número de inferências incorreto: %d, %s", llm.calls, result)
	}
	if !strings.Contains(llm.prompt, "Vence dia 20") {
		t.Fatal("resumo não recebeu conteúdo")
	}
}

func TestDigestRejectsInventedUIDAndStillWorksOffline(t *testing.T) {
	messages := []MessageSummary{{ID: "1", Subject: "Reunião", From: "Cliente", Snippet: "Terça às 10h"}}
	llm := &digestLLM{answer: `{"resumo":"resposta inválida","relevantes":["999"]}`}
	for _, client := range []LLMClient{nil, llm} {
		result, err := CurateWithSummary(context.Background(), client, messages, "não lidas")
		if err != nil || !strings.Contains(result, "Resumo") || !strings.Contains(result, "Reunião") || strings.Contains(result, "resposta inválida") {
			t.Fatalf("fallback incorreto: %s (%v)", result, err)
		}
	}
}

func TestCompoundQueryKeepsSynthesisOutOfFilters(t *testing.T) {
	for _, text := range []string{"veja os emails do João e faça um resumo", "leia os e-mails do João, resuma tudo", "emails do João e traga uma síntese"} {
		query := ParseQuery(text, time.Now())
		if query.From != "João" || !WantsSummary(text) {
			t.Fatalf("filtros ou intenção perdidos em %s: %+v", text, query)
		}
	}
}
