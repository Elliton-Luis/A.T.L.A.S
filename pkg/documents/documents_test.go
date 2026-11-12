package documents

import (
	"context"
	"os"
	"path/filepath"
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

func TestIsAudioAndSupported(t *testing.T) {
	if !IsAudio("audio.mp3") || !IsAudio("record.m4a") || !IsAudio("voice.wav") {
		t.Errorf("expected audio formats to be detected")
	}

	if IsAudio("file.pdf") || IsAudio("data.csv") {
		t.Errorf("expected non-audio to return false")
	}

	if !IsSupportedDocument("doc.pdf") || !IsSupportedDocument("table.csv") || !IsSupportedDocument("code.go") {
		t.Errorf("expected documents to be supported")
	}

	if IsSupportedDocument("archive.zip") || IsSupportedDocument("app.exe") {
		t.Errorf("expected unsupported files to return false")
	}
}
