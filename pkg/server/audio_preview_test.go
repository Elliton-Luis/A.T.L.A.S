package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"atlas/pkg/stt"
	"atlas/pkg/telegram"
)

func TestAudioTranscriptionIsFirstAndPrecedesAnyMutation(t *testing.T) {
	for _, kind := range []string{"voice", "audio", "document"} {
		t.Run(kind, func(t *testing.T) {
			s := automationServer(t)
			s.sttClient = stt.NewClient("test")
			var messages []string
			var actions []string
			previous := http.DefaultTransport
			http.DefaultTransport = auditTransport(func(r *http.Request) (*http.Response, error) {
				body := `{"ok":true}`
				switch {
				case strings.HasSuffix(r.URL.Path, "/getFile"):
					body = `{"ok":true,"result":{"file_path":"voice.ogg"}}`
				case strings.Contains(r.URL.Path, "/file/"):
					body = "audio"
				case strings.HasSuffix(r.URL.Path, "/audio/transcriptions"):
					body = `{"text":"/tarefa Revisar *Go*"}`
				case strings.HasSuffix(r.URL.Path, "/sendMessage"):
					var payload map[string]interface{}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					text, _ := payload["text"].(string)
					messages = append(messages, text)
					actions = append(actions, "message")
					if len(messages) == 1 {
						if text != "Transcrição: '/tarefa Revisar *Go*'" {
							t.Fatal("primeira mensagem não foi a transcrição", text)
						}
						if _, ok := payload["parse_mode"]; ok {
							t.Fatal("transcrição interpretada como Markdown")
						}
						tasks, _ := s.db.DailyTasks(context.Background(), s.today())
						if len(tasks) != 0 {
							t.Fatal("alteração aconteceu antes da transcrição")
						}
					}
				case strings.HasSuffix(r.URL.Path, "/sendChatAction"):
					actions = append(actions, "typing")
				default:
					t.Fatalf("chamada inesperada: %s", r.URL.Host+r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			message := &telegram.Message{Chat: telegram.Chat{ID: 1}}
			switch kind {
			case "voice":
				message.Voice = &telegram.Voice{FileID: "x"}
			case "audio":
				message.Audio = &telegram.Audio{FileID: "x"}
			case "document":
				message.Document = &telegram.Document{FileID: "x", FileName: "voice.mp3"}
			}
			s.processIncomingMessage(context.Background(), message)
			tasks, err := s.db.DailyTasks(context.Background(), s.today())
			if err != nil || len(tasks) != 1 || len(messages) < 2 || len(actions) == 0 || actions[0] != "message" {
				t.Fatal("ordem ou execução incorreta", messages, actions, tasks, err)
			}
		})
	}
}

func TestFailedTranscriptionDeliveryBlocksExecution(t *testing.T) {
	s := automationServer(t)
	s.sttClient = stt.NewClient("test")
	previous := http.DefaultTransport
	http.DefaultTransport = auditTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"ok":true}`
		status := 200
		switch {
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			body = `{"ok":true,"result":{"file_path":"voice.ogg"}}`
		case strings.Contains(r.URL.Path, "/file/"):
			body = "audio"
		case strings.HasSuffix(r.URL.Path, "/audio/transcriptions"):
			body = `{"text":"/tarefa Não executar"}`
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			status = 429
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	s.processIncomingMessage(context.Background(), &telegram.Message{Chat: telegram.Chat{ID: 1}, Voice: &telegram.Voice{FileID: "x"}})
	tasks, _ := s.db.DailyTasks(context.Background(), s.today())
	if len(tasks) != 0 {
		t.Fatal("comando executado sem entregar transcrição")
	}
}
