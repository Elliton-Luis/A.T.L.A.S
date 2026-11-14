package planner

import (
	"fmt"
	"regexp"
	"strings"

	"atlas/pkg/synonyms"
)

type Plan struct {
	ID           int64  `json:"-"`
	Date         string `json:"-"`
	Objective    string `json:"-"`
	Minutes      int    `json:"-"`
	ParentTaskID int64  `json:"-"`
	Difficulty   string `json:"dificuldade"`
	Steps        []Step `json:"passos"`
}

type Step struct {
	TaskID     int64  `json:"-"`
	Action     string `json:"acao"`
	Minutes    int    `json:"minutos"`
	Completion string `json:"conclusao"`
	Free       bool   `json:"gratuito"`
	Practical  bool   `json:"pratico"`
	Resource   string `json:"recurso,omitempty"`
	Done       bool   `json:"-"`
}

// Public, free references avoid invented links and trial/subscription resources.
const Resources = `Recursos opcionais permitidos (use a URL exata, somente se ajudar):
https://learnenglishkids.britishcouncil.org/read-write/short-stories — histórias infantis em inglês
https://learnenglish.britishcouncil.org/ — leitura e exercícios gratuitos de inglês
https://www.gutenberg.org/ — livros de domínio público
https://pt.khanacademy.org/ — conteúdos e exercícios gratuitos
https://developer.mozilla.org/pt-BR/docs/ — documentação e exemplos de programação
https://go.dev/tour/ — exercícios Go no navegador
https://pt.wikipedia.org/ — textos de referência
O usuário também pode praticar com o próprio ATLAS, papel e materiais que já possui. Nenhuma compra é necessária.`

var allowedResources = map[string]bool{
	"https://learnenglishkids.britishcouncil.org/read-write/short-stories": true,
	"https://learnenglish.britishcouncil.org/":                             true,
	"https://www.gutenberg.org/":                                           true,
	"https://pt.khanacademy.org/":                                          true,
	"https://developer.mozilla.org/pt-BR/docs/":                            true,
	"https://go.dev/tour/":                                                 true,
	"https://pt.wikipedia.org/":                                            true,
}

var paidAction = regexp.MustCompile(`\b(comprar|compre|pagar|pague|assinar|assine|contratar|contrate|premium|assinatura|mensalidade|trial|freemium)\b|teste gratis|teste gratuito|curso pago|versao pro`)
var bareURL = regexp.MustCompile(`(?i)https?://|www\.`)

// StepBounds keeps short sessions actionable without fragmenting them excessively.
func StepBounds(minutes int) (int, int) {
	switch {
	case minutes <= 5:
		return 1, 1
	case minutes <= 15:
		return 1, 2
	case minutes <= 30:
		return 2, 3
	case minutes <= 60:
		return 2, 4
	case minutes <= 120:
		return 3, 5
	case minutes <= 240:
		return 4, 6
	default:
		return 5, 8
	}
}

func Validate(plan Plan) error {
	if strings.TrimSpace(plan.Objective) == "" {
		return fmt.Errorf("informe a atividade que deseja planejar")
	}
	if plan.Minutes < 1 || plan.Minutes > 480 {
		return fmt.Errorf("defina um bloco entre 1 e 480 minutos")
	}
	minimum, maximum := StepBounds(plan.Minutes)
	if len(plan.Steps) < minimum || len(plan.Steps) > maximum {
		return fmt.Errorf("para %d minutos, use entre %d e %d passos", plan.Minutes, minimum, maximum)
	}
	switch synonyms.RemoveAccents(strings.ToLower(plan.Difficulty)) {
	case "facil", "moderada", "dificil":
	default:
		return fmt.Errorf("dificuldade inválida")
	}
	total := 0
	seen := make(map[string]bool)
	for _, step := range plan.Steps {
		text := strings.TrimSpace(step.Action)
		key := synonyms.RemoveAccents(strings.ToLower(text))
		if len(strings.Fields(text)) < 2 || strings.TrimSpace(step.Completion) == "" {
			return fmt.Errorf("cada passo precisa de ação concreta e resultado verificável")
		}
		if seen[key] {
			return fmt.Errorf("passos repetidos")
		}
		seen[key] = true
		if step.Minutes < 1 {
			return fmt.Errorf("cada passo precisa de tempo positivo")
		}
		if !step.Free || !step.Practical {
			return fmt.Errorf("todos os passos devem ser práticos e gratuitos")
		}
		if paidAction.MatchString(key + " " + synonyms.RemoveAccents(strings.ToLower(step.Completion))) {
			return fmt.Errorf("o plano não pode exigir compras, assinatura ou teste promocional")
		}
		if bareURL.MatchString(step.Action + " " + step.Completion) {
			return fmt.Errorf("links devem ficar no campo de recurso")
		}
		if step.Resource != "" && !allowedResources[step.Resource] {
			return fmt.Errorf("recurso fora do catálogo gratuito")
		}
		total += step.Minutes
	}
	if total != plan.Minutes {
		return fmt.Errorf("a soma dos passos (%d min) deve ser igual ao tempo escolhido (%d min)", total, plan.Minutes)
	}
	return nil
}
