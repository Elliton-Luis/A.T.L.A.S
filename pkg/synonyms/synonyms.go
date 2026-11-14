package synonyms

import (
	"strings"
	"unicode"
)

// Dicionário de Sinônimos e Mapeamento Canônico
var canonicalMap = map[string]string{
	// Verbos de Exclusão / Limpeza -> "apagar"
	"apagar":   "apagar",
	"apague":   "apagar",
	"apaga":    "apagar",
	"deletar":  "apagar",
	"delete":   "apagar",
	"deleta":   "apagar",
	"limpar":   "apagar",
	"limpe":    "apagar",
	"limpa":    "apagar",
	"remover":  "apagar",
	"remova":   "apagar",
	"remove":   "apagar",
	"excluir":  "apagar",
	"exclua":   "apagar",
	"exclui":   "apagar",
	"zerar":    "apagar",
	"zere":     "apagar",
	"zera":     "apagar",
	"eliminar": "apagar",
	"elimine":  "apagar",
	"elimina":  "apagar",
	"cancelar": "apagar",
	"cancele":  "apagar",
	"resetar":  "apagar",
	"resete":   "apagar",
	"limpeza":  "apagar",

	// Verbos de Exibição / Consulta -> "ver"
	"ver":       "ver",
	"veja":      "ver",
	"mostrar":   "ver",
	"mostre":    "ver",
	"mostra":    "ver",
	"listar":    "ver",
	"liste":     "ver",
	"lista":     "ver",
	"exibir":    "ver",
	"exiba":     "ver",
	"consultar": "ver",
	"consulte":  "ver",
	"olhar":     "ver",
	"olhe":      "ver",
	"abrir":     "ver",
	"abra":      "ver",
	"trazer":    "ver",
	"traga":     "ver",

	// Agenda e Compromissos -> "agenda"
	"agenda":       "agenda",
	"agendas":      "agenda",
	"compromisso":  "agenda",
	"compromissos": "agenda",
	"evento":       "agenda",
	"eventos":      "agenda",
	"tarefa":       "tarefa",
	"tarefas":      "tarefa",
	"afazer":       "tarefa",
	"afazeres":     "tarefa",
	"task":         "tarefa",
	"tasks":        "tarefa",
	"checklist":    "tarefa",

	// Lembretes e Rotinas -> "lembrete"
	"lembrete":     "lembrete",
	"lembretes":    "lembrete",
	"rotina":       "lembrete",
	"rotinas":      "lembrete",
	"aviso":        "lembrete",
	"avisos":       "lembrete",
	"notificacao":  "lembrete",
	"notificacoes": "lembrete",
	"alarme":       "lembrete",
	"alarmes":      "lembrete",

	// Calendário e Mês -> "calendario"
	"calendario":  "calendario",
	"calendarios": "calendario",
	"mes":         "calendario",
	"meses":       "calendario",
	"grade":       "calendario",
	"mensal":      "calendario",

	// Metas e Objetivos -> "meta"
	"meta":      "meta",
	"metas":     "meta",
	"objetivo":  "meta",
	"objetivos": "meta",
	"alvo":      "meta",
	"alvos":     "meta",

	// Ideias e Projetos -> "ideia"
	"ideia":       "ideia",
	"ideias":      "ideia",
	"projeto":     "ideia",
	"projetos":    "ideia",
	"pensamento":  "ideia",
	"pensamentos": "ideia",
	"plano":       "ideia",
	"planos":      "ideia",

	// Quantificadores de Totalidade -> "todo"
	"todo":    "todo",
	"todos":   "todo",
	"toda":    "todo",
	"todas":   "todo",
	"tudo":    "todo",
	"inteiro": "todo",
	"inteira": "todo",
	"geral":   "todo",

	// E-mail e Caixa de Entrada -> "email"
	"email":            "email",
	"emails":           "email",
	"e-mail":           "email",
	"e-mails":          "email",
	"inbox":            "email",
	"correio":          "email",
	"mensagens":        "email",
	"correspondencia":  "email",
	"correspondencias": "email",
	"briefing":         "briefing",
	"briefings":        "briefing",
	"liturgia":         "liturgia",
	"liturgias":        "liturgia",
	"evangelho":        "liturgia",
	"evangelhos":       "liturgia",
	"diariamente":      "diario",
	"diaria":           "diario",
	"diarias":          "diario",
	"agende":           "agendar",
	"agendar":          "agendar",
	"programe":         "agendar",
	"programar":        "agendar",
	"envie":            "enviar",
	"enviar":           "enviar",
	"mande":            "enviar",
	"mandar":           "enviar",
	// Síntese pode compor uma consulta sem descartar a ação anterior.
	"resumo":       "resumir",
	"resumos":      "resumir",
	"resumir":      "resumir",
	"resuma":       "resumir",
	"resume":       "resumir",
	"resumindo":    "resumir",
	"sintese":      "resumir",
	"sinteses":     "resumir",
	"sintetize":    "resumir",
	"sintetizar":   "resumir",
	"sumarize":     "resumir",
	"sumarizar":    "resumir",
	"planejar":     "planejar",
	"planeje":      "planejar",
	"planeja":      "planejar",
	"planejamento": "planejar",
	"minimeta":     "planejar",
	"minimetas":    "planejar",
	"microtarefa":  "planejar",
	"microtarefas": "planejar",
	"desdobrar":    "planejar",
	"desdobre":     "planejar",
	"pdf":          "pdf",
	"exportar":     "exportar",
	"exporte":      "exportar",
	"exportacao":   "exportar",
	"setor":        "setor",
	"setores":      "setor",
	"categoria":    "setor",
	"categorias":   "setor",

	// Santo do Dia -> "santo"
	"santo":      "santo",
	"santos":     "santo",
	"santododia": "santo",
	"padroeiro":  "santo",
	"padroeira":  "santo",

	// Bíblia Católica e Vulgata -> "versiculo"
	"versiculo":  "versiculo",
	"versiculos": "versiculo",
	"versinho":   "versiculo",
	"biblia":     "versiculo",
	"biblias":    "versiculo",
	"passagem":   "versiculo",
	"vulgata":    "versiculo",

	// Liturgia das Horas -> "horas"
	"horas":            "horas",
	"laudes":           "horas",
	"vesperas":         "horas",
	"completas":        "horas",
	"oficio":           "horas",
	"liturgiadashoras": "horas",

	// Contas, Cartões, Assinaturas -> "conta"
	"conta":       "conta",
	"contas":      "conta",
	"fatura":      "conta",
	"faturas":     "conta",
	"boleto":      "conta",
	"boletos":     "conta",
	"cartao":      "conta",
	"cartoes":     "conta",
	"assinatura":  "conta",
	"assinaturas": "conta",
	"mensalidade": "conta",

	// Pagamentos / Baixas -> "pagar"
	"pagar":    "pagar",
	"paguei":   "pagar",
	"pago":     "pagar",
	"paga":     "pagar",
	"quitei":   "pagar",
	"quite":    "pagar",
	"quitado":  "pagar",
	"liquidar": "pagar",
	"baixar":   "pagar",

	// Código e Script -> "python"
	"python": "python",
	"py":     "python",
	"script": "python",
	"codigo": "python",
}

