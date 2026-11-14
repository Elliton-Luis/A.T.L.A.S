package nlu

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPlannerSuppliesUserQueryRequiredByProviderTemplate(t *testing.T) {
	client := NewClient("test", "", "", 450, 350, 500)
	calls := 0
	client.httpClient.Transport = catalogTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var request ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) < 2 || request.Messages[0].Role != "system" || request.Messages[len(request.Messages)-1].Role != "user" {
			t.Fatal("modelo exige instruções e consulta do usuário em mensagens distintas", request.Messages)
		}
		query := request.Messages[len(request.Messages)-1].Content
		if !strings.Contains(query, "latim") || !strings.Contains(query, "60") {
			t.Fatal("objetivo ou tempo ausente da consulta", query)
		}
		answer := `{"dificuldade":"moderada","passos":[{"acao":"Leia um texto simples em latim","minutos":20,"conclusao":"5 palavras anotadas","gratuito":true,"pratico":true},{"acao":"Escreva frases usando as palavras","minutos":20,"conclusao":"5 frases escritas","gratuito":true,"pratico":true},{"acao":"Revise as frases com o ATLAS","minutos":20,"conclusao":"5 frases corrigidas","gratuito":true,"pratico":true}]}`
		encoded, _ := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"finish_reason": "stop", "message": map[string]string{"content": answer}}}})
		return providerResponse(200, string(encoded)), nil
	})
	plan, err := client.GenerateDailyPlan(context.Background(), "Estudar latim", 60)
	if err != nil || plan.Minutes != 60 || len(plan.Steps) != 3 || calls != 1 {
		t.Fatal(plan, err, calls)
	}
}
