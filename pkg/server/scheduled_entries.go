package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
	"atlas/pkg/scheduler"
)

func (s *Server) scheduledList(ctx context.Context, kind string) string {
	routines, err := s.db.GetAllRoutines(ctx)
	if err != nil {
		return "⚠️ Não foi possível consultar os envios."
	}
	var out strings.Builder
	for _, category := range []string{"lembrete", "automacao"} {
		if kind != "" && kind != category {
			continue
		}
		if category == "lembrete" {
			out.WriteString("*Lembretes — mensagens fixas*\n")
		} else {
			out.WriteString("*Automações — consultas e ações programadas*\n")
		}
		count := 0
		for _, r := range routines {
			if scheduler.RoutineKind(r) != category {
				continue
			}
			count++
			state := "ativo"
			if r.Ativo == 0 {
				state = "pausado"
			}
			description := r.Nome
			if category == "lembrete" {
				if reminder, err := scheduler.DecodeReminder(r); err == nil {
					description = reminder.Message
				}
			}
			fmt.Fprintf(&out, "• [%d] %s | %s | %s\n", r.ID, r.Horario, description, state)
		}
		if count == 0 {
			out.WriteString("Nenhum item cadastrado.\n")
		}
		if category == "lembrete" {
			out.WriteString("Alterar: /lembrete <ID> horario HH:MM\nExcluir: /lembrete <ID> excluir\n\n")
		} else {
			out.WriteString("Alterar: /automacao <ID> horario HH:MM\nExcluir: /automacao <ID> excluir\n\n")
		}
	}
	return strings.TrimSpace(out.String())
}

func (s *Server) createReminder(ctx context.Context, intent *nlu.IntentResult) string {
	if intent.Frequencia != "" && intent.Frequencia != "diario" && intent.Frequencia != "semanal" {
		return "Informe um lembrete diário ou os dias da semana desejados."
	}
	if strings.TrimSpace(intent.Mensagem) == "" {
		return "Qual mensagem fixa deseja receber? Para buscar conteúdo atualizado, crie uma automação."
	}
	for _, day := range intent.DiasSemana {
		if day < 0 || day > 6 {
			return "⚠️ Dia da semana inválido."
		}
	}
	days := intent.DiasSemana
	if len(days) == 0 && intent.DiaSemana != nil {
		days = []int{*intent.DiaSemana}
	}
	for _, day := range days {
		if day < 0 || day > 6 {
			return "⚠️ Dia da semana inválido."
		}
	}
	if intent.Frequencia == "semanal" && len(days) == 0 {
		return "Em quais dias da semana devo enviar esse lembrete?"
	}
	payload, err := json.Marshal(scheduler.Reminder{Message: intent.Mensagem, Days: days})
	if err != nil {
		return "⚠️ Falha ao preparar lembrete."
	}
	name := []rune(intent.Mensagem)
	if len(name) > 40 {
		name = name[:40]
	}
	body := string(payload)
	id, err := s.db.InsertRoutine(ctx, string(name), intent.Horario, "lembrete", &body)
	if err != nil {
		return "⚠️ Informe horário válido para o lembrete, como 09:00."
	}
	return fmt.Sprintf("✅ Lembrete [%d] salvo às %s: %s\nEle envia essa mensagem fixa; não executa ferramentas.", id, intent.Horario, intent.Mensagem)
}

