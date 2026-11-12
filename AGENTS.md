# ATLAS — Diretrizes de Governança e Memória Contínua

Este arquivo define as regras fundamentais de desenvolvimento e comportamento do ecossistema ATLAS. Todo agente que interagir com esta codebase DEVE seguir estas instruções.

---

## 1. Regra de Ouro: Mapeamento Semântico Amplo de Termos Próximos
* **Nunca exija sintaxe rígida:** O usuário deve ser livre para falar em linguagem natural, áudio ou texto coloquial.
* **Mapeamento Próximo Universal:** Sempre que uma funcionalidade, ferramenta ou intenção for criada ou atualizada:
  1. **Sinônimos e Variações Gramaticais:** Mapear no `pkg/synonyms/synonyms.go` termos próximos, sinônimos, gírias, plural/singular, abreviações e variações com ou sem pontuação/hífen (ex: `e-mail`, `email`, `inbox`, `caixa de entrada`, `correio`, `mensagens`).
  2. **Captura Resiliente no Fast-Path Router:** Usar condições amplas e semânticas em `matchFastPath` (ex: verificar presença do conceito-chave ao invés de substrings rígidas ou frases idênticas).
  3. **Consciência Total na NLU:** Toda e qualquer ferramenta do robô DEVE estar explicitada no prompt de sistema (`pkg/nlu/nlu.go`) e no roteador de ações (`executeAppAction`), para que a IA jamais dê respostas pré-fabricadas dizendo que "não possui acesso" ou "não tem integração".

---

## 2. Princípios Arquiteturais (SOLID & KISS)
* **KISS (Keep It Simple, Stupid):** Prefira Go puro e biblioteca padrão (`net/http`, `crypto/tls`, `time`, `strings`). Zero frameworks inflados.
* **SOLID:** Separe responsabilidades em pacotes pequenos e claros (`pkg/email`, `pkg/crawler`, `pkg/interpreter`, `pkg/swarm`).
* **Zero Tokens & Baixa Latência:** Consultas rotineiras, comandos e buscas devem ser interceptados e resolvidos em tempo de máquina (<1ms) pelo Fast-Path sempre que possível.
* **Segurança por Padrão:** Ações de leitura de e-mail devem ser estritamente *read-only* (`EXAMINE` e `BODY.PEEK`), sem cliente SMTP ou escrita.

---

## 3. Gestão Inteligente de Compromissos e Exclusão
* Suportar exclusões contextuais: "delete isso", "cancele o último", "apague o dentista", "delete tudo".
* Suportar múltiplos dias e exceções na semana: "segunda a sexta", "exceto domingo e segunda".
* Se um compromisso tiver múltiplas ocorrências (recorrente), deletar todas ao receber ordem de exclusão.
