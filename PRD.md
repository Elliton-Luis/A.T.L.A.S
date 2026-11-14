# PRD: ATLAS — Assistente Pessoal Executivo e Socrático

## 1. Visão Geral
Assistente de organização pessoal operado via Telegram (voz e texto). Seu núcleo é tornar atividades, hábitos e estudos executáveis: o usuário escolhe a atividade e o tempo disponível; o ATLAS organiza passos práticos e gratuitos, registra o checklist e acompanha a conclusão. Agenda com margem de preparo/saída, briefing pessoal e ferramentas auxiliares complementam esse fluxo.

---

## 2. Princípios de Arquitetura e Engenharia
- **Máximo de Algoritmos, Mínimo de IA:** IA usada estritamente para extração de entidades e formulação socrática. Cálculos temporais, validações e alertas são funções matemáticas puras e determinísticas.
- **Latência Mínima (Fast & Reliable):** Respostas de áudio e texto em menos de 1 segundo.
- **Zero Memory Leaks & Zero Latência de Rede no Banco:** SQLite local via WAL mode (Write-Ahead Logging).

---

## 3. Stack Tecnológica
- **Interface:** Telegram Bot API (Webhook HTTPS).
- **Backend:** **Go (Golang)** — Binário único compilado autônomo, servidor HTTP nativo com timeouts rígidos, motor de agendamento em Goroutines concorrentes, consumo < 15MB RAM.
- **STT (Speech-to-Text):** Groq API (Whisper-large-v3, latência ~300ms).
- **NLU / Raciocínio:** NVIDIA NIM API (Llama-3.1/3.3-70B-Instruct).
- **Banco de Dados:** SQLite local (arquivo `.sqlite` via WAL mode e permissões estritas 0600).
- **Segurança:** Whitelist estrita por `chat_id`, validação constant-time de `X-Telegram-Bot-Api-Secret-Token`, sanitização de logs e contenção de vazamentos.

---

## 4. Regras de Negócio e Algoritmos

### 4.1. Algoritmo Anti-Atraso (Margem de Segurança Determinística)
- Para cada compromisso com horário oficial $H_{oficial}$:
  $$T_{deslocamento\_real} = \lceil T_{deslocamento} \times 1.15 \rceil$$
  $$T_{preparo\_real} = \lceil T_{preparo} \times 1.15 \rceil$$
  $$\text{Buffer 2 (Hora de Sair)} = H_{oficial} - T_{deslocamento\_real}$$
  $$\text{Buffer 1 (Hora de Preparo/Banho)} = \text{Buffer 2} - T_{preparo\_real}$$

### 4.2. Personalidade Socrática e Registro de Ideias
- Não atua como assistente passivo/capacho.
- Toda ideia registrada é salva no banco e confrontada com uma pergunta crítica sobre premissas, custo de oportunidade e viabilidade prática.

### 4.3. Memória de Conversação (Context Window Local)
- A API da LLM é stateless. O backend mantém uma janela deslizante das últimas $N$ interações no SQLite (`historico_chat`) e as injeta no payload da requisição.

### 4.4. Planejamento prático diário
- A duração é obrigatoriamente escolhida pelo usuário; sem ela, o bot pergunta antes de criar tarefas.
- Cada passo tem uma ação concreta, minutos e um critério observável de conclusão.
- Prática e gratuidade são requisitos obrigatórios. Não são usados passos dependentes de compras, assinaturas ou testes promocionais.
- A soma dos minutos precisa ser igual à duração escolhida. O número de passos é proporcional ao bloco de tempo, limitado a oito.
- O plano informa dificuldade e pode incluir referências públicas gratuitas do catálogo do aplicativo.
- Os passos usam o checklist existente. O histórico é preservado por data, sem contar uma tarefa de origem novamente junto com seus passos.

### 4.5. Navegação e exportação
- A ajuda principal apresenta apenas setores. Os comandos detalhados aparecem ao abrir um setor.
- A exportação PDF usa dados reais da agenda, das rotinas e do checklist, com períodos diário, semanal ou mensal.
- Atividades sem horário permanecem identificadas como não agendadas. Uma duração estimada nunca se transforma automaticamente em um horário de calendário.

