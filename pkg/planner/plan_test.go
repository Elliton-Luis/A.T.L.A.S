package planner

import (
	"strings"
	"testing"
)

func samplePlan() Plan {
	return Plan{Objective: "Estudar inglês", Minutes: 30, Difficulty: "facil", Steps: []Step{
		{Action: "Procure um texto infantil curto", Minutes: 5, Completion: "1 texto selecionado", Free: true, Practical: true, Resource: "https://learnenglishkids.britishcouncil.org/read-write/short-stories"},
		{Action: "Leia o texto e anote palavras", Minutes: 10, Completion: "5 palavras anotadas", Free: true, Practical: true},
		{Action: "Converse em inglês com o ATLAS", Minutes: 15, Completion: "5 frases trocadas", Free: true, Practical: true},
	}}
}

func TestPlanEnforcesTimePracticeAndFreeResources(t *testing.T) {
	if err := Validate(samplePlan()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*Plan)
	}{
		{"tempo incorreto", func(p *Plan) { p.Steps[0].Minutes = 6 }},
		{"sem prática", func(p *Plan) { p.Steps[1].Practical = false }},
		{"não gratuito", func(p *Plan) { p.Steps[1].Free = false }},
		{"compra disfarçada", func(p *Plan) { p.Steps[0].Action = "Compre um livro para praticar" }},
		{"link inventado", func(p *Plan) { p.Steps[0].Resource = "https://curso-pago.example" }},
		{"link fora do campo", func(p *Plan) { p.Steps[0].Action = "Abra https://curso.example para estudar" }},
		{"sem resultado", func(p *Plan) { p.Steps[0].Completion = "" }},
		{"muitos passos", func(p *Plan) { p.Steps = append(p.Steps, p.Steps[0]) }},
		{"poucos passos", func(p *Plan) { p.Steps = p.Steps[:1]; p.Steps[0].Minutes = 30 }},
		{"duração ausente", func(p *Plan) { p.Minutes = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := samplePlan()
			test.change(&p)
			if err := Validate(p); err == nil {
				t.Fatal("plano inválido aceito")
			}
		})
	}
}

func TestDurationUnderstandsNaturalBudgets(t *testing.T) {
	for _, test := range []struct {
		text    string
		minutes int
	}{{"inglês por 30min", 30}, {"treinar por meia hora", 30}, {"ler durante 1h30", 90}, {"estudar por uma hora e quinze minutos", 75}, {"estudar por quarenta e cinco minutos", 45}, {"praticar por 1h e 20min", 80}, {"ler por um quarto de hora", 15}, {"inglês por 1h30min", 90}, {"inglês por 1,5h", 90}, {"30min ou 1 hora", 60}, {"estudar por 2-3 horas", 180}} {
		minutes, remaining, found, err := ParseDuration(test.text)
		if err != nil || !found || minutes != test.minutes {
			t.Errorf("%s: %d (%v)", test.text, minutes, err)
		}
		if strings.Contains(remaining, "30min") {
			t.Fatal("tempo ficou no objetivo")
		}
	}
	for _, text := range []string{"0 min", "1h90", "999 min"} {
		if _, _, _, err := ParseDuration(text); err == nil {
			t.Errorf("duração inválida aceita: %s", text)
		}
	}
	if _, _, found, _ := ParseDuration("Estudar inglês"); found {
		t.Fatal("duração inventada")
	}
}
