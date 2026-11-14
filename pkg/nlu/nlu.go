package nlu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"atlas/pkg/capabilities"
	"atlas/pkg/database"
	"atlas/pkg/pool"
	"atlas/pkg/schedule"
)

type Client struct {
	groqPool          *pool.KeyPool
	groqModel         string
	nvidiaPool        *pool.KeyPool
	nvidiaModel       string
	maxTokensQuick    int
	maxTokensHeavy    int
	maxTokensResearch int
	httpClient        *http.Client
	capabilitySource  func() capabilities.Runtime
}

// SetCapabilitySource is wired once at server construction; no secrets enter prompts.
func (c *Client) SetCapabilitySource(source func() capabilities.Runtime) { c.capabilitySource = source }
func (c *Client) HasProvider() bool                                      { return !c.groqPool.IsEmpty() || !c.nvidiaPool.IsEmpty() }
func (c *Client) capabilityPrompt() string {
	runtime := capabilities.Runtime{AI: c.HasProvider(), Voice: !c.groqPool.IsEmpty()}
	if c.capabilitySource != nil {
		runtime = c.capabilitySource()
	}
	return capabilities.Prompt(runtime)
}

type IntentResult struct {
	Salario                   *float64             `json:"salario,omitempty"`
	Gastos                    *float64             `json:"gastos,omitempty"`
	Dividas                   *float64             `json:"dividas,omitempty"`
	CustoItem                 *float64             `json:"custo_item,omitempty"`
	Alteracoes                []database.EventEdit `json:"alteracoes,omitempty"`
	DiaOrigem                 *int                 `json:"dia_origem,omitempty"`
	HorarioOrigem             string               `json:"horario_origem,omitempty"`
	Secoes                    []string             `json:"secoes,omitempty"`
	Periodo                   string               `json:"periodo,omitempty"`
	DuracaoMin                int                  `json:"duracao_min,omitempty"`
	Tarefas                   []string             `json:"tarefas,omitempty"`
	Data                      string               `json:"data,omitempty"`
	Acoes                     []ActionStep         `json:"acoes,omitempty"`
	Tipo                      string               `json:"tipo"`           // acao, evento, ideia, adicionar_ideia, lembrar_fato, esquecer_fato, objetivo, lembrete_diario, conversa
	Acao                      string               `json:"acao,omitempty"` // apagar_rotina, alterar_rotina, toggle_rotina, concluir_evento, apagar_evento, atualizar_meta, fazer_backup, listar_agenda, listar_lembretes
	ID                        int64                `json:"id,omitempty"`
	Termo                     string               `json:"termo,omitempty"`
	Progresso                 int                  `json:"progresso,omitempty"`
	Complexidade              string               `json:"complexidade,omitempty"` // "simples" ou "pesada"
	Titulo                    string               `json:"titulo,omitempty"`
	Conteudo                  string               `json:"conteudo,omitempty"`
	SumaIdeia                 string               `json:"suma_ideia,omitempty"`
	PerguntaSocratica         string               `json:"pergunta_socratica,omitempty"`
	ChaveMemoria              string               `json:"chave_memoria,omitempty"`
	ValorMemoria              string               `json:"valor_memoria,omitempty"`
	Descricao                 string               `json:"descricao,omitempty"`
	Frequencia                string               `json:"frequencia,omitempty"`  // "pontual", "diario", "semanal", "mensal"
	DiaSemana                 *int                 `json:"dia_semana,omitempty"`  // 0=Domingo, 1=Segunda, ..., 6=Sábado
	DiasSemana                []int                `json:"dias_semana,omitempty"` // Múltiplos dias da semana (0=Dom a 6=Sáb) para semanas com exceções
	DiaMes                    *int                 `json:"dia_mes,omitempty"`     // 1 a 31
	HorarioOficial            string               `json:"horario_oficial,omitempty"`
	EstimativaDeslocamentoMin *int                 `json:"estimativa_deslocamento_min,omitempty"`
	EstimativaPreparoMin      *int                 `json:"estimativa_preparo_min,omitempty"`
	Prazo                     string               `json:"prazo,omitempty"`
	Resposta                  string               `json:"resposta,omitempty"`
	Horario                   string               `json:"horario,omitempty"`
	Nome                      string               `json:"nome,omitempty"`
	Mensagem                  string               `json:"mensagem,omitempty"`
	URL                       string               `json:"url,omitempty"`
	SubstituirEventoID        int64                `json:"substituir_evento_id,omitempty"`
	DiaVencimento             int                  `json:"dia_vencimento,omitempty"`
	Valor                     float64              `json:"valor,omitempty"`
	Categoria                 string               `json:"categoria,omitempty"`

	// Token usage telemetry
	PromptTokens     int    `json:"-"`
	CompletionTokens int    `json:"-"`
	TotalTokens      int    `json:"-"`
	ProvedorUsado    string `json:"-"`
	ModeloUsado      string `json:"-"`
}

type ActionStep struct {
	Acao   string `json:"acao"`
	Termo  string `json:"termo,omitempty"`
	Titulo string `json:"titulo,omitempty"`
}

type IntentContext struct {
	Routines          []database.RotinaProativa
	RoutineCategories map[int64]string
	Tasks             []database.DailyTask
	Briefing          []ActionStep
}

