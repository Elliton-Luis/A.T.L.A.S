package finance

import "testing"

func TestPurchaseLevelsAndReserveBoundaries(t *testing.T) {
	for _, test := range []struct {
		salary, expenses, debt, cost int64
		level                        int
		reserve                      bool
	}{{500000, 200000, 50000, 100000, 5, true}, {100000, 0, 0, 100000, 2, false}, {199999, 0, 0, 100000, 3, false}, {200000, 0, 0, 100000, 4, true}, {1000000, 0, 0, 100000, 20, true}, {100000, 200000, 0, 100000, 1, false}} {
		result, err := Simulate(Purchase{Salary: test.salary, Expenses: test.expenses, Debt: test.debt, Cost: test.cost})
		if err != nil || result.Index != test.level || result.MinimumReserve != test.reserve {
			t.Fatalf("%+v => %+v (%v)", test, result, err)
		}
	}
	for half := 1; half <= 20; half++ {
		result, err := Simulate(Purchase{Salary: int64(half * 5000), Cost: 10000})
		if err != nil || result.Index != half {
			t.Fatalf("níveis incorretos: %d %+v %v", half, result, err)
		}
	}
	if _, err := Simulate(Purchase{Cost: 0}); err == nil {
		t.Fatal("divisão por zero aceita")
	}
	if _, err := Simulate(Purchase{Salary: 100, Expenses: -1, Cost: 1}); err == nil {
		t.Fatal("valor negativo aceito")
	}
}

func TestMoneyParsingDoesNotLoseCents(t *testing.T) {
	for text, want := range map[string]int64{"R$ 5.000,25": 500025, "5000.25": 500025, "1.500": 150000, "0,01": 1, "100,3": 10030} {
		got, err := ParseMoney(text)
		if err != nil || got != want {
			t.Errorf("%s => %d (%v)", text, got, err)
		}
	}
	for _, text := range []string{"-100", "abc", "1,999", "NaN", "10000000000,01"} {
		if _, err := ParseMoney(text); err == nil {
			t.Errorf("inválido aceito: %s", text)
		}
	}
}
