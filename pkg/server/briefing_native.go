package server

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
	"atlas/pkg/synonyms"
)

// Known unparameterized sources do not need an LLM just to select tools.
// Topic searches, quotations, exclusions and edits retain the richer NLU path.
func parseSimpleBriefing(text string) (*nlu.IntentResult, bool) {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	if !strings.Contains(clean, "briefing") && !strings.Contains(clean, "resumo matinal") {
		return nil, false
	}
	tokens := synonyms.TokenizeAndCanonicalize(clean)
	if synonyms.MatchCanonical(tokens, "semana") || strings.Contains(clean, "semanal") || strings.Contains(clean, "mensal") || strings.Contains(clean, "a cada ") {
		return nil, false
	}
	if synonyms.MatchCanonical(tokens, "apagar") || synonyms.MatchCanonical(tokens, "nao") {
		return nil, false
	}
	for _, complex := range []string{"noticia", "versiculo", "biblia", "pensamento", "ideia", "email", "e-mail", "inbox", "clima", "previsao", "cotacao", "temperatura", "exceto", "menos ", "segunda", "terca", "quarta", "quinta", "sexta", "sabado", "domingo", "retire", "remova", "mude", "altere", "troque", "alterar", "remover", "liturgia das horas", "laudes", "vesperas", "completas"} {
		if strings.Contains(clean, complex) {
			return nil, false
		}
	}
	requested := false
	for _, word := range []string{"criar", "crie", "configure", "configurar", "quero", "mande", "mandar", "envie", "enviar", "adicionar", "adicione", "inclua", "incluir", "coloque", "deve conter", "deve ter"} {
		if strings.Contains(clean, word) {
			requested = true
			break
		}
	}
	if !requested {
		return nil, false
	}
	operation := "configurar_briefing"
	for _, word := range []string{"adicione", "adicionar", "inclua", "incluir", "acrescente"} {
		if strings.Contains(clean, word) {
			operation = "adicionar_briefing"
			break
		}
	}
	type candidate struct {
		action   string
		aliases  []string
		position int
	}
	candidates := []candidate{
		{action: "liturgia", aliases: []string{"liturgia", "evangelho", "leituras do dia"}},
		{action: "santo_do_dia", aliases: []string{"santo do dia", "santo de hoje", "santo"}},
		{action: "agenda_completa", aliases: []string{"tudo o que eu tenho que fazer", "tudo que eu tenho que fazer", "tudo o que tenho que fazer", "tudo que tenho que fazer", "tudo o que vou fazer", "tudo que vou fazer", "tudo do meu dia"}},
		{action: "listar_agenda", aliases: []string{"compromissos", "agenda"}},
		{action: "listar_tarefas", aliases: []string{"tarefas", "checklist", "metas diarias"}},
		{action: "listar_contas", aliases: []string{"contas", "faturas", "vencimentos"}},
		{action: "listar_lembretes", aliases: []string{"lembretes"}},
		{action: "listar_automacoes", aliases: []string{"automacoes"}},
	}
	var selected []candidate
	complete := false
	for _, item := range candidates {
		item.position = -1
		for _, alias := range item.aliases {
			if at := strings.Index(clean, alias); at >= 0 && (item.position < 0 || at < item.position) {
				item.position = at
			}
		}
		if item.position >= 0 {
			selected = append(selected, item)
			if item.action == "agenda_completa" {
				complete = true
			}
		}
	}
	if len(selected) == 0 {
		return nil, false
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].position < selected[j].position })
	intent := &nlu.IntentResult{Tipo: "acao", Acao: operation, Horario: extractRoutineTime(clean)}
	for _, item := range selected {
		if complete && (item.action == "listar_agenda" || item.action == "listar_tarefas" || item.action == "listar_contas" || item.action == "listar_lembretes" || item.action == "listar_automacoes") {
			continue
		}
		intent.Acoes = append(intent.Acoes, nlu.ActionStep{Acao: item.action})
	}
	return intent, true
}

func wantsDailyBriefing(text string) bool {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	for _, phrase := range []string{"todo dia", "todos os dias", "diariamente", "toda manha"} {
		if strings.Contains(clean, phrase) {
			return true
		}
	}
	return false
}

func (s *Server) handleSimpleBriefing(ctx context.Context, chatID int64, text string) (string, bool) {
	intent, ok := parseSimpleBriefing(text)
	if !ok {
		return "", false
	}
	if wantsDailyBriefing(text) && intent.Horario == "" {
		body, err := json.Marshal(intent)
		if err != nil {
			return "⚠️ Não foi possível guardar a composição.", true
		}
		if err := s.db.SavePendingConfirmation(ctx, chatID, "briefing_horario", 0, string(body)); err != nil {
			return "⚠️ Não foi possível guardar a composição.", true
		}
		return "Qual horário para esse briefing diário? A composição foi guardada; pode responder ‘sete horas da manhã’ ou ‘07:00’.", true
	}
	return s.configureBriefing(ctx, intent), true
}

func (s *Server) handleBriefingHour(ctx context.Context, chatID int64, text string, pending *database.ConfirmacaoPendente) (bool, string) {
	clean := strings.Trim(synonyms.RemoveAccents(strings.ToLower(text)), " .!?")
	if clean == "cancele" || clean == "cancelar" || clean == "cancela" {
		_ = s.db.DeletePendingConfirmation(ctx, chatID)
		return true, "Configuração do briefing cancelada."
	}
	hour := extractRoutineTime(text)
	if hour == "" {
		hour = extractRoutineTime("as " + text)
	}
	if hour == "" {
		return false, ""
	}
	var intent nlu.IntentResult
	if json.Unmarshal([]byte(pending.NovoEventoJSON), &intent) != nil {
		return true, "⚠️ Não foi possível recuperar a composição."
	}
	intent.Horario = hour
	reply := s.configureBriefing(ctx, &intent)
	if strings.HasPrefix(reply, "✅") {
		_ = s.db.DeletePendingConfirmation(ctx, chatID)
	}
	return true, reply
}