var Curiosidades = []string{
	"Você sabia que os flamingos nascem cinzas e só ficam cor-de-rosa porque se alimentam de algas e camarões ricos em carotenoides?",
	"Você sabia que o mel puro é o único alimento da Terra que nunca estraga? Potes de 3.000 anos achados no Egito continuam perfeitamente comestíveis.",
	"Você sabia que os polvos possuem três corações, sangue azul e que dois terços dos seus neurônios ficam espalhados nos próprios tentáculos?",
	"Você sabia que as lontras-marinhas dormem de mãos dadas na água para que a correnteza não as separe durante o sono?",
	"Você sabia que a cada dois minutos tiramos mais fotos no mundo do que toda a humanidade tirou ao longo de todo o século XIX?",
	"Você sabia que o tubarão-da-groenlândia pode viver mais de 400 anos, o que significa que alguns vivos hoje nasceram antes da Revolução Industrial?",
	"Você sabia que as árvores compartilham nutrientes e se avisam sobre pragas no subsolo usando uma rede de fungos conhecida como 'Wood Wide Web'?",
}

func RandomCuriosity() string {
	idx := time.Now().UnixNano() % int64(len(Curiosidades))
	if idx < 0 {
		idx = -idx
	}
	return Curiosidades[idx]
}

type ChatMessagePayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionRequest struct {
	Model       string               `json:"model"`
	Messages    []ChatMessagePayload `json:"messages"`
	Temperature float64              `json:"temperature"`
	MaxTokens   int                  `json:"max_tokens"`
	Structured  bool                 `json:"-"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			Reasoning string `json:"reasoning,omitempty"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type APIResponse struct {
	Text         string
	Provider     string
	Model        string
	PromptTokens int
	ComplTokens  int
	TotalTokens  int
}

func NewClient(groqKeys, nvidiaKeys, nvidiaModel string, maxTokensQuick, maxTokensHeavy, maxTokensResearch int) *Client {
	if nvidiaModel == "" {
		nvidiaModel = "meta/llama-3.2-90b-vision-instruct"
	}
	if maxTokensHeavy <= 0 || maxTokensHeavy > 400 {
		maxTokensHeavy = 350
	}
	if maxTokensQuick <= 0 {
		maxTokensQuick = 300
	}
	if maxTokensResearch <= 0 || maxTokensResearch > 800 {
		maxTokensResearch = 500
	}
	return &Client{
		groqPool:          pool.New(groqKeys),
		groqModel:         "qwen/qwen3.8-27b",
		nvidiaPool:        pool.New(nvidiaKeys),
		nvidiaModel:       nvidiaModel,
		maxTokensQuick:    maxTokensQuick,
		maxTokensHeavy:    maxTokensHeavy,
		maxTokensResearch: maxTokensResearch,
		httpClient: &http.Client{
			Timeout: 12 * time.Second,
		},
	}
}

var (
	jsonBlockRegex = regexp.MustCompile(`(?s)\{.*\}`)
	respostaRegex  = regexp.MustCompile(`(?s)"resposta"\s*:\s*"((?:[^"\\]|\\.)*)`)
)

// SanitizeHumanResponse strips JSON wrappers, escaped quotes and backslashes, ensuring clean human text.
func SanitizeHumanResponse(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// If contains JSON artifacts like "resposta": "...", extract the inner message
	if strings.Contains(s, `"resposta"`) {
		if m := respostaRegex.FindStringSubmatch(s); len(m) > 1 {
			s = m[1]
		}
	}

	// Unescape JSON escape sequences to real formatting
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\t`, "  ")

	// If string still starts with JSON curly brace, strip lines that look like JSON keys
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		var cleanLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") || trimmed == "{" || trimmed == "}" ||
				strings.HasPrefix(trimmed, `"tipo":`) || strings.HasPrefix(trimmed, `"complexidade":`) ||
				strings.HasPrefix(trimmed, `"resposta":`) {
				continue
			}
			cleanLines = append(cleanLines, line)
		}
		s = strings.Join(cleanLines, "\n")
	}

	return strings.TrimSpace(s)
}

// buildCleanHistory ensures an alternating user/assistant history without duplicates or orphan backlogs.
func buildCleanHistory(history []database.ChatMessage, currentInput string) []ChatMessagePayload {
	var clean []ChatMessagePayload
	for _, h := range history {
		if strings.TrimSpace(h.Content) == "" {
			continue
		}
		// Prevent consecutive turns of the same role (deduplicate)
		if len(clean) > 0 && clean[len(clean)-1].Role == h.Role {
			clean[len(clean)-1].Content = h.Content
			continue
		}
		clean = append(clean, ChatMessagePayload{
			Role:    h.Role,
			Content: h.Content,
		})
	}

	// Direct commands may have no recorded assistant turn. Preserve their context
	// instead of discarding the request that a follow-up refers to.
	if len(clean) > 0 && clean[len(clean)-1].Role == "user" && currentInput != "" {
		if clean[len(clean)-1].Content != currentInput {
			clean[len(clean)-1].Content += "\n\nMensagem atual: " + currentInput
		}
		return clean
	}

	if currentInput != "" {
		clean = append(clean, ChatMessagePayload{
			Role:    "user",
			Content: currentInput,
		})
	}

	return clean
}

// ParseIntent sends the user's message along with sliding history, active events and long-term memory to NLU.
func (c *Client) ParseIntent(ctx context.Context, text, currentDatetime string, history []database.ChatMessage, userMemories []database.MemoriaUsuario, latestIdeia *database.Ideia, activeEvents []database.Evento, extra ...IntentContext) (*IntentResult, error) {
	if c.groqPool.IsEmpty() && c.nvidiaPool.IsEmpty() {
		return nil, fmt.Errorf("nenhuma chave de IA (Groq ou NVIDIA) configurada")
	}

	var memSb strings.Builder
	if len(userMemories) > 0 {
		memSb.WriteString("\n\nPERFIL E MEMÓRIAS PERMANENTES DO USUÁRIO (USE SEMPRE ESTE CONTEXTO PARA PERSONALIZAR):\n")
		for _, m := range userMemories {
			memSb.WriteString(fmt.Sprintf("- %s: %s\n", m.Chave, m.Valor))
		}
	}

	var ideiaSb strings.Builder
	if latestIdeia != nil {
		ideiaSb.WriteString(fmt.Sprintf("\n\nÚLTIMA IDEIA EM DESENVOLVIMENTO (ID %d - '%s'):\nSuma atual:\n%s\nSe o usuário estiver trazendo mais argumentos, detalhes ou complementos a essa ideia, use 'tipo': 'adicionar_ideia'.\n", latestIdeia.ID, latestIdeia.Titulo, latestIdeia.SumaExecutiva))
	}

	var agendaSb strings.Builder
	if len(extra) > 0 {
		agendaSb.WriteString("\nROTINAS EXISTENTES (use o ID correto para alterar ou cancelar; nunca invente IDs):\n")
		for _, r := range extra[0].Routines {
			category := r.Tipo
			if value, ok := extra[0].RoutineCategories[r.ID]; ok {
				category = value
			}
			fmt.Fprintf(&agendaSb, "- ID %d: %s às %s (categoria %s, ativo %d)\n", r.ID, r.Nome, r.Horario, category, r.Ativo)
		}
		agendaSb.WriteString("\nCHECKLIST DO DIA (não confundir com compromissos da agenda):\n")
		for _, task := range extra[0].Tasks {
			fmt.Fprintf(&agendaSb, "- ID %d: %s (concluída: %t)\n", task.ID, task.Description, task.Done)
		}
		agendaSb.WriteString("\nCOMPOSIÇÃO ATUAL DO BRIEFING (número identifica posição do item):\n")
		for i, item := range extra[0].Briefing {
			fmt.Fprintf(&agendaSb, "- %d: %s (ação: %s; parâmetro: %s)\n", i+1, item.Titulo, item.Acao, item.Termo)
		}
	}
	if len(activeEvents) > 0 {
		agendaSb.WriteString("\n\n" + schedule.Format(activeEvents, "COMPROMISSOS ATIVOS — grupos e IDs reais", time.UTC))
		agendaSb.WriteString("\nPara alterar uma linha existente use acao alterar_evento com seu ID. Cada dia/horário tem ID próprio. Não transforme atualização em cadastro nem delete e recrie a atividade.\n")
	}

	systemPrompt := fmt.Sprintf(`Você é o ATLAS: assistente pessoal executivo de alto nível, inspirado no JARVIS.
Horário atual de referência: %s.%s%s%s

%s
Núcleo: organização pessoal; planejamento prático e gratuito, checklist, agenda e briefing.
CONCEITOS INCONFUNDÍVEIS:
- Compromisso: possui início e duração, único cadastro da agenda. Recorrência pertence ao compromisso.
- Calendário: visualização da agenda, não outra entidade para cadastrar ou editar.
- Tarefa: checklist sem horário fixo. Plano: desdobramento prático de uma tarefa com orçamento de tempo.
- Lembrete: texto fixo programado. Automação: executa ferramenta e envia conteúdo atualizado.
- Rotina/dia: visão geral de compromissos, tarefas e envios. "Minha rotina" => acao ver_dia; não crie outro cadastro chamado rotina.
- Nunca exclua de outra categoria quando não encontrar o alvo. Preserve IDs e categorias reais do contexto.
Agenda aplica margem de 15%% para preparo/saída e consulta conclusão após o evento. iCal é exportação, não leitura de calendários externos.

PERSONALIDADE (ESTILO JARVIS):
- Calmo, sofisticado, sóbrio, educado, altamente competente e respeitoso.
- JAMAIS seja grosseiro, arrogante, sarcástico ou combativo.
- Em ideias, planos ou reflexões (MÉTODO SOCRÁTICO): atue como um conselheiro estratégico experiente. Valide a ideia, gere a suma executiva e levante com polidez um ponto cego real ou custo de oportunidade com uma pergunta inteligente.

Classifique a intenção estritamente em uma das opções abaixo e responda EXCLUSIVAMENTE em formato JSON puro:

1. Para NOVA ideia, pensamento ou projeto (ou se disser "anotar uma ideia", "tive uma ideia"):
{
  "tipo": "ideia",
  "complexidade": "simples" ou "pesada",
  "titulo": "Título curto da ideia",
  "conteudo": "Resumo fiel e exato do que o usuário disse",
  "suma_ideia": "Suma executiva estruturada e clara da ideia em tópicos",
  "pergunta_socratica": "Pergunta reflexiva apontando ponto cego ou risco estratégico"
}

2. Para CONTINUAÇÃO ou COMPLEMENTO de uma ideia já discutida (ex: "adicione à ideia", "mais um ponto", "anote também que..."):
{
  "tipo": "adicionar_ideia",
  "id": 0,
  "conteudo": "Resumo exato do novo ponto adicionado",
  "suma_ideia": "Nova suma executiva acumulada integrando todos os pontos anteriores com o novo ponto",
  "pergunta_socratica": "Novo questionamento estratégico sobre o plano atualizado"
}

3. Para GUARDAR UM FATO OU INFORMAÇÃO SOBRE O USUÁRIO (ex: "sou programador", "acordo às 6h", "minha meta é faturar 10k", "não como carne", "lembre-se que..."):
{
  "tipo": "lembrar_fato",
  "chave_memoria": "palavra_chave_curta (ex: profissao, rotina_sono, alimentacao, cidade, meta_financeira)",
  "valor_memoria": "O fato ou informação exata a ser lembrada",
  "resposta": "Confirmação respeitosa de que o fato foi gravado em sua memória permanente"
}

4. Para ESQUECER UM FATO gravado:
{
  "tipo": "esquecer_fato",
  "chave_memoria": "palavra_chave_a_remover"
}

5. Para compromissos / eventos na agenda (PONTUAIS ou RECORRENTES):
Para atividades em casa/online, deslocamento é 0. Preserve preparo e deslocamento explicitamente iguais a 0.
Aceite variações naturais como: "marque", "anote", "agende", "crie um compromisso", "coloque na minha agenda", "tenho X amanhã às Y", "anotar compromisso toda semana exceto...":
{
  "tipo": "evento",
  "complexidade": "simples",
  "descricao": "Nome do compromisso",
  "frequencia": "pontual" ou "diario" ou "semanal" ou "mensal",
  "horario": "HH:MM",
  "duracao_min": 180,
  "horario_oficial": "YYYY-MM-DD HH:MM:SS" (obrigatório se frequencia for "pontual"),
  "dia_semana": 0 a 6 (se for apenas um dia da semana, 0=Dom, 1=Seg, 2=Ter, 3=Qua, 4=Qui, 5=Sex, 6=Sáb),
  "dias_semana": [1, 2, 3] (obrigatório quando o usuário indicar múltiplos dias ou exceções na semana, ex: "segunda a sexta" -> [1,2,3,4,5], "exceto domingo e segunda" -> [2,3,4,5,6], "terça e quinta" -> [2,4]),
  "dia_mes": 1 a 31 (use apenas se frequencia for "mensal"),
  "estimativa_deslocamento_min": 25,
  "estimativa_preparo_min": 35,
  "substituir_evento_id": 0
}
Para novo compromisso, a duração deve vir do usuário. Se faltar, duracao_min=0: o bot pergunta antes de salvar.
"Dura 2-3 horas", "de 2 a 3 horas", "entre 2 e 3 horas": sempre considere o limite superior, 180 minutos. Um horário de início como "às 18h" NÃO é duração.
Preserve duração ao alterar apenas horário/dia. Para mudar a duração, use acao alterar_evento e duracao_min, ou o mesmo campo em cada alteração do lote.
5.1. CONSULTAR E ALTERAR COMPROMISSOS:
"todos os meus compromissos", "minha agenda", "ver as aulas": tipo acao, acao listar_agenda. "Agenda de hoje": listar_agenda_hoje. Sempre execute a consulta; não escreva uma lista própria na resposta.
Atividades como Aula são agrupadas pelo nome, mas cada linha tem dia, horário e ID independentes.
"Mude a Aula de sexta para 08:30": {"tipo":"acao","acao":"alterar_evento","id":ID_REAL_DA_SEXTA,"dia_origem":5,"horario":"08:30"}.
Só envie os campos que o usuário quer modificar. Nunca preencha frequência, outros dias, preparo ou deslocamento com defaults ao editar.
Se faltar identificação entre várias linhas, consulte a agenda e peça dia/horário ou ID; nunca escolha a primeira/última por nome.
"Mude segunda e quarta": {"tipo":"acao","acao":"alterar_evento","alteracoes":[{"id":ID_SEGUNDA,"horario":"19:00"},{"id":ID_QUARTA,"horario":"19:00"}]}.
Cada alteração é parcial e o lote é atômico. Para mover uma linha a outro dia, use dia_origem para selecionar e dia_semana para o novo dia. Não use dias_semana para sobrescrever uma atividade inteira.
Se o usuário responder "sim" à pergunta de fim previsto de um compromisso no histórico, use concluir_evento com o ID e a data da ocorrência indicada, inclusive quando começou na véspera.
Adicionar um novo dia/horário de Aula é tipo evento, sem substituir_evento_id. Se pedir apenas para ver os compromissos e depois alterá-los, primeiro liste os dados reais e peça as mudanças; não invente alterações.

6. Para metas e objetivos:
{
  "tipo": "objetivo",
  "complexidade": "simples",
  "titulo": "Meta declarada",
  "prazo": "YYYY-MM-DD HH:MM:SS" ou ""
}

7. Para lembretes diários recorrentes de TEXTO FIXO (ex: "todo dia às 09:00 me lembre de tomar creatina"):
{
  "tipo": "lembrete_diario",
  "complexidade": "simples",
  "horario": "HH:MM",
  "mensagem": "Mensagem exata a ser disparada todo dia"
}
Pode incluir frequencia diario/semanal e dias_semana. O texto é literal: mesmo contendo "liturgia", não execute a ferramenta se foi pedido um lembrete para ler/rezar.
Listagens: listar_lembretes mostra apenas mensagens fixas; listar_automacoes mostra apenas ações programadas.
Para alterar_rotina, toggle_rotina ou apagar_rotina, informe categoria="lembrete" ou "automacao" conforme o contexto e ID real. Nunca misture com IDs de compromissos/tarefas.
limpar_lembretes exclui apenas mensagens fixas; limpar_automacoes exclui apenas automações. Ambas têm confirmação. O termo genérico "rotina" sem alvo específico pede esclarecimento.

7.1. Para EXECUTAR UMA FERRAMENTA e enviar seu resultado periodicamente (liturgia, briefing, emails, santo, versículo, notícias):
NUNCA classifique como evento ou lembrete textual. Exemplo: "me mande a liturgia todo dia às 7h".
{
  "tipo": "acao_programada",
  "acao": "liturgia" ou "liturgia_das_horas" ou "briefing" ou "ler_emails" ou "santo_do_dia" ou "versiculo_biblico" ou "noticias",
  "horario": "HH:MM",
  "frequencia": "diario" ou "semanal",
  "dias_semana": [1,2,3,4,5],
  "termo": "parâmetros da ferramenta, filtros de email ou hora canônica, quando houver"
}
Para todos os dias, omita dias_semana. Para exceções, enumere apenas os dias desejados.
Para mudar/cancelar rotina existente, use alterar_rotina/apagar_rotina e ID existente.
"Mude para 8h" ou "cancele esse envio" refere-se à rotina discutida no histórico; se ambíguo, pergunte.
Para briefing imediato use tipo acao, acao briefing. Briefing usa agenda, rotinas, metas e contas reais.
Email aceita filtros por remetente, assunto, hoje/ontem e desde YYYY-MM-DD; preserve esses filtros em termo.
Para "veja meus emails e faça um resumo", use acao resumir_emails. Produz lista relevante E parágrafo de síntese com uma só leitura.
Para resumo de texto fornecido ou da resposta anterior, use acao resumir e termo com o texto (ou "isso").

7.2. Para combinar CONSULTAS e opcionalmente resumir seus resultados:
{"tipo":"sequencia","acoes":[{"acao":"ler_emails","termo":"hoje"},{"acao":"listar_agenda"},{"acao":"resumir"}]}
Execute na ordem pedida. O resumo consome os resultados anteriores, não precisa buscar novamente.
Consultas disponíveis: ler_emails, listar_agenda, listar_lembretes, listar_contas, ver_perfil, briefing, liturgia, liturgia_das_horas, santo_do_dia, versiculo_biblico, noticias.
Use no máximo 8 etapas. resumir deve ser a última. Não inclua alterações, exclusões ou execução de código nessa sequência.

7.3. BRIEFING PESSOAL CONFIGURÁVEL: conteúdo diário escolhido pelo usuário, persistido na ordem desejada.
Para "quero meu briefing com liturgia, santo, versículo sobre confiança, meu pensamento e agenda, às 7h":
{"tipo":"acao","acao":"configurar_briefing","horario":"07:00","acoes":[{"acao":"liturgia"},{"acao":"santo_do_dia"},{"acao":"versiculo_biblico","termo":"Provérbios 3:5-6","titulo":"Versículo sobre confiança"},{"acao":"pensamento","termo":"texto literal fornecido pelo usuário"},{"acao":"listar_agenda"}]}
Itens possíveis: agenda_completa (agenda/rotinas/metas/contas), listar_agenda (todos os compromissos do dia), liturgia, santo_do_dia, liturgia_das_horas (termo=hora), versiculo_biblico (termo=referência real), pensamento (termo=texto literal), ideia (termo=ID de ideia existente, vazio=última ideia), noticias_resumo (termo=tema escolhido), resumir_emails (termo=filtros), listar_contas, listar_lembretes.
listar_tarefas inclui o checklist de metas diárias no briefing.
Para versículo sobre tema, escolha uma referência bíblica real e preserve o tema em titulo. O texto será buscado na Bíblia; nunca escreva ou invente a citação.
Nunca invente pensamentos pessoais. Se o usuário não fornecer texto, use ideia para consultar a última ideia registrada.
"Adicione/inclua X no meu briefing": acao adicionar_briefing, acoes contendo SOMENTE os novos itens. Não substitua os anteriores.
"Remova X do briefing": acao remover_briefing e termo identificando nome, tema ou número do item.
"Mude o tema de notícias do briefing para economia": acao alterar_briefing, id=número do item existente e acoes=[apenas o novo item]. Preserve a posição e os outros itens.
Para reordenar, use configurar_briefing com todos os itens atuais na ordem pedida. Não omita itens anteriores sem solicitação de remoção.
"O que tem no meu briefing?": acao ver_briefing.
Use configurar_briefing apenas quando o usuário definir a composição completa. Sem alterar conteúdo, mudança de horário continua alterar_rotina.
Horario é opcional; omita quando o usuário não informar. Não crie horários ou temas de notícias que não foram pedidos.

7.4. CHECKLIST / METAS DIÁRIAS (sem horário obrigatório, diferente de compromissos e objetivos de longo prazo):
"Anote minhas tarefas de hoje: estudar Go, treinar e ler":
{"tipo":"acao","acao":"anotar_tarefas","tarefas":["Estudar Go","Treinar","Ler"]}
"Anote que fiz tal task", "fiz o treino", "terminei de estudar":
{"tipo":"acao","acao":"concluir_tarefa","id":0,"termo":"nome da tarefa do checklist"}
Use ID real do checklist acima quando identificar. Nunca marque compromisso como tarefa nem invente que uma tarefa já foi anotada.
Mapeie variações naturais como "fiz o treino" para "Treinar" usando os IDs existentes. Se mais de uma tarefa combinar, peça identificação.
"Minhas metas diárias", "checklist", "o que fiz ontem": acao listar_tarefas; data opcional YYYY-MM-DD (use a referência atual para resolver ontem).
Sem data explícita, ações usam hoje. Tarefas ficam no histórico por data; novo dia começa vazio, sem apagar dias anteriores.

7.5. PLANEJAR O DIA é o núcleo para tornar atividades, hábitos e estudos praticáveis.
"Meu plano é estudar inglês por meia hora", "planeje 30 minutos de inglês", "torne essa tarefa tangível":
{"tipo":"acao","acao":"planejar_dia","termo":"Estudar inglês","duracao_min":30,"id":0}
Extraia apenas atividade, ID real da tarefa quando houver e minutos EXPLICITAMENTE escolhidos pelo usuário. NUNCA invente duração.
Se não houver tempo definido, omita duracao_min ou use 0: o app vai perguntar e só então criar o plano.
Não gere passos no JSON de intenção: uma ferramenta própria cria e valida passos práticos, gratuitos e proporcionais ao tempo, com dificuldade e resultado verificável.
Planejar não cria evento com horário de saída e não é uma ideia abstrata. Use planejar_dia mesmo se o pedido parecer complexo.
7.6. AJUDA: acao ajuda_setor, termo organizar, proatividade, emails, fe, conhecimento, ferramentas, financas ou sistema. Sem termo, mostra apenas o índice de setores.
7.7. EXPORTAR PDF: para "quero minha rotina em PDF", "exporte minha agenda do mês", "mande meu checklist em PDF":
{"tipo":"acao","acao":"exportar_pdf","secoes":["rotina"],"periodo":"semana"}
Secoes disponíveis: rotina (agenda, rotinas e checklist), agenda, rotinas, checklist, briefing. Pode combinar seções.
Periodo: dia, semana ou mes. Data inicial opcional YYYY-MM-DD. Sem período, usa próximos 7 dias.
O PDF usa os dados reais do banco e envia um documento ao Telegram. Nunca invente horários para tarefas que só têm duração.
7.9. SIMULAÇÃO DE COMPRA: o cálculo e os 20 níveis são determinísticos, não opinião da IA.
"Meu salário mensal é R$ 5000": {"tipo":"acao","acao":"definir_salario","salario":5000}.
"Posso comprar um item de 1000, com gastos de 2000 e dívidas de 500?": {"tipo":"acao","acao":"simular_compra","custo_item":1000,"gastos":2000,"dividas":500}.
Salario é opcional na simulação: sem ele, usa o salário salvo. Não invente valores. Gastos e dívidas são os valores informados para essa simulação, não some automaticamente contas cadastradas.
Se as dívidas já estiverem incluídas nos gastos, não conte duas vezes. Não converta renda semanal/diária em mensal sem o usuário definir o valor mensal.
O app calcula saldo = salário - gastos - dívidas; saldo/custo >=2 atinge reserva mínima, >=10 atinge o nível 20. Se o salário não existir, o app pergunta e preserva os outros valores.
7.8. AUTOCONHECIMENTO: "o que você consegue fazer?", "você pode gerar PDF?", "qual integração está disponível?":
{"tipo":"acao","acao":"ver_capacidades","termo":"pdf"}
Termo opcional filtra o catálogo. Consultar capacidades não executa a ferramenta. Falta de credencial não significa recurso inexistente.
Para pedidos concretos, use a ação real. Terminal: executar_terminal com termo=comando explícito; rastreamento: rastrear_site com url; conselho: deliberar com termo=tema; painel: abrir_painel; consumo: ver_tokens; metas: listar_metas; ideias: listar_ideias/ver_ideia (id opcional).

8. Para ações do app, contas/cartões/assinaturas, buscas e exclusões ("anotar cartão dia 10", "paguei a netflix", "quais contas vencem?", "delete o dentista", "delete isso"):
{
  "tipo": "acao",
  "acao": "cadastrar_conta" (para cadastrar conta/cartão/assinatura com vencimento contínuo 3 dias antes), "listar_contas" (para listar contas e próximos vencimentos), "pagar_conta" (marcar conta/cartão/fatura como paga), "apagar_conta" (remover conta do monitoramento), "executar_codigo" (para rodar scripts ou trechos de código em Python no interpretador local/nuvem), "liturgia_das_horas" (para Laudes, Vésperas, Completas ou Ofício das Leituras), "santo_do_dia" (para consultar o Santo do Dia católico, história e oração), "versiculo_biblico" (para buscar versículos na Bíblia Católica com Vulgata Latina), "ler_emails" (para checar a caixa de entrada, ler e-mails ou resumir o que há de interessante), "apagar_evento" (para apagar 1 evento por nome ou "isso"), "apagar_rotina" (para apagar 1 rotina/lembrete), "limpar_agenda" (apagar todos os eventos), "limpar_lembretes" (apagar todos os lembretes), "limpar_tudo" (apagar agenda E lembretes), "liturgia", "noticias", "resumir_link", "alterar_rotina", "toggle_rotina", "concluir_evento", "atualizar_meta", "fazer_backup", "listar_agenda", "ver_calendario", "listar_lembretes", "ver_perfil",
  "id": 0,
  "termo": "palavra-chave ou nome da conta" (ex: "cartao nubank", "netflix", "aluguel", "dentista", "Joao 3:16"),
  "dia_vencimento": 10,
  "valor": 150.00,
  "categoria": "cartao" ou "assinatura" ou "boleto" ou "outros",
  "url": "https://...",
  "horario": "HH:MM",
  "progresso": 80
}

9. Para conversa, reflexão, debate ou pesquisa aprofundada:
{
  "tipo": "conversa" ou "pesquisa",
  "complexidade": "simples" ou "pesada" ou "pesquisa" (use "pesquisa" quando for solicitação de estudo aprofundado, pesquisa extensa, artigos, dossiê ou resumo detalhado),
  "resposta": "Sua resposta equilibrada, polida e perspicaz (apenas se complexidade for simples)"
}
`, currentDatetime, memSb.String(), ideiaSb.String(), agendaSb.String(), c.capabilityPrompt())

	messages := []ChatMessagePayload{
		{Role: "system", Content: systemPrompt},
	}
	messages = append(messages, buildCleanHistory(history, text)...)

	reqPayload := ChatCompletionRequest{
		Model:       c.groqModel,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   c.maxTokensQuick,
		Structured:  true,
	}

	apiRes, err := c.doRequest(ctx, reqPayload)
	if err != nil {
		return nil, err
	}

	rawResp := apiRes.Text
	match := jsonBlockRegex.FindString(rawResp)
	if match == "" {
		if strings.Contains(rawResp, "{") {
			return nil, fmt.Errorf("resposta de intenção incompleta")
		}
		return &IntentResult{
			Tipo:             "conversa",
			Resposta:         SanitizeHumanResponse(rawResp),
			PromptTokens:     apiRes.PromptTokens,
			CompletionTokens: apiRes.ComplTokens,
			TotalTokens:      apiRes.TotalTokens,
			ProvedorUsado:    apiRes.Provider,
			ModeloUsado:      apiRes.Model,
		}, nil
	}

	var res IntentResult
	if err := json.Unmarshal([]byte(match), &res); err != nil {
		return nil, fmt.Errorf("resposta de intenção inválida: %w", err)
	}

	res.Resposta = SanitizeHumanResponse(res.Resposta)
	res.PromptTokens = apiRes.PromptTokens
	res.CompletionTokens = apiRes.ComplTokens
	res.TotalTokens = apiRes.TotalTokens
	res.ProvedorUsado = apiRes.Provider
	res.ModeloUsado = apiRes.Model
	return &res, nil
}

// GenerateBriefing generates a proactive morning briefing based on today's events and goals.
func (c *Client) GenerateBriefing(ctx context.Context, todayStr string, events []database.Evento, goals []database.Objetivo) (string, error) {
	prompt := fmt.Sprintf(`Você é o ATLAS, assistente executivo estilo JARVIS. Apresente um briefing matinal refinado, claro e direto para o dia %s.
Compromissos agendados: %d
Metas ativas: %d

Diretrizes:
- Tom sereno, polido, focado e profissional.
- Destaque os compromissos principais e horários de saída com calma.
- Deixe 1 reflexão estratégica para guiar a atenção do dia.
- Máximo 2 parágrafos.`, todayStr, len(events), len(goals))

	reqPayload := ChatCompletionRequest{
		Model: c.groqModel,
		Messages: []ChatMessagePayload{
			{Role: "system", Content: "Você é o ATLAS: assistente executivo refinado e inteligente estilo JARVIS."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
		MaxTokens:   350,
	}

	res, err := c.doRequest(ctx, reqPayload)
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

// GenerateReview generates a calm, insightful evening review.
func (c *Client) GenerateReview(ctx context.Context, todayStr string, events []database.Evento, goals []database.Objetivo) (string, error) {
	prompt := fmt.Sprintf(`Você é o ATLAS, assistente executivo estilo JARVIS. É o fechamento do dia %s.
Gere um balanço do dia:
- Tom elegante, respeitoso, sóbrio e reflexivo.
- Faça um breve resumo do dia e sugira uma reflexão madura para descanso e planejamento de amanhã.
- Máximo 2 parágrafos. Sem broncas ou agressividade.`, todayStr)

	reqPayload := ChatCompletionRequest{
		Model: c.groqModel,
		Messages: []ChatMessagePayload{
			{Role: "system", Content: "Você é o ATLAS: assistente executivo refinado e inteligente estilo JARVIS."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
		MaxTokens:   350,
	}

	res, err := c.doRequest(ctx, reqPayload)
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

// AnalyzeWithModel executes inference with a targeted model, enabling multi-model Swarm distribution.
func (c *Client) AnalyzeWithModel(ctx context.Context, model, text string, maxTokens int) (*APIResponse, error) {
	if maxTokens <= 0 {
		maxTokens = 320
	}
	payload := ChatCompletionRequest{
		Model: model,
		Messages: []ChatMessagePayload{
			{Role: "system", Content: c.capabilityPrompt()},
			{Role: "user", Content: text},
		},
		Temperature: 0.3,
		MaxTokens:   maxTokens,
	}

	// 1. Tenta Groq com o modelo específico solicitado
	if !c.groqPool.IsEmpty() {
		res, err := c.executePoolAPI(ctx, "https://api.groq.com/openai/v1/chat/completions", c.groqPool, payload)
		if err == nil && res != nil {
			return res, nil
		}
	}

	// 2. Fallback para DeepAnalyze
	return c.DeepAnalyze(ctx, text, nil)
}

// DeepAnalyze delegates heavy research, complex analysis or strategic evaluations.
// Prioriza Groq com OpenAI GPT-OSS 120B (sub-segundo) e faz fallback para Qwen 27B e NVIDIA NIM.
func (c *Client) DeepAnalyze(ctx context.Context, text string, history []database.ChatMessage) (*APIResponse, error) {
	systemPrompt := `Você é o ATLAS: assistente executivo e estrategista de alto nível estilo JARVIS.
Você foi acionado para uma análise aprofundada, pesquisa ou raciocínio complexo.
Responda com extrema sofisticação, profundidade analítica, clareza cirúrgica e elegância, apontando dados, riscos, custos de oportunidade e passos recomendados.`
	systemPrompt += c.capabilityPrompt()

	messages := []ChatMessagePayload{
		{Role: "system", Content: systemPrompt},
	}
	messages = append(messages, buildCleanHistory(history, text)...)

	payload := ChatCompletionRequest{
		Model:       "openai/gpt-oss-120b",
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   c.maxTokensHeavy,
	}

	// 1. Tenta Groq primeiro (velocidade relâmpago de ~0.8s) com GPT-OSS 120B
	if !c.groqPool.IsEmpty() {
		res, err := c.executePoolAPI(ctx, "https://api.groq.com/openai/v1/chat/completions", c.groqPool, payload)
		if err == nil && res != nil {
			return res, nil
		}
		// Fallback no Groq para Qwen 27B
		payload.Model = c.groqModel
		res, err = c.executePoolAPI(ctx, "https://api.groq.com/openai/v1/chat/completions", c.groqPool, payload)
		if err == nil && res != nil {
			return res, nil
		}
	}

	// 2. Fallback para NVIDIA NIM caso Groq atinja rate limit ou oscile
	if !c.nvidiaPool.IsEmpty() {
		payload.Model = c.nvidiaModel
		return c.executePoolAPI(ctx, "https://integrate.api.nvidia.com/v1/chat/completions", c.nvidiaPool, payload)
	}

	return nil, fmt.Errorf("nenhum provedor de IA disponível para análise profunda")
}

// ResearchAnalyze executes unconstrained deep research with expanded output tokens.
func (c *Client) ResearchAnalyze(ctx context.Context, text string, history []database.ChatMessage) (*APIResponse, error) {
	systemPrompt := `Você é o ATLAS no MODO PESQUISA & SÍNTESE APROFUNDADA (JARVIS Research Core).
Você tem autorização para produzir respostas completas, extensas, densas e minuciosas, sem cortes ou truncamentos prematuros.
Diretrizes do Perfil de Pesquisa:
1. Traga conceitos fundamentais, contexto histórico/técnico e o estado da arte.
2. Apresente múltiplos pontos de vista, referências, artigos e escolas de pensamento relevantes.
3. Estruture em tópicos claros, com introdução, desenvolvimento analítico, exemplos práticos e conclusões executivas.
4. Jamais economize explicações essenciais: priorize riqueza conceitual, precisão e clareza analítica.`
	systemPrompt += c.capabilityPrompt()

	messages := []ChatMessagePayload{
		{Role: "system", Content: systemPrompt},
	}
	messages = append(messages, buildCleanHistory(history, text)...)

	payload := ChatCompletionRequest{
		Model:       "openai/gpt-oss-120b",
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   c.maxTokensResearch,
	}

	// 1. Tenta Groq primeiro (sub-segundo)
	if !c.groqPool.IsEmpty() {
		res, err := c.executePoolAPI(ctx, "https://api.groq.com/openai/v1/chat/completions", c.groqPool, payload)
		if err == nil && res != nil {
			return res, nil
		}
		payload.Model = c.groqModel
		res, err = c.executePoolAPI(ctx, "https://api.groq.com/openai/v1/chat/completions", c.groqPool, payload)
		if err == nil && res != nil {
			return res, nil
		}
	}

	// 2. Fallback para NVIDIA NIM
	if !c.nvidiaPool.IsEmpty() {
		payload.Model = c.nvidiaModel
		return c.executePoolAPI(ctx, "https://integrate.api.nvidia.com/v1/chat/completions", c.nvidiaPool, payload)
	}

	return nil, fmt.Errorf("nenhum provedor de IA disponível para pesquisa profunda")
}

func (c *Client) doRequest(ctx context.Context, payload ChatCompletionRequest) (*APIResponse, error) {
	// 1. Tenta Groq primeiro (velocidade relâmpago de ~0.3s) com pool de chaves
	if !c.groqPool.IsEmpty() {
		groqPayload := payload
		groqPayload.Model = c.groqModel
		res, err := c.executePoolAPI(ctx, "https://api.groq.com/openai/v1/chat/completions", c.groqPool, groqPayload)
		if err == nil && res != nil {
			return res, nil
		}
	}

	// 2. Fallback para NVIDIA NIM com pool de chaves caso Groq oscile
	if !c.nvidiaPool.IsEmpty() {
		nvidiaPayload := payload
		nvidiaPayload.Model = c.nvidiaModel
		return c.executePoolAPI(ctx, "https://integrate.api.nvidia.com/v1/chat/completions", c.nvidiaPool, nvidiaPayload)
	}

	return nil, fmt.Errorf("nenhum provedor de IA (Groq ou NVIDIA) configurado com sucesso")
}

func (c *Client) executePoolAPI(ctx context.Context, url string, keyPool *pool.KeyPool, payload ChatCompletionRequest) (*APIResponse, error) {
	if keyPool.IsEmpty() {
		return nil, fmt.Errorf("nenhuma chave disponível para %s", url)
	}

	keys := keyPool.OrderedKeys()
	var lastErr error

	for _, key := range keys {
		res, err := c.executeAPI(ctx, url, key, payload)
		if err == nil && res != nil {
			return res, nil
		}
		lastErr = err
	}

	return nil, lastErr
}

func (c *Client) executeAPI(ctx context.Context, url, apiKey string, payload ChatCompletionRequest) (*APIResponse, error) {
	// Preserve the provider quota budget for prose. Structured extraction uses
	// its own configured budget so longer action JSON is not silently truncated.
	if strings.Contains(url, "groq") && !payload.Structured && payload.MaxTokens > 350 {
		payload.MaxTokens = 350
	}
	if payload.MaxTokens <= 0 {
		payload.MaxTokens = 300
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	callCtx := ctx
	if strings.Contains(url, "nvidia") {
		var nvidiaCancel context.CancelFunc
		callCtx, nvidiaCancel = context.WithTimeout(ctx, 4*time.Second)
		defer nvidiaCancel()
	}

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do req to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api status %d: %s", resp.StatusCode, string(respBody))
	}

	var res ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(res.Choices) == 0 {
		return nil, fmt.Errorf("api returned 0 choices")
	}
	if res.Choices[0].FinishReason == "length" {
		return nil, fmt.Errorf("resposta truncada pelo limite de tokens")
	}

	provider := "groq"
	if strings.Contains(url, "nvidia") {
		provider = "nvidia"
	}

	content := strings.TrimSpace(res.Choices[0].Message.Content)
	if content == "" && res.Choices[0].Message.Reasoning != "" {
		content = strings.TrimSpace(res.Choices[0].Message.Reasoning)
	}

	return &APIResponse{
		Text:         content,
		Provider:     provider,
		Model:        payload.Model,
		PromptTokens: res.Usage.PromptTokens,
		ComplTokens:  res.Usage.CompletionTokens,
		TotalTokens:  res.Usage.TotalTokens,
	}, nil
}
