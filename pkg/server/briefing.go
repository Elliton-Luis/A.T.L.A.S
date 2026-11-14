package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"atlas/pkg/catholic"
	"atlas/pkg/nlu"
	"atlas/pkg/scheduler"
	"atlas/pkg/synonyms"
	"atlas/pkg/web"
)

var briefingTitles = map[string]string{
	"agenda_completa": "Agenda, checklist, rotinas, metas e contas", "listar_agenda": "Compromissos do dia",
	"liturgia": "Liturgia do dia", "santo_do_dia": "Santo do dia", "liturgia_das_horas": "Liturgia das Horas",
	"versiculo_biblico": "Versículo", "pensamento": "Meu pensamento", "ideia": "Pensamento registrado",
	"noticias_resumo": "Notícias", "resumir_emails": "Resumo dos e-mails", "listar_contas": "Contas",
	"listar_lembretes": "Rotinas", "listar_tarefas": "Checklist diário",
}

func isBriefingConfiguration(text string) bool {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	if !strings.Contains(clean, "briefing") {
		return false
	}
	if strings.Contains(clean, "tema") || strings.Contains(clean, "pensamento") || strings.Contains(clean, "versiculo") || strings.Contains(clean, "noticia") || strings.Contains(clean, "ordem") {
		return true
	}
	for _, term := range []string{"configure", "configurar", "configuracao", "adicione", "adicionar", "inclua", "incluir", "coloque", "retire", "remova", "remover", "deve ter", "deve conter", "quero", " com ", "itens", "composicao"} {
		if strings.Contains(clean, term) {
			return true
		}
	}
	return false
}

func (s *Server) briefingItems(ctx context.Context) ([]scheduler.ActionRoutine, error) {
	body, err := s.db.GetBriefingConfig(ctx)
	if err != nil {
		return nil, err
	}
	if body == "" {
		return []scheduler.ActionRoutine{{Action: "agenda_completa", Title: briefingTitles["agenda_completa"]}}, nil
	}
	var items []scheduler.ActionRoutine
	err = json.Unmarshal([]byte(body), &items)
	return items, err
}

func (s *Server) configureBriefing(ctx context.Context, intent *nlu.IntentResult) string {
	items, err := s.briefingItems(ctx)
	if err != nil {
		return "⚠️ Não foi possível consultar a composição atual."
	}
	replaceAt := -1
	switch intent.Acao {
	case "configurar_briefing":
		items = nil
	case "adicionar_briefing":
	case "remover_briefing", "alterar_briefing":
		term := synonyms.RemoveAccents(strings.ToLower(strings.TrimSpace(intent.Termo)))
		index, parseErr := strconv.Atoi(term)
		if intent.ID > 0 {
			index = int(intent.ID)
			parseErr = nil
		}
		var matches []int
		for i, item := range items {
			label := synonyms.RemoveAccents(strings.ToLower(item.Title + " " + item.Term + " " + item.Action))
			if parseErr == nil && i+1 == index || parseErr != nil && term != "" && strings.Contains(label, term) {
				matches = append(matches, i)
			}
		}
		if len(matches) != 1 {
			return "Qual item deseja remover? Use /briefing config para ver os números."
		}
		if intent.Acao == "alterar_briefing" {
			if len(intent.Acoes) != 1 {
				return "Descreva um novo conteúdo para substituir esse item do briefing."
			}
			replaceAt = matches[0]
		} else {
			items = append(items[:matches[0]], items[matches[0]+1:]...)
		}
	}
	for _, step := range intent.Acoes {
		title, ok := briefingTitles[step.Acao]
		if !ok {
			return fmt.Sprintf("⚠️ Item de briefing não reconhecido: %s. Composição mantida.", step.Acao)
		}
		term := strings.TrimSpace(step.Termo)
		if step.Acao == "pensamento" && term == "" {
			return "Qual pensamento deseja guardar no briefing? Envie o texto."
		}
		if step.Acao == "noticias_resumo" && term == "" {
			return "Sobre qual tema deseja receber notícias no briefing?"
		}
		if step.Acao == "versiculo_biblico" {
			if _, _, _, _, err := catholic.ParseReference(term); err != nil {
				return "Informe uma referência bíblica para o tema desejado, como Provérbios 3:5-6."
			}
		}
		if step.Titulo != "" {
			title = step.Titulo
		} else if term != "" && step.Acao != "pensamento" {
			title += " — " + term
		}
		item := scheduler.ActionRoutine{Action: step.Acao, Term: term, Title: title}
		if replaceAt >= 0 {
			items[replaceAt] = item
			continue
		}
		duplicate := false
		for _, previous := range items {
			if previous.Action == item.Action && previous.Term == item.Term {
				duplicate = true
				break
			}
		}
		if !duplicate {
			items = append(items, item)
		}
	}
	if len(items) == 0 || len(items) > 12 {
		return "Escolha de 1 a 12 itens para o briefing. Composição mantida."
	}
	body, err := json.Marshal(items)
	if err != nil {
		return "⚠️ Não foi possível preparar o briefing."
	}
	if err := s.db.SaveBriefingConfig(ctx, string(body), intent.Horario); err != nil {
		return fmt.Sprintf("⚠️ Não foi possível salvar o briefing: %v", err)
	}
	return "✅ Composição do briefing salva.\n\n" + s.showBriefingConfig(ctx)
}

