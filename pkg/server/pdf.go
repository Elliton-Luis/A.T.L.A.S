package server

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"atlas/pkg/calculator"
	"atlas/pkg/database"
	"atlas/pkg/exportpdf"
	"atlas/pkg/nlu"
	"atlas/pkg/scheduler"
	"atlas/pkg/synonyms"
)

var pdfDate = regexp.MustCompile(`\b[0-9]{4}-[0-9]{2}-[0-9]{2}\b`)

func pdfFastPath(text string) (string, bool) {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	if !strings.Contains(clean, "pdf") {
		return "", false
	}
	for _, word := range []string{"em pdf", "para pdf", "export", "gere", "gerar", "crie", "criar", "quero", "mande", "envie"} {
		if strings.Contains(clean, word) {
			return "/pdf " + text, true
		}
	}
	return "", false
}

func (s *Server) pdfCommand(ctx context.Context, chatID int64, text string) string {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	if clean == "ajuda" {
		return "*Exportar PDF*\n/pdf rotina semana — calendário dos próximos 7 dias\n/pdf agenda mes — compromissos do mês\n/pdf checklist hoje — tarefas e passos\n/pdf briefing — conteúdo do briefing\nPode combinar: /pdf agenda checklist semana. Dados sem horário ficam separados, sem inventar horários."
	}
	intent := &nlu.IntentResult{Acao: "exportar_pdf"}
	for _, section := range []struct {
		value string
		words []string
	}{{"rotina", []string{"rotina", "calendario"}}, {"agenda", []string{"agenda", "compromissos"}}, {"checklist", []string{"checklist", "tarefas", "planos"}}, {"briefing", []string{"briefing"}}} {
		for _, word := range section.words {
			if strings.Contains(clean, word) {
				intent.Secoes = append(intent.Secoes, section.value)
				break
			}
		}
	}
	if len(intent.Secoes) == 0 && strings.TrimSpace(text) != "" {
		return "Quais informações deseja exportar? Posso reunir rotina, agenda, checklist ou briefing. Exemplo: /pdf agenda checklist semana."
	}
	switch {
	case strings.Contains(clean, "mes"):
		intent.Periodo = "mes"
	case strings.Contains(clean, "hoje") || strings.Contains(clean, "amanha") || strings.Contains(clean, "ontem"):
		intent.Periodo = "dia"
	default:
		intent.Periodo = "semana"
	}
	if strings.Contains(clean, "amanha") {
		intent.Data = time.Now().In(s.cfg.Location).AddDate(0, 0, 1).Format("2006-01-02")
	} else if strings.Contains(clean, "ontem") {
		intent.Data = "ontem"
	}
	if date := pdfDate.FindString(text); date != "" {
		intent.Data = date
		if !strings.Contains(clean, "semana") && !strings.Contains(clean, "mes") {
			intent.Periodo = "dia"
		}
	}
	if len(intent.Secoes) == 1 && intent.Secoes[0] == "briefing" {
		intent.Periodo = "dia"
	}
	return s.exportPDF(ctx, chatID, intent)
}

