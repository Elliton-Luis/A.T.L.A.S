package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"atlas/pkg/email"
	"atlas/pkg/nlu"
	"atlas/pkg/sonar"
	"atlas/pkg/telegram"
)

func TestSelfKnowledgeQuestionsStayReadOnlyAndConcreteRequestsExecute(t *testing.T) {
	for _, test := range []struct{ text, command string }{
		{"o que você consegue fazer?", "/capacidades"},
		{"quais são suas funções?", "/capacidades"},
		{"você consegue criar um PDF?", "/capacidades pdf"},
		{"você consegue ler PDF?", "/capacidades documentos"},
		{"você tem integração com email?", "/capacidades emails"},
		{"você consegue usar Linux?", "/capacidades terminal"},
		{"envie um email para João", "/capacidades emails"},
		{"apague meus emails", "/capacidades emails"},
	} {
		if command, ok := matchFastPath(test.text); !ok || command != test.command {
			t.Errorf("%q: %s", test.text, command)
		}
	}
	for _, text := range []string{"quero minha rotina em PDF", "você consegue gerar minha rotina em PDF?", "veja meus emails e faça um resumo"} {
		if command, ok := matchFastPath(text); !ok || strings.HasPrefix(command, "/capacidades") {
			t.Errorf("pedido concreto virou apenas descrição: %s => %s", text, command)
		}
	}
	if sectorHelp("") != helpIndex {
		t.Fatal("ajuda deixou de ser índice por setores")
	}
}

func TestCapabilitiesUseRuntimeWithoutLeakingCredentialsOrCallingAI(t *testing.T) {
	s := automationServer(t)
	s.cfg.GroqAPIKey = "GROQ_SECRET_TEST"
	s.cfg.IMAPPass = "IMAP_SECRET_TEST"
	s.cfg.TelegramBotToken = "TELEGRAM_SECRET_TEST"
	s.cfg.SonarBridgeToken = "BRIDGE_SECRET_TEST"
	s.emailReader = email.NewReader(email.Config{Host: "imap.example.com", User: "PRIVATE_USER_TEST", Password: s.cfg.IMAPPass})
	text := s.describeCapabilities("emails")
	if !strings.Contains(text, "configurado") {
		t.Fatal(text)
	}
	for _, secret := range []string{s.cfg.GroqAPIKey, s.cfg.IMAPPass, s.cfg.TelegramBotToken, s.cfg.SonarBridgeToken, "PRIVATE_USER_TEST"} {
		if strings.Contains(text, secret) {
			t.Fatal("credencial exposta")
		}
	}
	calls := mockPersonalAPI(t, `{}`)
	s.processIncomingMessage(context.Background(), &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "o que você consegue fazer?"})
	if *calls != 0 {
		t.Fatal("autodescrição consumiu IA")
	}
}

func TestUnsupportedEmailOperationNeverFetchesInbox(t *testing.T) {
	s := automationServer(t)
	inbox := &fakeInbox{}
	s.emailReader = inbox
	calls := mockPersonalAPI(t, `{}`)
	s.processIncomingMessage(context.Background(), &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "apague meus emails"})
	if inbox.calls != 0 || *calls != 0 {
		t.Fatal("ação não suportada virou leitura", inbox.calls, *calls)
	}
}

func TestNLUTerminalActionReachesBridgeAndDoesNotFakeSuccess(t *testing.T) {
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":false,"exit_code":1,"output":"","error":"comando recusado"}`)
	}))
	defer bridge.Close()
	s := automationServer(t)
	s.sonarClient = sonar.NewClient(bridge.URL, "test", "/missing-cli", "")
	result := s.executeAppAction(context.Background(), &nlu.IntentResult{Acao: "executar_terminal", Termo: "test-command"})
	if !strings.Contains(result, "código 1") || strings.Contains(result, "sucesso") {
		t.Fatal(result)
	}
}
