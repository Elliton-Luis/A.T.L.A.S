package server

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"atlas/pkg/nlu"
)

func TestPDFExportsActualTimesAndUnscheduledTasks(t *testing.T) {
	s := automationServer(t)
	s.cfg.Location = time.FixedZone("BRT", -3*60*60)
	ctx := context.Background()
	date := s.today()
	if _, err := s.db.InsertEvento(ctx, "Consulta dentista", date+" 10:00:00", date+" 08:50:00", date+" 09:31:00"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.AddDailyTasks(ctx, date, []string{"Estudar inglês"}); err != nil {
		t.Fatal(err)
	}
	document, err := s.pdfDocument(ctx, &nlu.IntentResult{Secoes: []string{"rotina"}, Periodo: "dia"})
	if err != nil || len(document.Sections) != 1 {
		t.Fatal(document, err)
	}
	found := false
	for _, row := range document.Sections[0].Rows {
		if row.Activity == "Consulta dentista" {
			found = true
			if row.Time != "10:00" || row.Prep != "08:50" || row.Leave != "09:31" {
				t.Fatal("horários incorretos", row)
			}
		}
	}
	if !found || !strings.Contains(strings.Join(document.Sections[0].Notes, "\n"), "sem horário definido") {
		t.Fatal("tarefa sem horário perdeu sua condição", document)
	}
	agenda, err := s.pdfDocument(ctx, &nlu.IntentResult{Secoes: []string{"agenda"}, Periodo: "dia"})
	if err != nil || strings.Contains(strings.Join(agenda.Sections[0].Notes, "\n"), "Estudar inglês") {
		t.Fatal("seção não solicitada incluída", agenda, err)
	}
}

func TestPDFIsUploadedAsDocumentAndTempFileRemoved(t *testing.T) {
	s := automationServer(t)
	var document []byte
	var filename string
	previous := http.DefaultTransport
	http.DefaultTransport = auditTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/sendDocument") {
			t.Errorf("esperava envio de documento: %s", r.URL.Path)
		}
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Error(err)
		}
		parts := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := parts.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				break
			}
			if part.FormName() == "document" {
				filename = part.FileName()
				document, _ = io.ReadAll(part)
			}
			part.Close()
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	result := s.pdfCommand(context.Background(), 1, "rotina hoje")
	if !strings.Contains(result, "PDF enviado") || !bytes.HasPrefix(document, []byte("%PDF-1.4")) {
		t.Fatal("PDF não entregue como arquivo", result)
	}
	if filename == "" {
		t.Fatal("arquivo sem nome")
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), filename)); !os.IsNotExist(err) {
		t.Fatal("arquivo temporário não foi removido", err)
	}
}

func TestPDFPeriodsAndExplicitSections(t *testing.T) {
	s := automationServer(t)
	document, err := s.pdfDocument(context.Background(), &nlu.IntentResult{Secoes: []string{"agenda"}, Data: "2026-02-15", Periodo: "mes"})
	if err != nil || len(document.Sections) != 28 || !strings.Contains(document.Subtitle, "01/02/2026 a 28/02/2026") {
		t.Fatal(document.Subtitle, err)
	}
	if _, err := s.pdfDocument(context.Background(), &nlu.IntentResult{Secoes: []string{"desconhecida"}}); err == nil {
		t.Fatal("informação desconhecida substituída por rotina")
	}
	if command, ok := matchFastPath("quero minha rotina em PDF"); !ok || !strings.HasPrefix(command, "/pdf ") {
		t.Fatal(command)
	}
	if command, ok := matchFastPath("analise este arquivo.pdf"); ok && strings.HasPrefix(command, "/pdf ") {
		t.Fatal("leitura de PDF virou exportação")
	}
}
