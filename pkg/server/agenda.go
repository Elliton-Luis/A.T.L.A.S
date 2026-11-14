package server

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
	"atlas/pkg/schedule"
	"atlas/pkg/synonyms"
)

var sourceWeekday = regexp.MustCompile(`\b(?:de|da|na)?\s*(domingo|segunda|terca|quarta|quinta|sexta|sabado)(?:-feira)?\b`)
var sourceClock = regexp.MustCompile(`\b(?:das|de|as)\s*([0-9]{1,2})(?:(?::|h)([0-9]{2})?)?\b`)
var explicitEventID = regexp.MustCompile(`\b(?:id|compromisso)\s*#?\s*([0-9]+)\b`)

func eventSaveFailure(err error) string {
	if errors.Is(err, database.ErrEventExists) {
		return "⚠️ " + err.Error()
	}
	return "⚠️ Não foi possível salvar o compromisso. Nenhum registro foi alterado."
}

func equalWeekday(a, b *int) bool { return a != nil && b != nil && *a == *b }

func (s *Server) agendaView(ctx context.Context, date string) string {
	var events []database.Evento
	var err error
	title := "Agenda — todos os compromissos ativos"
	if date == "" {
		events, err = s.db.ActiveEvents(ctx)
	} else {
		day, e := s.taskDate(date)
		if e != nil {
			return "⚠️ " + e.Error()
		}
		var byDay map[string][]database.Evento
		byDay, err = s.db.EventsForRange(ctx, day, 1, s.cfg.Location)
		events = byDay[day]
		title = "Agenda de " + day
	}
	if err != nil {
		return "⚠️ Não foi possível consultar a agenda."
	}
	return schedule.Format(events, title, s.cfg.Location)
}

func eventEdit(intent *nlu.IntentResult) database.EventEdit {
	id := intent.ID
	if id == 0 {
		id = intent.SubstituirEventoID
	}
	var duration *int
	if intent.DuracaoMin > 0 {
		duration = &intent.DuracaoMin
	}
	return database.EventEdit{ID: id, Descricao: intent.Descricao, Horario: intent.Horario, HorarioOficial: intent.HorarioOficial, Data: intent.Data, Frequencia: intent.Frequencia, DiaSemana: intent.DiaSemana, DiaMes: intent.DiaMes, DiaOrigem: intent.DiaOrigem, HorarioOrigem: intent.HorarioOrigem, Deslocamento: intent.EstimativaDeslocamentoMin, Preparo: intent.EstimativaPreparoMin, Duracao: duration}
}

func (s *Server) createdEventsReply(ctx context.Context, ids []int64) string {
	all, err := s.db.ActiveEvents(ctx)
	if err != nil {
		return "✅ Compromisso salvo. Consulte /agenda."
	}
	wanted := map[int64]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	var created []database.Evento
	for _, event := range all {
		if wanted[event.ID] {
			created = append(created, event)
		}
	}
	return "✅ Compromisso registrado.\n\n" + schedule.Format(created, "Dias e horários registrados", s.cfg.Location)
}

func adjustmentRequest(text string) bool {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	for _, word := range []string{"mude", "mudar", "altere", "alterar", "ajuste", "ajustar", "corrija", "corrigir", "corrigindo", "errei", "errado", "troque", "trocar", "atualize", "atualizar", "ao inves", "em vez"} {
		if strings.Contains(clean, word) {
			return true
		}
	}
	return false
}

