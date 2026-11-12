package telegram

import (
	"strings"
	"testing"
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
