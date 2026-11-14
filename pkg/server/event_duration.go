package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"atlas/pkg/database"
	"atlas/pkg/nlu"
	"atlas/pkg/schedule"
	"atlas/pkg/synonyms"
)

func (s *Server) requestEventDuration(ctx context.Context, chatID int64, intent *nlu.IntentResult) string {
	body, err := json.Marshal(intent)
	if err != nil {
		return "⚠️ Não foi possível guardar o compromisso."
	}
	if err := s.db.SavePendingConfirmation(ctx, chatID, "duracao_evento", 0, string(body)); err != nil {
		return "⚠️ Não foi possível guardar o compromisso."
	}
	return fmt.Sprintf("Quanto tempo vai durar *%s*? Pode dizer 90 minutos, 2 horas ou 2-3 horas. Em intervalos, considero o maior tempo. O compromisso ainda não foi criado.", intent.Descricao)
}

func (s *Server) handleEventDuration(ctx context.Context, chatID int64, text string, pending *database.ConfirmacaoPendente) (bool, string) {
	clean := strings.Trim(synonyms.RemoveAccents(strings.ToLower(text)), " .!?")
	if clean == "cancelar" || clean == "cancele" || clean == "cancela" || clean == "nao" {
		_ = s.db.DeletePendingConfirmation(ctx, chatID)
		return true, "Cadastro cancelado. Nenhum compromisso foi criado."
	}
	minutes, found, err := schedule.ExtractDuration(text, "", true)
	if !found {
		if value, e := strconv.Atoi(clean); e == nil {
			minutes = value
			found = true
		}
	}
	if !found {
		return false, ""
	}
	if err != nil || minutes < 1 || minutes > 366*1440 {
		return true, "Informe uma duração positiva, por exemplo: 2-3 horas."
	}
	var intent nlu.IntentResult
	if err := json.Unmarshal([]byte(pending.NovoEventoJSON), &intent); err != nil {
		return true, "⚠️ Não foi possível recuperar o compromisso."
	}
	intent.DuracaoMin = minutes
	reply := s.createEventoFromIntent(ctx, &intent)
	if !strings.HasPrefix(reply, "⚠️") && !strings.HasPrefix(reply, "Qual") && !strings.HasPrefix(reply, "Quanto") {
		_ = s.db.DeletePendingConfirmation(ctx, chatID)
	}
	return true, reply
}
