package nlu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"atlas/pkg/capabilities"
)

type catalogTransport func(*http.Request) (*http.Response, error)

func (f catalogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAllConversationalPathsReceiveCurrentCapabilityCatalog(t *testing.T) {
	client := NewClient("test", "", "", 450, 350, 500)
	client.SetCapabilitySource(func() capabilities.Runtime {
		return capabilities.Runtime{AI: true, Email: true, Telegram: true, ScheduledChat: true}
	})
	calls := 0
	client.httpClient.Transport = catalogTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var request ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) == 0 || request.Messages[0].Role != "system" {
			t.Fatal("catálogo não está em contexto de sistema")
		}
		for _, feature := range []string{"planejar_dia", "exportar_pdf", "executar_terminal", "rastrear_site", "deliberar", "emails/E-mails [configurado]", "o texto não executa ferramentas"} {
			if !strings.Contains(request.Messages[0].Content, feature) {
				t.Errorf("chamada %d não recebeu %s", calls, feature)
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"tipo\":\"acao\",\"acao\":\"ver_capacidades\"}"}}]}`))}, nil
	})
	ctx := context.Background()
	if _, err := client.ParseIntent(ctx, "quais recursos tem?", "2026-11-14", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeepAnalyze(ctx, "como você pode me ajudar?", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResearchAnalyze(ctx, "analise suas capacidades", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AnalyzeWithModel(ctx, "test-model", "explique sua integração", 100); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatal("caminho sem chamada verificada", calls)
	}
}
