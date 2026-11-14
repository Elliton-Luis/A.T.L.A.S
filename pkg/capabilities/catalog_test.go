package capabilities

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCatalogStatesReflectConfigurationNotAssumedHealth(t *testing.T) {
	missing := Describe(Runtime{}, "emails")
	if !strings.Contains(missing, "requer configuração") || !strings.Contains(missing, "Somente leitura") {
		t.Fatal(missing)
	}
	configured := Describe(Runtime{Email: true}, "emails")
	if !strings.Contains(configured, "configurado") || !strings.Contains(configured, "conexão não testada") || strings.Contains(configured, "online") {
		t.Fatal(configured)
	}
	if got := Describe(Runtime{}, "pdf"); !strings.Contains(got, "geração local disponível") {
		t.Fatal(got)
	}
	cron := Describe(Runtime{Telegram: true, ScheduledChat: true}, "automacoes")
	if !strings.Contains(cron, "endpoint cron") {
		t.Fatal(cron)
	}
	running := Describe(Runtime{Telegram: true, ScheduledChat: true, InternalScheduler: true}, "automacoes")
	if !strings.Contains(running, "ticker interno") {
		t.Fatal(running)
	}
}

func TestEveryAdvertisedActionHasAnActualServerCase(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "server", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	actual := make(map[string]bool)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				value, ok := expr.(*ast.BasicLit)
				if ok && value.Kind == token.STRING {
					if text, err := strconv.Unquote(value.Value); err == nil {
						actual[text] = true
					}
				}
			}
			return true
		})
	}
	seen := make(map[string]bool)
	for _, item := range Catalog(Runtime{}) {
		if seen[item.ID] {
			t.Errorf("capacidade duplicada: %s", item.ID)
		}
		seen[item.ID] = true
		for _, action := range item.Actions {
			if !actual[action] {
				t.Errorf("%s anuncia ação sem executor: %s", item.ID, action)
			}
		}
	}
}

func TestPromptNamesToolsAndHonestExecutionBoundary(t *testing.T) {
	prompt := Prompt(Runtime{AI: true, Email: true})
	for _, expected := range []string{"exportar_pdf", "planejar_dia", "rastrear_site", "executar_terminal", "deliberar", "resumir_emails", "credencial ausente", "o texto não executa ferramentas", "não leio agendas externas", "não envio"} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("falta %q no catálogo", expected)
		}
	}
}
