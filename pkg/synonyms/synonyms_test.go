package synonyms

import (
	"testing"
)

func TestCanonicalizeSynonyms(t *testing.T) {
	// 1. Testa sinônimos de apagar
	deleteWords := []string{"apagar", "deletar", "limpar", "remover", "excluir", "zerar", "eliminar"}
	for _, w := range deleteWords {
		canon := CanonicalizeWord(w)
		if canon != "apagar" {
			t.Errorf("word '%s' should canonicalize to 'apagar', got '%s'", w, canon)
		}
	}

	// 2. Testa sinônimos de agenda
	agendaWords := []string{"agenda", "compromissos", "eventos"}
	for _, w := range agendaWords {
		canon := CanonicalizeWord(w)
		if canon != "agenda" {
			t.Errorf("word '%s' should canonicalize to 'agenda', got '%s'", w, canon)
		}
	}
	for _, w := range []string{"tarefa", "tarefas", "afazeres", "checklist", "task", "tasks"} {
		if canonical := CanonicalizeWord(w); canonical != "tarefa" {
			t.Errorf("%s: esperado tarefa, recebido %s", w, canonical)
		}
	}

	// 3. Testa sinônimos de lembretes
	lembreteWords := []string{"lembrete", "lembretes", "avisos", "notificações"}
	for _, w := range lembreteWords {
		canon := CanonicalizeWord(w)
		if canon != "lembrete" {
			t.Errorf("word '%s' should canonicalize to 'lembrete', got '%s'", w, canon)
		}
	}

	// 4. Frase complexa: "deletar todos os eventos" -> deve ter 'apagar', 'todo', 'agenda'
	tokens := TokenizeAndCanonicalize("deletar todos os eventos")
	if !HasPair(tokens, "apagar", "agenda") {
		t.Errorf("expected tokens to contain 'apagar' and 'agenda', got: %v", tokens)
	}

	// 5. Frase: "exibir meu calendário" -> deve ter 'ver' e 'calendario'
	tokensCal := TokenizeAndCanonicalize("exibir meu calendário")
	if !HasPair(tokensCal, "ver", "calendario") {
		t.Errorf("expected tokens to contain 'ver' and 'calendario', got: %v", tokensCal)
	}

	// 6. Sinônimos de Santo e Versículo
	if CanonicalizeWord("santododia") != "santo" || CanonicalizeWord("padroeiro") != "santo" {
		t.Errorf("expected santo canonicalization")
	}
	if CanonicalizeWord("versiculo") != "versiculo" || CanonicalizeWord("biblia") != "versiculo" || CanonicalizeWord("vulgata") != "versiculo" {
		t.Errorf("expected versiculo canonicalization")
	}

	// 7. Sinônimos de Contas e Pagar
	if CanonicalizeWord("cartao") != "conta" || CanonicalizeWord("fatura") != "conta" || CanonicalizeWord("assinatura") != "conta" {
		t.Errorf("expected conta canonicalization")
	}
	if CanonicalizeWord("paguei") != "pagar" || CanonicalizeWord("quitei") != "pagar" || CanonicalizeWord("pago") != "pagar" {
		t.Errorf("expected pagar canonicalization")
	}
}
