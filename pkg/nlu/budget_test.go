package nlu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStructuredBudgetAndProviderQuota(t *testing.T) {
	want := 450
	truncated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.MaxTokens != want {
			t.Errorf("budget=%d, esperado=%d", payload.MaxTokens, want)
		}
		if truncated {
			w.Write([]byte(`{"choices":[{"finish_reason":"length","message":{"content":"{incompleto"}}]}`))
			return
		}
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`))
	}))
	defer server.Close()
	client := NewClient("", "", "", 450, 2048, 4096)
	payload := ChatCompletionRequest{MaxTokens: 450, Structured: true}
	if _, err := client.executeAPI(context.Background(), server.URL+"/groq", "test", payload); err != nil {
		t.Fatal(err)
	}
	want = 350
	payload.Structured = false
	if _, err := client.executeAPI(context.Background(), server.URL+"/groq", "test", payload); err != nil {
		t.Fatal(err)
	}
	truncated = true
	if _, err := client.executeAPI(context.Background(), server.URL+"/groq", "test", payload); err == nil {
		t.Fatal("resposta truncada aceita")
	}
}
