package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"atlas/pkg/catholic"
	"atlas/pkg/database"
	"atlas/pkg/nlu"
	"atlas/pkg/scheduler"
	"atlas/pkg/synonyms"
	"atlas/pkg/web"
)

var routineTime = regexp.MustCompile(`(?:\bas\s+|\ba\s+|\bpara\s+)?\b([0-9]{1,2})(?::([0-9]{2})|h([0-9]{2})?\b)`)
var shortRoutineTime = regexp.MustCompile(`\b(?:as|para)\s+([0-9]{1,2})\b`)

func automationOperation(text string) bool {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	tokens := synonyms.TokenizeAndCanonicalize(clean)
	if synonyms.MatchCanonical(tokens, "apagar") {
		return true
	}
	if synonyms.HasPair(tokens, "nao", "enviar") {
		return true
	}
	if synonyms.MatchCanonical(tokens, "todo") {
		for _, day := range []string{"dia", "dias", "semana", "semanas", "manha", "manhas", "segunda", "segundas", "terca", "tercas", "quarta", "quartas", "quinta", "quintas", "sexta", "sextas", "sabado", "sabados", "domingo", "domingos"} {
			if synonyms.MatchCanonical(tokens, day) {
				return true
			}
		}
	}
	if (strings.Contains(clean, "amanha") || strings.Contains(clean, " as ") || strings.Contains(clean, " para ")) && extractRoutineTime(clean) != "" {
		return true
	}
	for _, term := range []string{"todo dia", "todos os dias", "toda manha", "diariamente", "toda semana", "toda segunda", "toda terca", "toda quarta", "toda quinta", "toda sexta", "todo sabado", "todo domingo", "segunda a sexta", "agende", "agendar", "agendamento", "programe", "programar", "lembre", "lembra", "anote", "anotar", "adicione", "adicionar", "marque", "marcar", "crie", "criar", "mude", "mudar", "altere", "alterar", "troque", "trocar", "nao envie", "nao mande", "pare de", "conclui", "terminei"} {
		if strings.Contains(term, " ") && strings.Contains(clean, term) || !strings.Contains(term, " ") && synonyms.MatchCanonical(tokens, synonyms.CanonicalizeWord(term)) {
			return true
		}
	}
	return false
}

func routineAction(text string) (string, string) {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	tokens := synonyms.TokenizeAndCanonicalize(clean)
	switch {
	case strings.Contains(clean, "briefing") || strings.Contains(clean, "resumo matinal") || strings.Contains(clean, "resumo do dia"):
		return "briefing", ""
	case strings.Contains(clean, "liturgia das horas") || strings.Contains(clean, "laudes") || strings.Contains(clean, "vesperas") || strings.Contains(clean, "completas"):
		for _, hour := range []string{"laudes", "vesperas", "completas"} {
			if strings.Contains(clean, hour) {
				return "liturgia_das_horas", hour
			}
		}
		return "liturgia_das_horas", "auto"
	case strings.Contains(clean, "liturgia") || strings.Contains(clean, "evangelho"):
		return "liturgia", ""
	case synonyms.MatchCanonical(tokens, "email") || strings.Contains(clean, "caixa de entrada"):
		return "ler_emails", ""
	case strings.Contains(clean, "santo do dia"):
		return "santo_do_dia", ""
	}
	return "", ""
}

func actionRoutineName(action, term string) string {
	names := map[string]string{"briefing": "Briefing matinal", "liturgia": "Liturgia diária", "liturgia_das_horas": "Liturgia das Horas", "ler_emails": "Radar de emails", "santo_do_dia": "Santo do dia", "versiculo_biblico": "Versículo", "noticias": "Notícias"}
	name := names[action]
	if name == "" {
		return ""
	}
	if term != "" {
		name += " — " + term
	}
	return name
}

func (s *Server) saveScheduledAction(ctx context.Context, intent *nlu.IntentResult) string {
	name := actionRoutineName(intent.Acao, intent.Termo)
	if name == "" {
		return "⚠️ Essa ferramenta não pode ser programada. Nenhuma rotina criada."
	}
	if intent.Frequencia != "" && intent.Frequencia != "diario" && intent.Frequencia != "semanal" {
		return "⚠️ Informe frequência diária ou dias da semana para esse envio."
	}
	if intent.Frequencia == "semanal" && len(intent.DiasSemana) == 0 {
		return "⚠️ Quais dias da semana devo usar para esse envio?"
	}
	for _, day := range intent.DiasSemana {
		if day < 0 || day > 6 {
			return "⚠️ Dia da semana inválido."
		}
	}
	action := scheduler.ActionRoutine{Action: intent.Acao, Term: intent.Termo, Days: intent.DiasSemana}
	payload, err := json.Marshal(action)
	if err != nil {
		return "⚠️ Falha ao preparar rotina."
	}
	id, err := s.db.SaveActionRoutine(ctx, name, intent.Horario, string(payload))
	if err != nil {
		return fmt.Sprintf("⚠️ Não foi possível programar: %v", err)
	}
	frequency := "diariamente"
	if len(intent.DiasSemana) > 0 {
		frequency = fmt.Sprintf("nos dias da semana %v (0=domingo)", intent.DiasSemana)
	}
	return fmt.Sprintf("✅ Rotina [%d] %s programada %s às %s. Vou buscar e enviar o conteúdo atualizado.", id, name, frequency, intent.Horario)
}

