package nlu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func providerResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestRateLimitUsesOneAlternateModelAndCorrectJSONParameters(t *testing.T) {
	client := NewClient("test", "", "", 450, 350, 500)
	var models []string
	client.httpClient.Transport = catalogTransport(func(r *http.Request) (*http.Response, error) {
		var request ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		models = append(models, request.Model)
		if request.ResponseFormat == nil || request.ResponseFormat.Type != "json_object" {
			t.Fatal("JSON mode não foi enviado")
		}
		if len(models) == 1 {
			if request.ReasoningEffort != "none" || request.ReasoningFormat != "hidden" {
				t.Fatal("classificação gastando orçamento de raciocínio", request)
			}
			return providerResponse(429, `{"error":{"code":"rate_limit_exceeded","message":"secret must never appear"}}`), nil
		}
		if request.Model != "openai/gpt-oss-20b" || request.ReasoningEffort != "low" || request.IncludeReasoning == nil || *request.IncludeReasoning || request.ReasoningFormat != "" {
			t.Fatal("parâmetros incompatíveis no fallback", request)
		}
		return providerResponse(200, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"tipo\":\"acao\",\"acao\":\"listar_tarefas\"}"}}]}`), nil
	})
	intent, err := client.ParseIntent(context.Background(), "Mostre minhas tarefas", "2026-11-14", nil, nil, nil, nil)
	if err != nil || intent.Acao != "listar_tarefas" || len(models) != 2 {
		t.Fatal(intent, err, models)
	}
}

func TestProviderFailureRetainsSafeCauseAndDoesNotBlameMessageLength(t *testing.T) {
	client := NewClient("PRIVATE_KEY_TEST", "", "", 450, 350, 500)
	client.httpClient.Transport = catalogTransport(func(*http.Request) (*http.Response, error) {
		return providerResponse(429, `{"error":{"code":"rate_limit_exceeded","message":"PRIVATE_KEY_TEST PRIVATE_ORGANIZATION_TEST"}}`), nil
	})
	_, err := client.ParseIntent(context.Background(), "Pedido válido e completo", "2026-11-14", nil, nil, nil, nil)
	var api *ProviderError
	if !errors.As(err, &api) || api.Status != 429 || !strings.Contains(err.Error(), "rate_limit_exceeded") {
		t.Fatal("causa do provedor descartada", err)
	}
	for _, secret := range []string{"PRIVATE_KEY_TEST", "PRIVATE_ORGANIZATION_TEST"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("dados privados no erro", err)
		}
	}
	if message := FailureMessage(err); !strings.Contains(message, "limite de uso") || strings.Contains(message, "mais concisa") {
		t.Fatal(message)
	}
}

func TestAuthenticationFailureDoesNotLoopAcrossModels(t *testing.T) {
	client := NewClient("test", "", "", 450, 350, 500)
	calls := 0
	client.httpClient.Transport = catalogTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return providerResponse(401, `{"error":{"code":"invalid_api_key"}}`), nil
	})
	_, err := client.ParseIntent(context.Background(), "Liste tarefas", "2026-11-14", nil, nil, nil, nil)
	if err == nil || calls != 1 || !strings.Contains(FailureMessage(err), "credencial") {
		t.Fatal(err, calls)
	}
}

func TestReasoningOnlyIsNeverAUserFacingAnswer(t *testing.T) {
	client := NewClient("test", "", "", 450, 350, 500)
	client.httpClient.Transport = catalogTransport(func(*http.Request) (*http.Response, error) {
		return providerResponse(200, `{"choices":[{"finish_reason":"stop","message":{"content":"","reasoning":"PRIVATE_REASONING"}}]}`), nil
	})
	response, err := client.executeAPI(context.Background(), "https://api.groq.com/test", "test", ChatCompletionRequest{Model: "qwen/qwen3.8-27b", MaxTokens: 450, Structured: true})
	if response != nil || !errors.Is(err, ErrNoFinalAnswer) || strings.Contains(err.Error(), "PRIVATE_REASONING") {
		t.Fatal(response, err)
	}
}
