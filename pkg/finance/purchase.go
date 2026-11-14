// Package finance provides deterministic purchase simulations in integer cents.
package finance

import (
	"fmt"
	"strconv"
	"strings"
)

type Purchase struct{ Salary, Expenses, Debt, Cost int64 }
type Assessment struct {
	Available      int64
	Index          int
	Ratio          float64
	MinimumReserve bool
	Label          string
}

var Labels = [20]string{"Saldo insuficiente", "Sem reserva para imprevistos", "Reserva abaixo de 2x", "Mínimo de 2x atingido", "Margem de 2,5x", "Margem de 3x", "Margem de 3,5x", "Margem de 4x", "Margem de 4,5x", "Folga de 5x", "Folga de 5,5x", "Folga de 6x", "Folga de 6,5x", "Folga de 7x", "Folga de 7,5x", "Folga de 8x", "Folga de 8,5x", "Folga de 9x", "Folga de 9,5x", "Folga alta: 10x ou mais"}

func Simulate(input Purchase) (Assessment, error) {
	for _, value := range []int64{input.Salary, input.Expenses, input.Debt, input.Cost} {
		if value < 0 || value > 1_000_000_000_000 {
			return Assessment{}, fmt.Errorf("use valores não negativos de até R$ 10 bilhões")
		}
	}
	if input.Cost == 0 {
		return Assessment{}, fmt.Errorf("o custo do item deve ser maior que zero")
	}
	available := input.Salary - input.Expenses - input.Debt
	index := int(available * 2 / input.Cost)
	if index < 1 {
		index = 1
	}
	if index > 20 {
		index = 20
	}
	return Assessment{Available: available, Index: index, Ratio: float64(available) / float64(input.Cost), MinimumReserve: available >= 2*input.Cost, Label: Labels[index-1]}, nil
}

func ParseMoney(text string) (int64, error) {
	text = strings.TrimSpace(strings.ReplaceAll(strings.ToLower(text), "r$", ""))
	text = strings.ReplaceAll(text, " ", "")
	if strings.Contains(text, ",") {
		text = strings.ReplaceAll(text, ".", "")
		text = strings.ReplaceAll(text, ",", ".")
	} else if strings.Count(text, ".") > 1 || strings.Contains(text, ".") && len(strings.SplitN(text, ".", 2)[1]) == 3 {
		text = strings.ReplaceAll(text, ".", "")
	}
	parts := strings.Split(text, ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" || strings.HasPrefix(text, "-") {
		return 0, fmt.Errorf("valor monetário inválido")
	}
	for _, part := range parts {
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, fmt.Errorf("valor monetário inválido")
			}
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > 10_000_000_000 {
		return 0, fmt.Errorf("valor monetário fora do limite")
	}
	cents := int64(0)
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) > 2 || fraction == "" {
			return 0, fmt.Errorf("use no máximo duas casas decimais")
		}
		if len(fraction) == 1 {
			fraction += "0"
		}
		cents, _ = strconv.ParseInt(fraction, 10, 64)
	}
	result := whole*100 + cents
	if result > 1_000_000_000_000 {
		return 0, fmt.Errorf("valor monetário fora do limite")
	}
	return result, nil
}

func Money(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%sR$ %d,%02d", sign, cents/100, cents%100)
}