func (s *Server) manageScheduled(ctx context.Context, kind, operation string, id int64, term, value string) string {
	if kind != "lembrete" && kind != "automacao" {
		return "Indique a categoria: lembrete (mensagem fixa) ou automação (ação programada). Nenhuma alteração foi feita."
	}
	routines, err := s.db.GetAllRoutines(ctx)
	if err != nil {
		return "⚠️ Não foi possível consultar o item."
	}
	var matches []database.RotinaProativa
	for _, r := range routines {
		if id > 0 && r.ID == id || id == 0 && term != "" && strings.EqualFold(r.Nome, strings.TrimSpace(term)) {
			matches = append(matches, r)
		}
	}
	if len(matches) != 1 {
		return "Identifique um item de /lembretes ou /automacoes pelo ID. Nenhuma alteração foi feita."
	}
	r := matches[0]
	actual := scheduler.RoutineKind(r)
	if kind != "" && kind != actual {
		return fmt.Sprintf("⚠️ O ID %d é %s, não %s. Use /%s %d %s.", r.ID, actual, kind, actual, r.ID, operation)
	}
	switch operation {
	case "horario":
		err = s.db.UpdateRoutineTime(ctx, r.ID, value)
	case "excluir":
		err = s.db.DeleteRoutine(ctx, r.ID)
	case "pausar":
		if r.Ativo != 0 {
			_, err = s.db.ToggleRoutine(ctx, r.ID)
		}
	case "ativar":
		if r.Ativo == 0 {
			_, err = s.db.ToggleRoutine(ctx, r.ID)
		}
	case "toggle":
		_, err = s.db.ToggleRoutine(ctx, r.ID)
	default:
		return "Use horario HH:MM, pausar, ativar ou excluir."
	}
	if err != nil {
		return "⚠️ Não foi possível alterar o item: " + err.Error()
	}
	return fmt.Sprintf("✅ %s [%d]: %s concluído.", actual, r.ID, operation)
}

func (s *Server) scheduledCommand(ctx context.Context, kind, argument string) string {
	parts := strings.Fields(argument)
	if len(parts) == 0 {
		return s.scheduledList(ctx, kind)
	}
	if kind == "lembrete" && parts[0] == "criar" {
		if len(parts) < 3 {
			return "Use /lembrete criar HH:MM mensagem fixa."
		}
		return s.createReminder(ctx, &nlu.IntentResult{Horario: parts[1], Mensagem: strings.Join(parts[2:], " ")})
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || len(parts) < 2 {
		return "Use /" + kind + " <ID> horario HH:MM, pausar, ativar ou excluir."
	}
	value := ""
	if len(parts) > 2 {
		value = parts[2]
	}
	return s.manageScheduled(ctx, kind, parts[1], id, "", value)
}

func (s *Server) scheduledIDs(ctx context.Context, kind string) ([]int64, error) {
	routines, err := s.db.GetAllRoutines(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range routines {
		if scheduler.RoutineKind(r) == kind {
			ids = append(ids, r.ID)
		}
	}
	return ids, nil
}

func (s *Server) requestClearScheduled(ctx context.Context, chatID int64, kind string) string {
	ids, err := s.scheduledIDs(ctx, kind)
	if err != nil {
		return "⚠️ Não foi possível consultar os itens."
	}
	if len(ids) == 0 {
		return "Não há itens dessa categoria para excluir."
	}
	body, _ := json.Marshal(ids)
	category := "limpar_lembretes"
	if kind == "automacao" {
		category = "limpar_automacoes"
	}
	if err := s.db.SavePendingConfirmation(ctx, chatID, category, 0, string(body)); err != nil {
		return "⚠️ Não foi possível guardar a confirmação."
	}
	return fmt.Sprintf("Excluir %d item(ns) de %s? As outras categorias serão preservadas. Responda Sim ou Não.", len(ids), kind)
}

func (s *Server) clearScheduledConfirmation(ctx context.Context, kind, payload string) string {
	var ids []int64
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &ids); err != nil {
			return "⚠️ Confirmação inválida. Nenhum item foi excluído."
		}
	} else {
		var err error
		ids, err = s.scheduledIDs(ctx, kind)
		if err != nil {
			return "⚠️ Falha ao consultar os itens."
		}
	}
	// Even old or stale confirmations cannot delete an entry of another kind.
	current, err := s.scheduledIDs(ctx, kind)
	if err != nil {
		return "⚠️ Falha ao consultar os itens."
	}
	allowed := map[int64]bool{}
	for _, id := range current {
		allowed[id] = true
	}
	var selected []int64
	for _, id := range ids {
		if allowed[id] {
			selected = append(selected, id)
		}
	}
	if err := s.db.DeleteRoutineIDs(ctx, selected); err != nil {
		return "⚠️ Falha ao excluir os itens."
	}
	return fmt.Sprintf("✅ %d item(ns) de %s excluído(s); as outras categorias foram preservadas.", len(selected), kind)
}
