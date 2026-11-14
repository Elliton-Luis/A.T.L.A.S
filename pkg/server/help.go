package server

import (
	"strings"

	"atlas/pkg/synonyms"
)

const helpIndex = `*ATLAS — Setores*
1. Organização do dia /organizar
2. Briefing e envios /proatividade
3. E-mails /correio
4. Fé e oração /fe
5. Ideias e pesquisa /conhecimento
6. Linux e ferramentas /ferramentas
7. Finanças /financas
8. Perfil e sistema /sistema`

func sectorHelp(sector string) string {
	sector = strings.TrimPrefix(synonyms.RemoveAccents(strings.ToLower(strings.TrimSpace(sector))), "/")
	menus := map[string]string{
		"organizar": `*Organização do dia*
/dia — visão geral, sem criar outro cadastro
*Compromissos: possuem horário e duração*
/agenda — todas as atividades, agrupadas por dia/horário
/compromisso <ID> horario HH:MM — alterar somente uma linha
/hoje — compromissos de hoje
/calendario — visualização mensal da mesma agenda
*Tarefas: não possuem horário fixo*
/planejar <atividade> por 30 min — passos práticos e gratuitos
/tarefas — checklist de hoje
/tarefa <texto; outro texto> — anotar tarefas
/feito <nome ou ID> — marcar conclusão
/tarefas ontem — histórico
/metas — objetivos de longo prazo
/pdf rotina — exportar calendário em PDF

Também pode dizer: "Meu plano é estudar inglês por meia hora."
/help — setores`,
		"proatividade": `*Avisos e envios programados*
*Lembretes: mensagens fixas*
/lembretes — listar só lembretes
/lembrete criar 09:00 beber água — criar mensagem fixa
/lembrete <ID> horario HH:MM — mudar horário
/lembrete <ID> pausar ou ativar — controlar envio
/lembrete <ID> excluir — excluir só esse lembrete
*Automações: executam ferramentas e enviam resultados*
/automacoes — listar só automações
/automacao <ID> horario HH:MM — mudar horário
/automacao <ID> pausar ou ativar — controlar execução
/automacao <ID> excluir — excluir só essa automação
*Briefing: composição diária de informações*
/briefing — receber agora
/briefing config — ver conteúdo escolhido
/briefing configurar <itens> — definir composição
/briefing adicionar <item> — acrescentar conteúdo
/briefing remover <número> — retirar conteúdo
/briefing horario 07:00 — definir envio diário
/review — fechamento do dia
/help — setores`,
		"emails": `*E-mails — somente leitura*
/emails — consultar não lidos
/emails remetente: nome assunto: tema — filtrar
/emails hoje — consultar recebidos hoje
/resumo emails de hoje — relevantes e síntese

Também pode dizer: "Veja meus emails de ontem e faça um resumo."
/help — setores`,
		"fe": `*Fé e oração*
/liturgia — leituras do dia
/santo — santo do dia
/versiculo João 3:16 — passagem bíblica
/horas — oração da hora atual
/horas laudes — escolher hora canônica
/horas ativar — ativar envios diários
/horas desativar — suspender envios
/help — setores`,
		"conhecimento": `*Ideias e pesquisa*
/ideias — ideias e projetos registrados
/ideia <ID> — abrir uma ideia
/noticias <tema> — manchetes com fontes
/resumo <texto> — síntese
/swarm <tema> — conselho de especialistas
Envie documentos ou áudios para análise.
Diga "anote uma ideia" ou "pesquise sobre <tema>".
/help — setores`,
		"ferramentas": `*Linux e ferramentas*
/sonar <comando> ou $ <comando> — terminal
/py <código> — executar Python
/crawl <URL> — rastrear páginas
/help — setores`,
		"financas": `*Finanças*
/salario <valor> — guardar salário mensal
/simular_compra <custo> [gastos] [dívidas] — índice de 1 a 20
/contas — contas, faturas e assinaturas
/conta <nome> dia <dia> [valor] — cadastrar vencimento
/pagar <nome> — registrar pagamento
/apagar_conta <nome> — remover monitoramento
/help — setores`,
		"sistema": `*Perfil e sistema*
/perfil — informações lembradas sobre você
/tokens — consumo de IA
/backup — cópia do banco local
/hud — painel visual
/capacidades — funções, disponibilidade e limites
Diga "lembre-se que..." para registrar uma informação.
/help — setores`,
	}
	switch sector {
	case "email", "e-mail", "correio":
		sector = "emails"
	case "linux", "terminal":
		sector = "ferramentas"
	case "produtividade", "dia":
		sector = "organizar"
	}
	if menu, ok := menus[sector]; ok {
		return menu
	}
	return helpIndex
}

func helpFastPath(text string) (string, bool) {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	helpIntent := clean == "ajuda" || clean == "help" || clean == "menu" || clean == "comandos"
	for _, phrase := range []string{"ajuda com", "ajuda de", "ajuda para", "quero ajuda", "preciso de ajuda", "ajude com", "como usar", "quais comandos", "comandos de", "comandos do", "comandos para", "menu de", "menu do", "mostre o menu", "mostrar o menu"} {
		if strings.Contains(clean, phrase) {
			helpIntent = true
			break
		}
	}
	if !helpIntent {
		return "", false
	}
	for _, group := range []struct {
		command string
		words   []string
	}{
		{"/ferramentas", []string{"linux", "terminal", "python", "ferramentas"}},
		{"/correio", []string{"email", "e-mail", "correio", "inbox"}},
		{"/proatividade", []string{"briefing", "lembrete", "automacao", "envios"}},
		{"/organizar", []string{"rotina", "planejar", "planejamento", "tarefas", "agenda", "metas", "pdf"}},
		{"/fe", []string{"liturgia", "santo", "biblia", "oracao", "religiao"}},
		{"/conhecimento", []string{"pesquisa", "ideias", "noticias"}},
		{"/financas", []string{"contas", "financas", "pagamento", "fatura"}},
		{"/sistema", []string{"perfil", "tokens", "backup", "sistema"}},
	} {
		for _, word := range group.words {
			if strings.Contains(clean, word) {
				return group.command, true
			}
		}
	}
	if clean == "ajuda" || clean == "help" || clean == "menu" || clean == "comandos" || strings.Contains(clean, "quais comandos") {
		return "/ajuda", true
	}
	return "", false
}
