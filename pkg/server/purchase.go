package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"atlas/pkg/database"
	"atlas/pkg/finance"
	"atlas/pkg/nlu"
)

func (s *Server) savedSalary(ctx context.Context) (int64, bool) {
	memories, err := s.db.GetAllUserMemories(ctx)
	if err != nil {
		return 0, false
	}
	for _, key := range []string{"salario_mensal", "salario_liquido", "salario", "renda_mensal"} {
		for _, memory := range memories {
			if memory.Chave == key {
				value, err := finance.ParseMoney(memory.Valor)
				if err == nil {
					return value, true
				}
			}
		}
	}
	return 0, false
}

func (s *Server) setSalary(ctx context.Context, text string) string {
	value, err := finance.ParseMoney(text)
	if err != nil {
		return "⚠️ " + err.Error()
	}
	if err := s.db.SaveUserMemory(ctx, "salario_mensal", fmt.Sprintf("%d.%02d", value/100, value%100)); err != nil {
		return "⚠️ Não foi possível salvar o salário."
	}
	return "✅ Salário mensal salvo: " + finance.Money(value)
}

func floatMoney(value *float64) (int64, error) {
	if value == nil {
		return 0, nil
	}
	return finance.ParseMoney(strconv.FormatFloat(*value, 'f', 2, 64))
}

func (s *Server) purchaseAction(ctx context.Context, chatID int64, intent *nlu.IntentResult) string {
	if intent.Acao == "definir_salario" {
		if intent.Salario == nil {
			return "Qual seu salário mensal? Exemplo: /salario 5000"
		}
		return s.setSalary(ctx, strconv.FormatFloat(*intent.Salario, 'f', 2, 64))
	}
	cost, err := floatMoney(intent.CustoItem)
	if err != nil || cost <= 0 {
		return "Qual o custo do item? Exemplo: simule comprar um item de R$ 1000, com gastos de R$ 2000 e dívidas de R$ 500."
	}
	expenses, err := floatMoney(intent.Gastos)
	if err != nil {
		return "⚠️ Gastos inválidos."
	}
	debt, err := floatMoney(intent.Dividas)
	if err != nil {
		return "⚠️ Dívidas inválidas."
	}
	input := finance.Purchase{Cost: cost, Expenses: expenses, Debt: debt}
	var hasSalary bool
	if intent.Salario != nil {
		input.Salary, err = floatMoney(intent.Salario)
		if err != nil {
			return "⚠️ Salário inválido."
		}
		hasSalary = true
	} else {
		input.Salary, hasSalary = s.savedSalary(ctx)
	}
	if !hasSalary {
		body, _ := json.Marshal(input)
		if err := s.db.SavePendingConfirmation(ctx, chatID, "simular_compra", 0, string(body)); err != nil {
			return "⚠️ Não foi possível guardar a simulação."
		}
		return "Qual seu salário mensal? Vou salvá-lo e usar os gastos, dívidas e custo que você já informou nesta simulação."
	}
	return purchaseResult(input)
}

func purchaseResult(input finance.Purchase) string {
	result, err := finance.Simulate(input)
	if err != nil {
		return "⚠️ " + err.Error()
	}
	status := "Abaixo da reserva mínima de 2x. Considere imprevistos."
	if result.MinimumReserve {
		status = "Reserva mínima de 2x atingida."
	}
	return fmt.Sprintf("*Índice de compra (simulação): %d/20*\n%s\n\nSalário: %s\nGastos informados: %s\nDívidas informadas: %s\nSaldo: %s\nItem: %s\nSaldo/custo: %.2fx\n%s", result.Index, result.Label, finance.Money(input.Salary), finance.Money(input.Expenses), finance.Money(input.Debt), finance.Money(result.Available), finance.Money(input.Cost), result.Ratio, status)
}

func (s *Server) handlePurchaseSalary(ctx context.Context, chatID int64, text string, pending *database.ConfirmacaoPendente) (bool, string) {
	clean := strings.ToLower(strings.Trim(text, " .!?"))
	if clean == "cancelar" || clean == "cancele" || clean == "cancela" {
		_ = s.db.DeletePendingConfirmation(ctx, chatID)
		return true, "Simulação cancelada."
	}
	salary, err := finance.ParseMoney(text)
	if err != nil {
		return false, ""
	}
	var input finance.Purchase
	if json.Unmarshal([]byte(pending.NovoEventoJSON), &input) != nil {
		return true, "⚠️ Não foi possível recuperar a simulação."
	}
	input.Salary = salary
	confirmation := s.setSalary(ctx, text)
	if strings.HasPrefix(confirmation, "⚠️") {
		return true, confirmation
	}
	_ = s.db.DeletePendingConfirmation(ctx, chatID)
	return true, confirmation + "\n\n" + purchaseResult(input)
}

func (s *Server) purchaseCommand(ctx context.Context, chatID int64, text string) string {
	parts := strings.Fields(text)
	if len(parts) < 1 || len(parts) > 3 {
		return "Use /simular_compra <custo> [gastos] [dívidas]. Exemplo: /simular_compra 1000 2000 500. O salário vem de /salario."
	}
	values := make([]int64, 3)
	for i, part := range parts {
		value, err := finance.ParseMoney(part)
		if err != nil {
			return "⚠️ " + err.Error()
		}
		values[i] = value
	}
	if values[0] <= 0 {
		return "⚠️ O custo do item deve ser maior que zero."
	}
	salary, ok := s.savedSalary(ctx)
	if !ok {
		body, _ := json.Marshal(finance.Purchase{Cost: values[0], Expenses: values[1], Debt: values[2]})
		if err := s.db.SavePendingConfirmation(ctx, chatID, "simular_compra", 0, string(body)); err != nil {
			return "⚠️ Não foi possível guardar a simulação."
		}
		return "Qual seu salário mensal? Pode responder com o valor, como 5000."
	}
	return purchaseResult(finance.Purchase{Salary: salary, Cost: values[0], Expenses: values[1], Debt: values[2]})
}
