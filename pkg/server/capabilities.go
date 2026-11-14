package server

import (
	"os"
	"os/exec"
	"strings"

	"atlas/pkg/capabilities"
	"atlas/pkg/synonyms"
)

func (s *Server) capabilityRuntime() capabilities.Runtime {
	localPython := false
	for _, name := range []string{"python3", "python"} {
		if _, err := exec.LookPath(name); err == nil {
			localPython = true
			break
		}
	}
	terminal := false
	if s.sonarClient != nil {
		terminal = s.sonarClient.BridgeURL != ""
		if !terminal && s.sonarClient.LocalCLIPath != "" {
			if info, err := os.Stat(s.sonarClient.LocalCLIPath); err == nil && !info.IsDir() {
				_, err := exec.LookPath("php")
				terminal = err == nil
			}
		}
	}
	return capabilities.Runtime{AI: s.nluClient != nil && s.nluClient.HasProvider(), Voice: strings.Trim(s.cfg.GroqAPIKey, ", \t\r\n") != "", Email: s.emailReader != nil && s.emailReader.IsConfigured(), Terminal: terminal, PythonLocal: localPython, InternalScheduler: s.engine != nil && s.engine.Running(), Telegram: strings.TrimSpace(s.cfg.TelegramBotToken) != "", ScheduledChat: s.cfg.TelegramChatID != 0}
}

func (s *Server) describeCapabilities(topic string) string {
	return capabilities.Describe(s.capabilityRuntime(), topic)
}

// General capability questions do not execute the tool they are asking about.
// Concrete requests such as "gere minha rotina em PDF" retain their action path.
func capabilityFastPath(text string) (string, bool) {
	clean := strings.Trim(synonyms.RemoveAccents(strings.ToLower(text)), " .!?")
	for _, phrase := range []string{"o que voce consegue fazer", "o que consegue fazer", "o que voce pode fazer", "o que pode fazer", "o que voce sabe fazer", "quais suas capacidades", "quais sao suas capacidades", "quais suas funcionalidades", "quais sao suas funcionalidades", "quais suas funcoes", "quais sao suas funcoes", "quais ferramentas voce tem", "quais ferramentas tem", "quais integracoes voce tem", "no que voce pode me ajudar", "o que voce faz"} {
		if clean == phrase {
			return "/capacidades", true
		}
	}
	question := false
	for _, phrase := range []string{"voce consegue", "voce pode", "voce sabe", "voce tem acesso", "voce tem integracao", "voce e capaz", "consegue fazer", "nao consegue", "nao pode"} {
		if strings.Contains(clean, phrase) {
			question = true
			break
		}
	}
	if !question {
		return "", false
	}
	for _, object := range []string{"minha ", "minhas ", "meu ", "meus ", "para mim", "agora", "https://", "http://"} {
		if strings.Contains(clean, object) {
			return "", false
		}
	}
	if strings.Contains(clean, "pdf") && (strings.Contains(clean, "ler ") || strings.Contains(clean, "leitura") || strings.Contains(clean, "analisar")) {
		return "/capacidades documentos", true
	}
	for _, item := range capabilities.Catalog(capabilities.Runtime{}) {
		if capabilities.Match(item, clean) {
			return "/capacidades " + item.ID, true
		}
	}
	return "", false
}

func unsupportedEmailOperation(text string) bool {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	tokens := synonyms.TokenizeAndCanonicalize(clean)
	if !synonyms.MatchCanonical(tokens, "email") && !strings.Contains(clean, "caixa de entrada") {
		return false
	}
	if synonyms.MatchCanonical(tokens, "apagar") {
		return true
	}
	for _, phrase := range []string{"enviar um email", "envie um email", "mande um email", "mandar um email", "enviar email para", "envie email para", "responda o email", "responder email", "responda meus emails", "marque como lido", "marcar como lido", "arquive meus emails", "por email", "via email"} {
		if strings.Contains(clean, phrase) {
			return true
		}
	}
	return false
}