func (s *Server) showBriefingConfig(ctx context.Context) string {
	items, err := s.briefingItems(ctx)
	if err != nil {
		return "⚠️ Não foi possível consultar a composição do briefing."
	}
	var out strings.Builder
	out.WriteString("*Meu briefing diário*\n")
	for i, item := range items {
		fmt.Fprintf(&out, "%d. %s\n", i+1, item.Title)
		if item.Action == "pensamento" {
			fmt.Fprintf(&out, "   %s\n", item.Term)
		}
	}
	routines, err := s.db.GetActiveRoutines(ctx)
	if err == nil {
		for _, r := range routines {
			if r.Tipo == "briefing" || r.Tipo == "action" && r.Nome == "Briefing matinal" {
				fmt.Fprintf(&out, "\nEnvio programado: %s\n", r.Horario)
				break
			}
		}
	}
	out.WriteString("\n/briefing — receber agora\n/briefing horario 07:00 — definir envio diário\n/briefing adicionar notícias sobre tecnologia\n/briefing remover 2")
	return out.String()
}

func (s *Server) configureBriefingText(ctx context.Context, operation, text string) string {
	if strings.TrimSpace(text) == "" {
		return "Descreva os itens, por exemplo: configure meu briefing com liturgia, santo do dia, um versículo sobre confiança e todos os compromissos."
	}
	history, _ := s.db.GetRecentHistory(ctx, s.cfg.HistoryLimit)
	memories, _ := s.db.GetAllUserMemories(ctx)
	idea, _ := s.db.GetLatestActiveIdeia(ctx)
	items, _ := s.briefingIntentContext(ctx)
	intent, err := s.nluClient.ParseIntent(ctx, operation+" no meu briefing: "+text, time.Now().In(s.cfg.Location).Format("2006-01-02 15:04:05"), history, memories, idea, nil, nlu.IntentContext{Briefing: items})
	if err != nil {
		return "⚠️ Não foi possível interpretar a composição. Tente novamente."
	}
	if intent.Acao != "configurar_briefing" && intent.Acao != "adicionar_briefing" {
		return "Descreva os itens que deseja incluir no briefing."
	}
	if operation == "Adicione" {
		intent.Acao = "adicionar_briefing"
	} else {
		intent.Acao = "configurar_briefing"
	}
	return s.configureBriefing(ctx, intent)
}

func (s *Server) briefingIntentContext(ctx context.Context) ([]nlu.ActionStep, error) {
	items, err := s.briefingItems(ctx)
	if err != nil {
		return nil, err
	}
	var steps []nlu.ActionStep
	for _, item := range items {
		steps = append(steps, nlu.ActionStep{Acao: item.Action, Termo: item.Term, Titulo: item.Title})
	}
	return steps, nil
}

func (s *Server) briefingNews(ctx context.Context, topic string) (string, error) {
	news, err := web.SearchGoogleNews(ctx, topic+" when:1d", 5)
	if err != nil {
		return "", err
	}
	if len(news) == 0 {
		return "Nenhuma manchete recente encontrada para esse tema.", nil
	}
	var out strings.Builder
	for _, item := range news {
		fmt.Fprintf(&out, "• %s\n%s\n%s\n\n", item.Title, item.Date, item.Link)
	}
	summary := s.summarize(ctx, "Tema: "+topic+". Sintetize somente as manchetes a seguir, sem inferir detalhes dos artigos:\n"+out.String())
	return "*Síntese das manchetes*\n" + summary + "\n\n" + out.String(), nil
}

func (s *Server) briefingCommand(ctx context.Context, request string) string {
	parts := strings.Fields(request)
	if len(parts) == 0 {
		text, err := s.engine.BuildBriefing(ctx, s.today())
		if err != nil {
			return "⚠️ Não foi possível consultar os dados do briefing."
		}
		return text
	}
	command := synonyms.RemoveAccents(strings.ToLower(parts[0]))
	argument := strings.TrimSpace(strings.TrimPrefix(request, parts[0]))
	switch command {
	case "config", "itens", "composicao":
		return s.showBriefingConfig(ctx)
	case "configurar", "definir":
		return s.configureBriefingText(ctx, "Configure", argument)
	case "adicionar", "incluir":
		return s.configureBriefingText(ctx, "Adicione", argument)
	case "remover", "retirar":
		return s.configureBriefing(ctx, &nlu.IntentResult{Acao: "remover_briefing", Termo: argument})
	case "horario":
		return s.saveScheduledAction(ctx, &nlu.IntentResult{Acao: "briefing", Horario: argument, Frequencia: "diario"})
	default:
		return s.configureBriefingText(ctx, "Configure", request)
	}
}
