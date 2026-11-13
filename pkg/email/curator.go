package email

import (
	"context"
	"fmt"
	"strings"
	"time"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
)

// LLMClient is the interface needed to curate emails.
type LLMClient interface {
	DeepAnalyze(ctx context.Context, text string, history []database.ChatMessage) (*nlu.APIResponse, error)
}

// CurateEmails compiles email summaries and asks the LLM to highlight only what is truly important or interesting.
// If the LLM is slow or unavailable, it falls back instantly to a clean structured executive digest.
func CurateEmails(ctx context.Context, llm LLMClient, emails []MessageSummary) (string, error) {
	if len(emails) == 0 {
		return "📭 Sua caixa de entrada não tem novos e-mails no momento.", nil
	}

	var sb strings.Builder
	for i, m := range emails {
		sb.WriteString(fmt.Sprintf("%d. De: %s\n   Assunto: %s\n   Data: %s\n\n", i+1, m.From, m.Subject, m.Date))
	}

	// Try quick AI analysis with a strict 6-second timeout to avoid serverless drops
	aiCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`Você é o assistente executivo pessoal ATLAS.
Analise os e-mails recentes da caixa de entrada do usuário abaixo:

%s

Sua missão:
1. Destaque APENAS o que for realmente interessante, importante, urgente ou que exija atenção/ação do usuário (boletos, clientes, propostas, comunicados críticos).
2. Ignore e descarte spams, propagandas automáticas, promoções e newsletters irrelevantes.
3. Se todos forem irrelevantes/promocionais, diga isso claramente de forma executiva.
4. Mantenha o formato limpo e elegante em tópicos com marcadores.`, sb.String())

	if llm != nil {
		resp, err := llm.DeepAnalyze(aiCtx, prompt, nil)
		if err == nil && resp != nil && strings.TrimSpace(resp.Text) != "" {
			header := fmt.Sprintf("📬 *Radar de E-mails ATLAS* `(Somente Leitura • %d mensagens analisadas)`\n\n", len(emails))
			return header + strings.TrimSpace(nlu.SanitizeHumanResponse(resp.Text)), nil
		}
	}

	// Fast executive fallback (Zero tokens / <1ms)
	var fallback strings.Builder
	fallback.WriteString(fmt.Sprintf("📬 *Radar de E-mails ATLAS* `(Somente Leitura • %d mensagens)`\n\n", len(emails)))
	for _, m := range emails {
		fallback.WriteString(fmt.Sprintf("• *%s*\n  _De: %s_\n", m.Subject, m.From))
	}
	return strings.TrimSpace(fallback.String()), nil
}
