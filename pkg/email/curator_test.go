package email

import (
	"context"
	"strings"
	"testing"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
)

type mockCuratorLLM struct{}

func (m *mockCuratorLLM) DeepAnalyze(ctx context.Context, text string, history []database.ChatMessage) (*nlu.APIResponse, error) {
	return &nlu.APIResponse{
		Text: "• Importante: Sua fatura bancária fecha amanhã no valor de R$ 350,00.",
	}, nil
}

func TestCurateEmails(t *testing.T) {
	emails := []MessageSummary{
		{
			ID:      "1",
			From:    "banco@exemplo.com",
			Subject: "Fatura Disponível",
			Date:    "12 Nov",
			Snippet: "Sua fatura fechou",
		},
		{
			ID:      "2",
			From:    "loja@promo.com",
			Subject: "Desconto de 50%",
			Date:    "12 Nov",
			Snippet: "Compre agora",
		},
	}

	result, err := CurateEmails(context.Background(), &mockCuratorLLM{}, emails)
	if err != nil {
		t.Fatalf("CurateEmails failed: %v", err)
	}

	if !strings.Contains(result, "Radar de E-mails") {
		t.Errorf("expected header in result, got: %s", result)
	}
	if !strings.Contains(result, "fatura bancária") {
		t.Errorf("expected LLM curated text, got: %s", result)
	}
}

func TestCurateEmptyEmails(t *testing.T) {
	result, err := CurateEmails(context.Background(), &mockCuratorLLM{}, nil)
	if err != nil {
		t.Fatalf("CurateEmails failed: %v", err)
	}

	if !strings.Contains(result, "Nenhum e-mail corresponde à consulta") {
		t.Errorf("expected empty inbox message, got: %s", result)
	}
}
