package documents

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractPlainText(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "sample.txt")
	sampleContent := "Relatório Executivo ATLAS:\n1. Metas concluídas: 5\n2. Pendências: 0"
	if err := os.WriteFile(txtFile, []byte(sampleContent), 0644); err != nil {
		t.Fatalf("failed to write sample file: %v", err)
	}

	text, err := ExtractText(context.Background(), txtFile, "sample.txt", 1000)
	if err != nil {
		t.Fatalf("ExtractText failed: %v", err)
	}

	if text != sampleContent {
		t.Errorf("expected '%s', got '%s'", sampleContent, text)
	}
}

func TestExtractDocx(t *testing.T) {
	tmpDir := t.TempDir()
	docxPath := filepath.Join(tmpDir, "document.docx")

	f, err := os.Create(docxPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	xmlContent := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Primeiro parágrafo do documento ATLAS.</w:t></w:r></w:p>
    <w:p><w:r><w:t>Segundo parágrafo com informações financeiras.</w:t></w:r></w:p>
  </w:body>
</w:document>`
	if _, err := w.Write([]byte(xmlContent)); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	f.Close()

	text, err := ExtractText(context.Background(), docxPath, "document.docx", 5000)
	if err != nil {
		t.Fatalf("ExtractText docx failed: %v", err)
	}

	if !strings.Contains(text, "Primeiro parágrafo do documento ATLAS.") || !strings.Contains(text, "Segundo parágrafo com informações financeiras.") {
		t.Errorf("unexpected docx content: %s", text)
	}
}

func TestExtractXlsx(t *testing.T) {
	tmpDir := t.TempDir()
	xlsxPath := filepath.Join(tmpDir, "planilha.xlsx")

	f, err := os.Create(xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	// sharedStrings.xml
	sw, err := zw.Create("xl/sharedStrings.xml")
	if err != nil {
		t.Fatal(err)
	}
	sharedXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <si><t>Item</t></si>
  <si><t>Valor</t></si>
  <si><t>Aluguel</t></si>
</sst>`
	if _, err := sw.Write([]byte(sharedXML)); err != nil {
		t.Fatal(err)
	}

	// sheet1.xml
	shw, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	sheetXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row r="1">
      <c r="A1" t="s"><v>0</v></c>
      <c r="B1" t="s"><v>1</v></c>
    </row>
    <row r="2">
      <c r="A2" t="s"><v>2</v></c>
      <c r="B2"><v>2500</v></c>
    </row>
  </sheetData>
</worksheet>`
	if _, err := shw.Write([]byte(sheetXML)); err != nil {
		t.Fatal(err)
	}

	zw.Close()
	f.Close()

	text, err := ExtractText(context.Background(), xlsxPath, "planilha.xlsx", 5000)
	if err != nil {
		t.Fatalf("ExtractText xlsx failed: %v", err)
	}

	if !strings.Contains(text, "Item | Valor") || !strings.Contains(text, "Aluguel | 2500") {
		t.Errorf("unexpected xlsx content: %s", text)
	}
}

func TestExtractOdfAndEpub(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. ODT
	odtPath := filepath.Join(tmpDir, "texto.odt")
	f1, err := os.Create(odtPath)
	if err != nil {
		t.Fatal(err)
	}
	zw1 := zip.NewWriter(f1)
	cw, err := zw1.Create("content.xml")
	if err != nil {
		t.Fatal(err)
	}
	odtXML := `<?xml version="1.0" encoding="UTF-8"?>
<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">
  <office:body>
    <office:text>
      <text:p>Relatório OpenDocument ODT gerado no LibreOffice.</text:p>
    </office:text>
  </office:body>
</office:document-content>`
	cw.Write([]byte(odtXML))
	zw1.Close()
	f1.Close()

	odtText, err := ExtractText(context.Background(), odtPath, "texto.odt", 5000)
	if err != nil {
		t.Fatalf("ExtractText odt failed: %v", err)
	}
	if !strings.Contains(odtText, "Relatório OpenDocument ODT gerado no LibreOffice.") {
		t.Errorf("unexpected odt content: %s", odtText)
	}

	// 2. EPUB
	epubPath := filepath.Join(tmpDir, "livro.epub")
	f2, err := os.Create(epubPath)
	if err != nil {
		t.Fatal(err)
	}
	zw2 := zip.NewWriter(f2)
	ew, err := zw2.Create("OEBPS/chapter1.xhtml")
	if err != nil {
		t.Fatal(err)
	}
	epubHTML := `<html><body><h1>Capítulo I</h1><p>Era uma vez um assistente executivo ágil.</p></body></html>`
	ew.Write([]byte(epubHTML))
	zw2.Close()
	f2.Close()

	epubText, err := ExtractText(context.Background(), epubPath, "livro.epub", 5000)
	if err != nil {
		t.Fatalf("ExtractText epub failed: %v", err)
	}
	if !strings.Contains(epubText, "Capítulo I") || !strings.Contains(epubText, "Era uma vez um assistente executivo ágil.") {
		t.Errorf("unexpected epub content: %s", epubText)
	}
}

func TestExtractRtf(t *testing.T) {
	tmpDir := t.TempDir()
	rtfPath := filepath.Join(tmpDir, "sample.rtf")

	rtfContent := `{\rtf1\ansi\deff0 {\fonttbl{\f0 Arial;}} \viewkind4\uc1\pard\lang1046\f0\fs20 Ol\'e1 mundo ATLAS! \par Segundo par\'e1grafo.\par}`
	if err := os.WriteFile(rtfPath, []byte(rtfContent), 0644); err != nil {
		t.Fatal(err)
	}

	text, err := ExtractText(context.Background(), rtfPath, "sample.rtf", 5000)
	if err != nil {
		t.Fatalf("ExtractText rtf failed: %v", err)
	}

	if !strings.Contains(text, "mundo ATLAS!") || !strings.Contains(text, "Segundo") {
		t.Errorf("unexpected rtf content: %s", text)
	}
}

func TestIsAudioAndSupported(t *testing.T) {
	if !IsAudio("audio.mp3") || !IsAudio("record.m4a") || !IsAudio("voice.wav") {
		t.Errorf("expected audio formats to be detected")
	}

	if IsAudio("file.pdf") || IsAudio("data.csv") || IsAudio("contrato.docx") {
		t.Errorf("expected non-audio to return false")
	}

	// Documentos suportados
	supported := []string{
		"doc.pdf", "table.csv", "code.go", "contrato.docx",
		"balanco.xlsx", "apresentacao.pptx", "texto.odt",
		"tabela.ods", "slides.odp", "livro.epub", "documento.rtf",
		"legenda.srt", "relatorio.doc",
	}

	for _, s := range supported {
		if !IsSupportedDocument(s) {
			t.Errorf("expected '%s' to be supported", s)
		}
	}

	if IsSupportedDocument("archive.zip") || IsSupportedDocument("app.exe") || IsSupportedDocument("imagem.png") {
		t.Errorf("expected unsupported files to return false")
	}
}
