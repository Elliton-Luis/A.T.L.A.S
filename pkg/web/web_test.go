package web

import (
	"context"
	"testing"
	"time"
)

func TestSearchGoogleNews(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	news, err := SearchGoogleNews(ctx, "tecnologia brasil", 3)
	if err != nil {
		t.Fatalf("SearchGoogleNews failed: %v", err)
	}
	if len(news) == 0 {
		t.Fatalf("expected at least 1 news item")
	}
	if news[0].Title == "" {
		t.Fatalf("expected news title to be populated")
	}
}

func TestFetchLiturgiaDiaria(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	text, err := FetchLiturgiaDiaria(ctx)
	if err != nil {
		t.Fatalf("FetchLiturgiaDiaria failed: %v", err)
	}
	if len(text) < 20 {
		t.Fatalf("expected liturgia text, got: %s", text)
	}
}