func (s *Server) alterEvents(ctx context.Context, intent *nlu.IntentResult, original string) string {
	edits := intent.Alteracoes
	if len(edits) == 0 {
		if len(intent.DiasSemana) > 1 {
			return "Cada horário deve apontar para uma linha da atividade. Consulte /agenda e indique quais dias/IDs deseja alterar; os demais serão preservados."
		}
		edit := eventEdit(intent)
		if len(intent.DiasSemana) == 1 {
			day := intent.DiasSemana[0]
			edit.DiaSemana = &day
		}
		edits = []database.EventEdit{edit}
	}
	events, err := s.db.ActiveEvents(ctx)
	if err != nil {
		return "⚠️ Não foi possível consultar os compromissos."
	}
	for i := range edits {
		edit := &edits[i]
		if !edit.HasChanges() {
			return s.agendaView(ctx, "") + "\n\nQual linha deseja alterar e qual é o novo valor?"
		}
		if len(edits) == 1 && original != "" {
			before := strings.SplitN(synonyms.RemoveAccents(strings.ToLower(original)), " para ", 2)[0]
			if match := explicitEventID.FindStringSubmatch(before); len(match) > 1 {
				edit.ID, _ = strconv.ParseInt(match[1], 10, 64)
			}
			if match := sourceWeekday.FindStringSubmatch(before); len(match) > 1 {
				for day, label := range []string{"domingo", "segunda", "terca", "quarta", "quinta", "sexta", "sabado"} {
					if match[1] == label {
						value := day
						edit.DiaOrigem = &value
						break
					}
				}
			}
			if match := sourceClock.FindStringSubmatch(before); len(match) > 1 && (strings.Contains(synonyms.RemoveAccents(strings.ToLower(original)), " para ") || strings.Contains(before, "das ")) {
				hour, _ := strconv.Atoi(match[1])
				minute, _ := strconv.Atoi(match[2])
				edit.HorarioOrigem = fmt.Sprintf("%02d:%02d", hour, minute)
			}
		}
		var byID *database.Evento
		for j := range events {
			if events[j].ID == edit.ID {
				byID = &events[j]
				break
			}
		}
		if byID != nil && intent.Termo != "" && !strings.Contains(synonyms.RemoveAccents(strings.ToLower(byID.Descricao)), synonyms.RemoveAccents(strings.ToLower(intent.Termo))) {
			byID = nil
			edit.ID = 0
		}
		if byID != nil && len(edits) == 1 && original != "" && !explicitEventID.MatchString(synonyms.RemoveAccents(strings.ToLower(original))) {
			count := 0
			key := synonyms.RemoveAccents(strings.ToLower(byID.Descricao))
			for _, candidate := range events {
				if synonyms.RemoveAccents(strings.ToLower(candidate.Descricao)) != key {
					continue
				}
				if edit.DiaOrigem != nil && (candidate.DiaSemana == nil || *candidate.DiaSemana != *edit.DiaOrigem) {
					continue
				}
				if edit.HorarioOrigem != "" && candidate.HorarioTime != edit.HorarioOrigem {
					continue
				}
				count++
			}
			if count > 1 {
				return s.agendaView(ctx, "") + "\n\nA atividade tem mais de uma linha possível. Diga o dia, horário original ou ID; nenhuma linha foi alterada."
			}
		}
		if byID != nil && (edit.DiaOrigem == nil || byID.DiaSemana != nil && *byID.DiaSemana == *edit.DiaOrigem) && (edit.HorarioOrigem == "" || edit.HorarioOrigem == byID.HorarioTime) {
			continue
		}
		term := intent.Termo
		if term == "" && byID != nil {
			term = byID.Descricao
		}
		if term == "" {
			term = intent.Descricao
		}
		key := synonyms.RemoveAccents(strings.ToLower(strings.TrimSpace(term)))
		var matches []database.Evento
		for _, event := range events {
			name := synonyms.RemoveAccents(strings.ToLower(event.Descricao))
			if key == "" || !strings.Contains(name, key) {
				continue
			}
			if edit.DiaOrigem != nil && (event.DiaSemana == nil || *event.DiaSemana != *edit.DiaOrigem) {
				continue
			}
			if edit.HorarioOrigem != "" && event.HorarioTime != edit.HorarioOrigem {
				continue
			}
			matches = append(matches, event)
		}
		if len(matches) != 1 {
			return s.agendaView(ctx, "") + "\n\nHá mais de uma linha possível ou o alvo não foi encontrado. Informe o dia, horário original ou ID; nenhuma alteração foi aplicada."
		}
		edit.ID = matches[0].ID
	}
	updated, err := s.db.ApplyEventEdits(ctx, edits, s.cfg.Location)
	if err != nil {
		return fmt.Sprintf("⚠️ Nenhuma alteração foi aplicada: %v", err)
	}
	return "✅ Atualização concluída; os outros dias e horários foram preservados.\n\n" + schedule.Format(updated, "Linhas atualizadas", s.cfg.Location)
}

func (s *Server) eventCommand(ctx context.Context, argument string) string {
	parts := strings.Fields(argument)
	if len(parts) != 3 || parts[1] != "horario" {
		return "Use /compromisso <ID> horario HH:MM ou diga: mude a Aula de sexta para 08:30. /agenda mostra todas as linhas."
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "⚠️ Informe um ID de /agenda."
	}
	return s.alterEvents(ctx, &nlu.IntentResult{ID: id, Horario: parts[2]}, "")
}