func (s *Server) exportPDF(ctx context.Context, chatID int64, intent *nlu.IntentResult) string {
	document, err := s.pdfDocument(ctx, intent)
	if err != nil {
		return "⚠️ Não foi possível montar o PDF: " + err.Error()
	}
	content, err := exportpdf.Render(document)
	if err != nil {
		return "⚠️ Não foi possível gerar o PDF."
	}
	file, err := os.CreateTemp("", "atlas-calendario-*.pdf")
	if err != nil {
		return "⚠️ Não foi possível preparar o arquivo."
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(content); err != nil {
		file.Close()
		return "⚠️ Falha ao preparar o PDF."
	}
	if err := file.Close(); err != nil {
		return "⚠️ Falha ao finalizar o PDF."
	}
	if err := s.tg.SendDocument(ctx, chatID, path, document.Title+" — "+document.Subtitle); err != nil {
		return "⚠️ PDF gerado, mas o envio não foi confirmado. Tente /pdf novamente."
	}
	return "✅ PDF enviado com os dados e horários registrados."
}

func (s *Server) pdfDocument(ctx context.Context, intent *nlu.IntentResult) (exportpdf.Document, error) {
	date, err := s.taskDate(intent.Data)
	if err != nil {
		return exportpdf.Document{}, err
	}
	start, err := time.ParseInLocation("2006-01-02", date, s.cfg.Location)
	if err != nil {
		return exportpdf.Document{}, err
	}
	days := 7
	switch intent.Periodo {
	case "", "semana":
	case "dia":
		days = 1
	case "mes":
		start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
		days = time.Date(start.Year(), start.Month()+1, 0, 0, 0, 0, 0, start.Location()).Day()
	default:
		return exportpdf.Document{}, fmt.Errorf("escolha dia, semana ou mês")
	}
	selected := make(map[string]bool)
	if len(intent.Secoes) == 0 {
		intent.Secoes = []string{"rotina"}
	}
	for _, section := range intent.Secoes {
		switch section {
		case "rotina":
			selected["agenda"] = true
			selected["rotinas"] = true
			selected["checklist"] = true
		case "agenda", "rotinas", "checklist", "briefing":
			selected[section] = true
		default:
			return exportpdf.Document{}, fmt.Errorf("seção não reconhecida: %s", section)
		}
	}
	end := start.AddDate(0, 0, days-1)
	if selected["briefing"] && start.Format("2006-01-02") != s.today() {
		return exportpdf.Document{}, fmt.Errorf("o briefing usa fontes do dia atual; exporte a agenda para outras datas")
	}
	document := exportpdf.Document{Title: "ATLAS — Calendário pessoal", Subtitle: fmt.Sprintf("%s a %s | %s", start.Format("02/01/2006"), end.Format("02/01/2006"), s.cfg.Location.String())}
	var events map[string][]database.Evento
	if selected["agenda"] {
		events, err = s.db.EventsForRange(ctx, start.Format("2006-01-02"), days, s.cfg.Location)
		if err != nil {
			return document, err
		}
	}
	var routines []database.RotinaProativa
	if selected["rotinas"] {
		routines, err = s.db.GetActiveRoutines(ctx)
		if err != nil {
			return document, err
		}
	}
	tasksByDate := make(map[string][]database.DailyTask)
	if selected["checklist"] {
		tasks, err := s.db.DailyTasksRange(ctx, start.Format("2006-01-02"), end.Format("2006-01-02"))
		if err != nil {
			return document, err
		}
		for _, task := range tasks {
			tasksByDate[task.Date] = append(tasksByDate[task.Date], task)
		}
	}
	weekdays := []string{"Domingo", "Segunda-feira", "Terça-feira", "Quarta-feira", "Quinta-feira", "Sexta-feira", "Sábado"}
	if selected["agenda"] || selected["rotinas"] || selected["checklist"] {
		for i := 0; i < days; i++ {
			day := start.AddDate(0, 0, i)
			date := day.Format("2006-01-02")
			section := exportpdf.Section{Title: weekdays[day.Weekday()] + " — " + day.Format("02/01/2006")}
			for _, event := range events[date] {
				official, err := parseTimeFlexible(event.HorarioOficial, s.cfg.Location)
				if err != nil {
					return document, fmt.Errorf("horário inválido no compromisso %s: %w", event.Descricao, err)
				}
				row := exportpdf.Row{Time: official.Format("15:04"), Activity: event.Descricao}
				if official.Format("2006-01-02") != date {
					row.Time = official.Format("02/01 15:04")
					row.Activity += " (iniciado em outro dia)"
				}
				if event.DuracaoMin != nil {
					end := official.Add(time.Duration(*event.DuracaoMin) * time.Minute)
					label := end.Format("15:04")
					if end.Format("2006-01-02") != date {
						label = end.Format("02/01 15:04")
					}
					row.Time += "\n" + label
					row.Activity += fmt.Sprintf(" (%d min)", *event.DuracaoMin)
				} else {
					row.Activity += " (duração não informada)"
				}
				if event.Status == "concluido" {
					row.Activity += " (concluído)"
				}
				buffers := calculator.Calculate(official, event.DeslocamentoMin, event.PreparoMin, calculator.DefaultDelayMargin)
				format := func(value time.Time) string {
					if value.Format("2006-01-02") != date {
						return value.Format("02/01 15:04")
					}
					return value.Format("15:04")
				}
				if event.PreparoMin > 0 {
					value := buffers.BufferPrep
					if event.Frequencia == "pontual" {
						if stored, err := parseTimeFlexible(event.HorarioBuffer1, s.cfg.Location); err == nil {
							value = stored
						}
					}
					row.Prep = format(value)
				}
				if event.DeslocamentoMin > 0 {
					value := buffers.BufferLeave
					if event.Frequencia == "pontual" {
						if stored, err := parseTimeFlexible(event.HorarioBuffer2, s.cfg.Location); err == nil {
							value = stored
						}
					}
					row.Leave = format(value)
				}
				section.Rows = append(section.Rows, row)
			}
			for _, routine := range routines {
				match, err := scheduler.ScheduledForDay(routine, day)
				if err != nil {
					return document, fmt.Errorf("envio %d contém dados inválidos", routine.ID)
				}
				if match {
					label := "Automação: "
					if scheduler.RoutineKind(routine) == "lembrete" {
						label = "Lembrete: "
					}
					section.Rows = append(section.Rows, exportpdf.Row{Time: routine.Horario, Activity: label + routine.Nome})
				}
			}
			sort.SliceStable(section.Rows, func(i, j int) bool { return section.Rows[i].Time < section.Rows[j].Time })
			if selected["agenda"] || selected["rotinas"] {
				if len(section.Rows) == 0 {
					section.Notes = append(section.Notes, "Nenhum compromisso ou rotina com horário registrado.")
				}
			}
			if len(tasksByDate[date]) > 0 {
				section.Notes = append(section.Notes, "Checklist — atividades sem horário definido:")
				for _, task := range tasksByDate[date] {
					status := "[ ]"
					if task.Done {
						status = "[x]"
					}
					note := status + " " + task.Description
					if task.Minutes > 0 {
						note += fmt.Sprintf(" | %d min | %s | Resultado: %s", task.Minutes, task.Difficulty, task.Completion)
					}
					if task.Resource != "" {
						note += "\n" + task.Resource
					}
					section.Notes = append(section.Notes, note)
				}
			} else if selected["checklist"] {
				section.Notes = append(section.Notes, "Nenhuma tarefa anotada para esse dia.")
			}
			document.Sections = append(document.Sections, section)
		}
	}
	if selected["briefing"] {
		text, err := s.engine.BuildBriefing(ctx, start.Format("2006-01-02"))
		if err != nil {
			return document, err
		}
		document.Sections = append(document.Sections, exportpdf.Section{Title: "Briefing — " + start.Format("02/01/2006"), Notes: []string{strings.ReplaceAll(text, "*", "")}})
	}
	return document, nil
}
