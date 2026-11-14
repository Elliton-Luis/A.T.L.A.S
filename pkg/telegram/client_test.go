package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitMessage(t *testing.T) {
	// Case 1: Text within limit
	shortText := "Olá, mundo!"
	chunks := splitMessage(shortText, 4000)
	if len(chunks) != 1 || chunks[0] != shortText {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	// Case 2: Long research text exceeding 4000 chars
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("Parágrafo de teste sobre pesquisa científica e literatura com detalhes analíticos extensos.\n")
	}
	longText := sb.String()
	if len(longText) <= 4000 {
		t.Fatalf("expected longText to be > 4000 chars, got %d", len(longText))
	}

	chunks = splitMessage(longText, 4000)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	for i, chunk := range chunks {
		if len(chunk) > 4000 {
			t.Errorf("chunk %d exceeds 4000 chars: %d", i, len(chunk))
		}
	}
}

func TestSplitMessagePreservesUTF8(t *testing.T) {
	text := strings.Repeat("á⛪", 1000)
	chunks := splitMessage(text, 4000)
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk) {
			t.Fatal("corte dividiu caractere UTF-8")
		}
	}
	if strings.Join(chunks, "") != text {
		t.Fatal("conteúdo perdido no corte")
	}
}

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPlainFallbackRejectsFailedDelivery(t *testing.T) {
	client := NewClient("test")
	calls := 0
	client.httpClient.Transport = responseTransport(func(*http.Request) (*http.Response, error) {
		calls++
		body := `{"description":"can't parse entities"}`
		status := 400
		if calls == 2 {
			status = 429
			body = `{"description":"Too Many Requests"}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	err := client.SendMessage(context.Background(), 1, "texto")
	var rejected *RejectedError
	if !errors.As(err, &rejected) || rejected.Status != 429 || calls != 2 {
		t.Fatalf("falha foi considerada sucesso: %v (%d chamadas)", err, calls)
	}
}