// handleAutomation handles unambiguous daily read actions without an LLM round trip.
func (s *Server) handleAutomation(ctx context.Context, text string) (string, bool) {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	if !automationOperation(clean) {
		return "", false
	}
	action, term := routineAction(clean)
	if action == "" {
		return "", false
	}
	tokens := synonyms.TokenizeAndCanonicalize(clean)
	if synonyms.MatchCanonical(tokens, "nao") && (synonyms.MatchCanonical(tokens, "apagar") || strings.Contains(clean, "mud") || strings.Contains(clean, "alter")) {
		return "", false
	}
	cancel := synonyms.MatchCanonical(tokens, "apagar") || synonyms.HasPair(tokens, "nao", "enviar") || strings.Contains(clean, "pare de")
	change := strings.Contains(clean, "mud") || strings.Contains(clean, "alter") || strings.Contains(clean, "troqu")
	if cancel || change {
		routines, err := s.db.GetAllRoutines(ctx)
		if err != nil {
			return "⚠️ Falha ao consultar rotinas.", true
		}
		var matches []database.RotinaProativa
		for _, r := range routines {
			ra, rt := routineAction(r.Nome)
			if r.Tipo == "briefing" {
				ra = "briefing"
			}
			if r.Tipo == "action" && r.PromptCustom != nil {
				var a scheduler.ActionRoutine
				if json.Unmarshal([]byte(*r.PromptCustom), &a) == nil {
					ra, rt = a.Action, a.Term
				}
			}
			if ra == action && (term == "" || term == "auto" || term == rt) {
				matches = append(matches, r)
			}
		}
		if len(matches) == 0 {
			return "⚠️ Não encontrei uma rotina programada para esse conteúdo.", true
		}
		if change && len(matches) > 1 {
			return "Encontrei mais de uma rotina. Use /lembretes e informe qual ID deseja alterar.", true
		}
		for _, r := range matches {
			if cancel {
				if err := s.db.DeleteRoutine(ctx, r.ID); err != nil {
					return "⚠️ Falha ao cancelar rotina.", true
				}
			} else {
				hour := extractRoutineTime(clean)
				if err := s.db.UpdateRoutineTime(ctx, r.ID, hour); err != nil {
					return "Qual novo horário? Exemplo: mude a liturgia para 08:00.", true
				}
			}
		}
		if cancel {
			return "✅ Envio automático cancelado.", true
		}
		return "✅ Horário da rotina atualizado para " + extractRoutineTime(clean) + ".", true
	}
	daily := strings.Contains(clean, "todo dia") || strings.Contains(clean, "todos os dias") || strings.Contains(clean, "diariamente") || strings.Contains(clean, "toda manha")
	if !daily {
		return "", false
	}
	if synonyms.MatchCanonical(tokens, "lembre") || synonyms.MatchCanonical(tokens, "lembrar") || synonyms.MatchCanonical(tokens, "lembra") {
		return "", false
	}
	// More complex recurrence and filtered email requests retain NLU parameters.
	if strings.Contains(clean, "exceto") || strings.Contains(clean, "menos ") || strings.Contains(clean, "email") && (strings.Contains(clean, " do ") || strings.Contains(clean, " de ")) {
		return "", false
	}
	hour := extractRoutineTime(clean)
	if hour == "" {
		return "Qual horário para o envio diário? Exemplo: liturgia todo dia às 07:00.", true
	}
	return s.saveScheduledAction(ctx, &nlu.IntentResult{Acao: action, Termo: term, Horario: hour, Frequencia: "diario"}), true
}

func extractRoutineTime(text string) string {
	match := routineTime.FindStringSubmatch(text)
	if len(match) > 0 {
		minute := match[2]
		if minute == "" {
			minute = match[3]
		}
		if minute == "" {
			minute = "00"
		}
		return fmt.Sprintf("%02s:%s", match[1], minute)
	}
	match = shortRoutineTime.FindStringSubmatch(text)
	if len(match) > 0 {
		return fmt.Sprintf("%02s:00", match[1])
	}
	return ""
}

func (s *Server) executeScheduledAction(ctx context.Context, action scheduler.ActionRoutine) (string, error) {
	switch action.Action {
	case "briefing":
		return s.engine.BuildBriefing(ctx, s.today())
	case "liturgia":
		return web.FetchLiturgiaDiaria(ctx)
	case "liturgia_das_horas":
		return catholic.FetchLiturgiaDasHoras(ctx, action.Term, s.cfg.Location)
	case "santo_do_dia":
		return catholic.FetchSantoDoDia(ctx)
	case "versiculo_biblico":
		return catholic.FetchPassage(ctx, action.Term)
	case "ler_emails":
		return s.readEmails(ctx, action.Term)
	case "noticias":
		result := s.executeAppAction(ctx, &nlu.IntentResult{Acao: action.Action, Termo: action.Term})
		if strings.HasPrefix(result, "⚠️") {
			return "", fmt.Errorf("%s", result)
		}
		return result, nil
	default:
		return "", fmt.Errorf("ação programada desconhecida: %s", action.Action)
	}
}

func (s *Server) today() string { return time.Now().In(s.cfg.Location).Format("2006-01-02") }