---

## 5. Esquema de Banco de Dados (SQLite)

```sql
PRAGMA journal_mode = WAL;

-- Compromissos com buffers determinísticos
CREATE TABLE IF NOT EXISTS eventos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    descricao TEXT NOT NULL,
    horario_oficial DATETIME NOT NULL,
    horario_buffer_1 DATETIME NOT NULL, -- Alerta de preparo (+15%)
    horario_buffer_2 DATETIME NOT NULL, -- Alerta de saída (+15%)
    notificado_buffer_1 INTEGER DEFAULT 0,
    notificado_buffer_2 INTEGER DEFAULT 0,
    status TEXT CHECK(status IN ('pendente', 'em_andamento', 'concluido', 'esquecido')) DEFAULT 'pendente',
    criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Metas e Objetivos de Vida
CREATE TABLE IF NOT EXISTS objetivos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    titulo TEXT NOT NULL,
    prazo DATETIME,
    progresso_pct INTEGER DEFAULT 0,
    status TEXT CHECK(status IN ('ativo', 'concluido', 'pausado')) DEFAULT 'ativo',
    criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Ideias e Questionamentos Socráticos
CREATE TABLE IF NOT EXISTS ideias (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    titulo TEXT NOT NULL,
    conteudo TEXT NOT NULL,
    questionamento_socratico TEXT NOT NULL,
    status TEXT CHECK(status IN ('bruta', 'refinada', 'descartada', 'em_execucao')) DEFAULT 'bruta',
    criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Contexto contínuo para LLM
CREATE TABLE IF NOT EXISTS historico_chat (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    role TEXT CHECK(role IN ('user', 'assistant')) NOT NULL,
    content TEXT NOT NULL,
    criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_eventos_busca ON eventos(
    notificado_buffer_1,
    notificado_buffer_2,
    horario_buffer_1,
    horario_buffer_2
);
```

---

## 6. Fluxos de Execução

### Fluxo 1: Ingestão de Comandos (Voz ou Texto)
1. Telegram despacha POST HTTPS para `/webhook`.
2. Backend valida `X-Telegram-Bot-Api-Secret-Token`.
3. Se for áudio: baixa o binário e envia ao Groq Whisper (`pt`, ~300ms) obtendo o texto.
4. Consulta últimas mensagens em `historico_chat`.
5. Envia prompt para NVIDIA NIM (Llama 3.3 70B) classificando a intenção (`evento`, `objetivo`, `ideia`, `conversa`).
6. **Se evento:** Aplica o algoritmo de +15% de margem e persiste em `eventos`.
7. **Se ideia:** Persiste em `ideias` junto com a refutação/pergunta socrática gerada.
8. Salva interação em `historico_chat` e responde ao usuário.

### Fluxo 2: Motor de Ação Proativa (Ticker / CRON a cada 60s)
1. Varre `eventos` com `horario_buffer_1 <= now` ou `horario_buffer_2 <= now` não notificados.
2. Dispara mensagem incisiva no Telegram (`"Hora de se arrumar"`, `"Hora de sair"`).
3. Pós-evento: pergunta se o compromisso foi concluído ou se precisa ser reagendado.

### Fluxo 3: Backup Automático Diário
- Às 03:00, o arquivo `.sqlite` é enviado via API do Telegram (`sendDocument`) como backup offsite.

---

## 7. Roadmap de Entrega
- **V1 (MVP Ágil):** Servidor HTTP webhook, integração Groq Whisper (STT), NVIDIA NIM (NLU socrático), algoritmo de margem (+15%) e persistência SQLite.
- **V2 (Proatividade):** Rotina periódica ativa de disparo de alertas de buffer e cobrança pós-evento.
- **V3 (Expansão Unilateral):** Sincronização unidirecional push-only com Google Calendar (SQLite permanece como fonte única da verdade).
