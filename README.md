# ATLAS — Assistente Pessoal Executivo e Socrático (Go)

ATLAS é um assistente pessoal estilo JARVIS, combativo e hiper-racional, operado 100% via Telegram (voz e texto), construído em **Go (Golang)** como um binário compilado autônomo.

---

## 🛡️ Pilares de Segurança e Performance

- **Zero Vazamentos:** O webhook valida o `chat_id` do remetente. Mensagens vindas de qualquer outro chat são ignoradas silenciosamente sem resposta.
- **Validação Segura de Webhook:** O cabeçalho `X-Telegram-Bot-Api-Secret-Token` é validado em tempo constante (`subtle.ConstantTimeCompare`).
- **Arquivos Temporários Seguros:** Áudios baixados são salvos em arquivos com permissão `0600` e removidos imediatamente após a transcrição.
- **Banco Blindado:** SQLite local em modo `WAL`, queries parametrizadas (anti SQL-injection) e permissões restritas.
- **Alta Velocidade:** Servidor HTTP nativo em Go, sem PHP-FPM, sem dependências externas de runtime, consumo de ~11MB de RAM.

---git

## ⚡ Comandos Rápidos no Telegram

- `/ajuda` ou `/help` — Exibe todos os comandos disponíveis.
- `/rotinas` — Lista as rotinas proativas cadastradas e seus horários.
- `/rotina <id> horario HH:MM` — Altera o horário de disparo de uma rotina.
- `/rotina <id> toggle` — Ativa ou desativa uma rotina.
- `/hoje` — Exibe a agenda de hoje com os alertas de preparo e saída (+15% de margem).
- `/metas` — Lista os objetivos em andamento.
- `/ideias` — Lista ideias anotadas e seus questionamentos socráticos.
- `/backup` — Dispara backup manual imediato do banco SQLite para o Telegram.

---

## 🚀 Como Executar

### 1. Configurar o `.env`
Copie `.env.example` para `.env` e preencha as credenciais:
```bash
cp .env.example .env
nano .env
```

### 2. Compilar e Rodar
```bash
make build
make run
```
Ou diretamente:
```bash
go run ./cmd/atlas
```

### 3. Configurar Webhook no Telegram
```bash
curl -F "url=https://seu-dominio.com/webhook" \
     -F "secret_token=SEU_SECRET_TOKEN" \
     https://api.telegram.org/botSEU_TOKEN/setWebhook
```

## Briefing pessoal diário

O briefing reúne os itens escolhidos pelo usuário, na ordem configurada. Exemplos em linguagem natural:

- "Configure meu briefing às 07:00 com liturgia, santo do dia, um versículo sobre confiança, todos os compromissos, checklist e notícias sobre tecnologia."
- "Inclua no briefing este pensamento: focar no essencial."
- "Adicione um resumo dos meus emails de hoje ao briefing."
- "Mude o tema das notícias do briefing para economia."
- "Remova o santo do dia do briefing."

Comandos de consulta e manutenção:

- `/briefing` — gera o briefing agora.
- `/briefing config` — mostra composição e horário de envio.
- `/briefing configurar <descrição>` — define a composição completa.
- `/briefing adicionar <descrição>` — acrescenta itens preservando os anteriores.
- `/briefing remover <número>` — remove um item da composição.
- `/briefing horario 07:00` — define o envio diário.

Os temas bíblicos são convertidos em referências durante a configuração; as citações são consultadas nas fontes bíblicas existentes. Pensamentos fornecidos pelo usuário são guardados como texto, não gerados pela IA. Também é possível incluir a última ideia registrada.

Agenda e checklist são consultados novamente a cada briefing. Liturgia, santo e passagem bíblica usam cache por data; notícias usam cache de até 30 minutos. Falhas de uma fonte aparecem no respectivo item sem impedir os demais. No modo serverless, o cron externo precisa continuar chamando `/api/cron/tick`.

## Checklist de metas diárias

Tarefas são separadas dos compromissos com horário e das metas de longo prazo.

- "Anote minhas tarefas de hoje: estudar Go, treinar e ler."
- "Anote que fiz estudar Go."
- "Quais são minhas metas diárias?"
- "O que fiz ontem?"

Comandos equivalentes:

- `/tarefa Estudar Go; Treinar; Ler` — anota várias tarefas.
- `/tarefas` ou `/checklist` — lista o dia atual com marcações de conclusão.
- `/feito <nome ou ID>` — conclui uma tarefa.
- `/tarefas ontem` ou `/tarefas 2026-11-14` — consulta outro dia.

Cada dia tem seu próprio checklist. O dia seguinte começa vazio; dias anteriores permanecem no banco, inclusive com horário de conclusão. Repetir uma tarefa no mesmo dia não duplica o registro nem desfaz sua conclusão. Nomes ambíguos exigem identificação antes de marcar qualquer item.

## Consultas com resumo

"Veja meus emails e faça um resumo" retorna os emails relevantes e um parágrafo de síntese usando a mesma leitura e uma única inferência. A contagem informa a amostra consultada, não o total da caixa. Filtros como remetente e período são preservados.

`/resumo <texto>` resume o texto informado; `/resumo emails de hoje` consulta e resume emails. Consultas combinadas podem compartilhar os resultados em uma síntese final.
