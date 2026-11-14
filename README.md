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
