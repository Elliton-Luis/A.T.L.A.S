package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"atlas/pkg/crawler"
	"atlas/pkg/database"
	"atlas/pkg/nlu"
)

// Native tools remain the execution source; NLU must reach the same integrations.
func (s *Server) executeNativeAction(ctx context.Context, intent *nlu.IntentResult) (string, bool) {
	switch intent.Acao {
	case "executar_terminal":
		if strings.TrimSpace(intent.Termo) == "" {
			return "Qual comando explícito deseja executar na bridge Sonar?", true
		}
		result, err := s.sonarClient.Execute(ctx, intent.Termo, "")
		if err != nil {
			return fmt.Sprintf("⚠️ Falha na bridge Sonar: %v", err), true
		}
		if !result.OK || result.ExitCode != 0 {
			return fmt.Sprintf("⚠️ Comando terminou com código %d.\n%s\n%s", result.ExitCode, result.Output, result.Error), true
		}
		if strings.TrimSpace(result.Output) == "" {
			return "✅ Comando executado sem saída de texto.", true
		}
		return "*Saída da bridge Sonar*\n```text\n" + result.Output + "\n```", true
	case "rastrear_site":
		link := strings.TrimSpace(intent.URL)
		if link == "" {
			link = strings.TrimSpace(intent.Termo)
		}
		if link == "" {
			return "Qual site deseja rastrear?", true
		}
		if !strings.HasPrefix(link, "https://") && !strings.HasPrefix(link, "http://") {
			link = "https://" + link
		}
		result, err := s.crawler.Crawl(ctx, link, crawler.DefaultOptions())
		if err != nil {
			return fmt.Sprintf("⚠️ Falha no rastreamento: %v", err), true
		}
		if len(result.Pages) == 0 {
			return "⚠️ Nenhuma página retornou conteúdo no rastreamento.", true
		}
		var out strings.Builder
		fmt.Fprintf(&out, "*Rastreamento concluído — %d página(s)*\n", len(result.Pages))
		for _, page := range result.Pages {
			fmt.Fprintf(&out, "• %s\n%s\n", page.Title, page.URL)
		}
		return out.String(), true
	case "deliberar":
		if strings.TrimSpace(intent.Termo) == "" {
			return "Sobre qual tema deseja reunir o conselho?", true
		}
		result, err := s.swarm.Deliberate(ctx, intent.Termo)
		if err != nil {
			return fmt.Sprintf("⚠️ Falha no conselho: %v", err), true
		}
		if result.TotalTokens > 0 {
			_ = s.db.RecordTokenUsage(ctx, "swarm", "conselho", 0, 0, result.TotalTokens)
		}
		return result.FormatTelegramHUD(), true
	case "abrir_painel":
		return "*Painel ATLAS*\n" + s.getPublicURL() + "/hud?token=" + s.cfg.CalendarToken, true
	case "listar_metas":
		goals, err := s.db.GetActiveGoals(ctx, 100)
		if err != nil {
			return "⚠️ Não foi possível consultar metas.", true
		}
		if len(goals) == 0 {
			return "Nenhuma meta ativa cadastrada.", true
		}
		var out strings.Builder
		out.WriteString("*Metas e objetivos*\n")
		for _, goal := range goals {
			fmt.Fprintf(&out, "• %s — %d%%", goal.Titulo, goal.ProgressoPct)
			if goal.Prazo != nil {
				fmt.Fprintf(&out, " | Prazo: %s", *goal.Prazo)
			}
			out.WriteByte('\n')
		}
		return out.String(), true
	case "listar_ideias":
		ideas, err := s.db.GetRecentIdeas(ctx, 10)
		if err != nil {
			return "⚠️ Não foi possível consultar ideias.", true
		}
		if len(ideas) == 0 {
			return "Nenhuma ideia registrada ainda.", true
		}
		var out strings.Builder
		out.WriteString("*Ideias e projetos*\n")
		for _, idea := range ideas {
			fmt.Fprintf(&out, "• [%d] %s — /ideia %d\n", idea.ID, idea.Titulo, idea.ID)
		}
		return out.String(), true
	case "ver_ideia":
		var idea *database.Ideia
		var err error
		if intent.ID > 0 {
			idea, err = s.db.GetIdeiaByID(ctx, intent.ID)
		} else {
			idea, err = s.db.GetLatestActiveIdeia(ctx)
		}
		if err != nil || idea == nil {
			return "⚠️ Ideia não encontrada. Consulte /ideias.", true
		}
		return fmt.Sprintf("*Ideia #%d — %s*\n%s\n\n%s", idea.ID, idea.Titulo, idea.SumaExecutiva, idea.Conteudo), true
	case "ver_tokens":
		now := time.Now().In(s.cfg.Location)
		stats, err := s.db.GetTokenStats(ctx, now.Format("2006-01-02"), now.Format("2006-01"))
		if err != nil {
			return "⚠️ Não foi possível consultar consumo.", true
		}
		return fmt.Sprintf("*Consumo registrado*\nHoje: %d tokens\nMês: %d tokens\nGroq: %d\nNVIDIA: %d", stats.TotalHoje, stats.TotalMes, stats.TotalGroq, stats.TotalNv), true
	}
	return "", false
}
