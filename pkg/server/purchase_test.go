package server

import (
	"context"
	"strings"
	"testing"

	"atlas/pkg/nlu"
	"atlas/pkg/telegram"
)

func TestPurchaseUsesSavedSalaryAndDoesNotPersistSimulationDebts(t *testing.T) {
	s := automationServer(t)
	ctx := context.Background()
	calls := mockPersonalAPI(t, `{}`)
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "/salario 5.000,00"})
	result := s.purchaseCommand(ctx, 1, "1000 2000 500")
	if !strings.Contains(result, "5/20") || !strings.Contains(result, "R$ 2500,00") || *calls != 0 {
		t.Fatal(result, *calls)
	}
	other := s.purchaseCommand(ctx, 1, "1000 0 0")
	if !strings.Contains(other, "10/20") {
		t.Fatal("dívida de outra simulação reaproveitada", other)
	}
	value, ok := s.savedSalary(ctx)
	if !ok || value != 500000 {
		t.Fatal("salário não preservado")
	}
	if command, ok := matchFastPath("posso comprar um celular hoje?"); ok {
		t.Fatal("simulação virou outra consulta", command)
	}
}

func TestPurchaseAsksMissingSalaryWithoutLosingOtherValues(t *testing.T) {
	s := automationServer(t)
	ctx := context.Background()
	calls := mockPersonalAPI(t, `{}`)
	result := s.purchaseCommand(ctx, 1, "1000 2000 500")
	if !strings.Contains(result, "salário") {
		t.Fatal(result)
	}
	s.processIncomingMessage(ctx, &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "5.000,00"})
	if _, err := s.db.GetPendingConfirmation(ctx, 1); err == nil {
		t.Fatal("simulação permaneceu pendente")
	}
	if value, ok := s.savedSalary(ctx); !ok || value != 500000 || *calls != 0 {
		t.Fatal("valor perdido ou cálculo consumiu IA", value, ok, *calls)
	}
	cost, expense, debt := 1000.0, 2000.0, 500.0
	result = s.purchaseAction(ctx, 1, &nlu.IntentResult{Acao: "simular_compra", CustoItem: &cost, Gastos: &expense, Dividas: &debt})
	if !strings.Contains(result, "5/20") {
		t.Fatal(result)
	}
}
