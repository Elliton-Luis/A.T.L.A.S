package exportpdf

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPDFCalendarPaginationAccentsAndTextExtraction(t *testing.T) {
	document := Document{Title: "ATLAS — Calendário pessoal", Subtitle: "14/11/2026 a 20/11/2026 | America/Sao_Paulo"}
	for i := 0; i < 12; i++ {
		document.Sections = append(document.Sections, Section{Title: "Sábado — 14/11/2026", Rows: []Row{{Time: "07:00", Activity: "Inglês: leitura (ação prática) e revisão", Prep: "06:30", Leave: "06:50"}}, Notes: []string{"Checklist sem horário definido: escrever 5 frases, duração de 30 minutos."}})
	}
	content, err := Render(document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(content, []byte("%PDF-1.4")) || !bytes.HasSuffix(content, []byte("%%EOF\n")) {
		t.Fatal("estrutura PDF ausente")
	}
	if bytes.Count(content, []byte("/Type /Page ")) < 2 {
		t.Fatal("paginação não ocorreu")
	}
	path := filepath.Join(t.TempDir(), "rotina.pdf")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	tool, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("pdftotext não instalado; estrutura e paginação verificadas")
	}
	output, err := exec.Command(tool, "-layout", path, "-").CombinedOutput()
	if err != nil {
		t.Fatalf("PDF inválido: %v %s", err, output)
	}
	for _, expected := range []string{"Calendário pessoal", "Inglês: leitura (ação prática)", "07:00", "06:30", "06:50", "30 minutos"} {
		if !strings.Contains(string(output), expected) {
			t.Errorf("texto perdido no PDF: %s\n%s", expected, output)
		}
	}
}