// RemoveAccents converte caracteres acentuados para equivalentes ASCII simples.
func RemoveAccents(s string) string {
	s = strings.ReplaceAll(s, "e-mail", "email")
	s = strings.ReplaceAll(s, "e-mails", "emails")
	s = strings.ReplaceAll(s, "á", "a")
	s = strings.ReplaceAll(s, "à", "a")
	s = strings.ReplaceAll(s, "ã", "a")
	s = strings.ReplaceAll(s, "â", "a")
	s = strings.ReplaceAll(s, "é", "e")
	s = strings.ReplaceAll(s, "ê", "e")
	s = strings.ReplaceAll(s, "í", "i")
	s = strings.ReplaceAll(s, "ó", "o")
	s = strings.ReplaceAll(s, "ô", "o")
	s = strings.ReplaceAll(s, "õ", "o")
	s = strings.ReplaceAll(s, "ú", "u")
	s = strings.ReplaceAll(s, "ç", "c")
	return s
}

// CanonicalizeWord retorna a forma canônica de uma palavra segundo o dicionário.
func CanonicalizeWord(w string) string {
	w = RemoveAccents(strings.ToLower(strings.TrimSpace(w)))
	if canon, ok := canonicalMap[w]; ok {
		return canon
	}
	return w
}

// TokenizeAndCanonicalize processa uma frase em tokens canônicos limpos.
func TokenizeAndCanonicalize(text string) []string {
	text = RemoveAccents(strings.ToLower(text))

	// Quebra em palavras removendo pontuação
	words := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r) || unicode.IsSymbol(r)
	})

	var result []string
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		canon := CanonicalizeWord(w)
		result = append(result, canon)
	}
	return result
}

// MatchCanonical checa se os tokens contêm a combinação canônica especificada.
func MatchCanonical(tokens []string, target string) bool {
	for _, t := range tokens {
		if t == target {
			return true
		}
	}
	return false
}

// HasPair checa se ambos os termos canônicos existem na lista de tokens.
func HasPair(tokens []string, first, second string) bool {
	hasFirst := false
	hasSecond := false
	for _, t := range tokens {
		if t == first {
			hasFirst = true
		}
		if t == second {
			hasSecond = true
		}
	}
	return hasFirst && hasSecond
}
