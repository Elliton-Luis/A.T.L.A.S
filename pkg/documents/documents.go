package documents

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultMaxChars is the default maximum number of characters to extract from a document.
	DefaultMaxChars = 32000
)

// Supported text file extensions
var textExtensions = map[string]bool{
	".txt":      true,
	".md":       true,
	".markdown": true,
	".csv":      true,
	".tsv":      true,
	".json":     true,
	".yaml":     true,
	".yml":      true,
	".xml":      true,
	".html":     true,
	".htm":      true,
	".sql":      true,
	".py":       true,
	".go":       true,
	".sh":       true,
	".bash":     true,
	".js":       true,
	".ts":       true,
	".c":        true,
	".cpp":      true,
	".h":        true,
	".rs":       true,
	".log":      true,
	".ini":      true,
	".env":      true,
	".conf":     true,
	".toml":     true,
}

var audioExtensions = map[string]bool{
	".mp3":  true,
	".m4a":  true,
	".wav":  true,
	".ogg":  true,
	".oga":  true,
	".flac": true,
	".aac":  true,
	".opus": true,
}

// IsAudio checks if the file name has an audio extension.
func IsAudio(fileName string) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	return audioExtensions[ext]
}

// IsSupportedDocument checks if the file is a readable document (text or PDF).
func IsSupportedDocument(fileName string) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	return ext == ".pdf" || textExtensions[ext]
}

// ExtractText extracts readable plain text from a given local file.
func ExtractText(ctx context.Context, filePath, fileName string, maxChars int) (string, error) {
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}

	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(filePath))
	}

	switch ext {
	case ".pdf":
		return extractPDFText(ctx, filePath, maxChars)
	default:
		return extractPlainText(filePath, maxChars)
	}
}

// extractPlainText reads standard UTF-8/plain text files.
func extractPlainText(filePath string, maxChars int) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	// Validate UTF-8; if invalid, replace non-utf8 runes
	if !utf8.Valid(data) {
		data = bytes.ToValidUTF8(data, []byte("?"))
	}

	text := strings.TrimSpace(string(data))
	if len(text) > maxChars {
		text = text[:maxChars] + "\n\n[...conteúdo truncado para otimização de contexto...]"
	}

	return text, nil
}

// extractPDFText attempts to extract text from a PDF file using pdftotext or basic stream parser.
func extractPDFText(ctx context.Context, filePath string, maxChars int) (string, error) {
	// 1. Try host pdftotext if available
	if pdftotextPath, err := exec.LookPath("pdftotext"); err == nil && pdftotextPath != "" {
		cmd := exec.CommandContext(ctx, pdftotextPath, "-layout", "-nopgbrk", filePath, "-")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			text := strings.TrimSpace(out.String())
			if text != "" {
				if len(text) > maxChars {
					text = text[:maxChars] + "\n\n[...conteúdo truncado para otimização de contexto...]"
				}
				return text, nil
			}
		}
	}

	// 2. Fallback: lightweight basic stream extraction in pure Go
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read pdf file: %w", err)
	}

	extracted := extractRawPDFStrings(data)
	if len(extracted) > maxChars {
		extracted = extracted[:maxChars] + "\n\n[...conteúdo truncado para otimização de contexto...]"
	}

	if strings.TrimSpace(extracted) == "" {
		return "", fmt.Errorf("não foi possível extrair texto legível do PDF")
	}

	return extracted, nil
}

// extractRawPDFStrings is a pure Go lightweight fallback for uncompressed/light text inside PDFs.
func extractRawPDFStrings(data []byte) string {
	var builder strings.Builder
	inParentheses := false
	var current strings.Builder

	for i := 0; i < len(data); i++ {
		b := data[i]
		if b == '(' && !inParentheses {
			inParentheses = true
			current.Reset()
			continue
		}
		if b == ')' && inParentheses {
			inParentheses = false
			str := current.String()
			if len(str) >= 2 && isPrintable(str) {
				builder.WriteString(str)
				builder.WriteString(" ")
			}
			continue
		}
		if inParentheses {
			if b == '\\' && i+1 < len(data) {
				i++
				b = data[i]
			}
			current.WriteByte(b)
		}
	}

	return strings.TrimSpace(builder.String())
}

func isPrintable(s string) bool {
	printableCount := 0
	for _, r := range s {
		if (r >= 32 && r <= 126) || r >= 160 {
			printableCount++
		}
	}
	return float64(printableCount)/float64(len(s)) > 0.8
}
