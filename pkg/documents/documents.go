package documents

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultMaxChars is the default maximum number of characters to extract from a document.
	DefaultMaxChars = 32000
)

// Supported plain text / code file extensions
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
	".srt":      true,
	".vtt":      true,
	".tex":      true,
	".rst":      true,
	".adoc":     true,
	".asciidoc": true,
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

var officeExtensions = map[string]bool{
	".docx": true,
	".xlsx": true,
	".pptx": true,
	".odt":  true,
	".ods":  true,
	".odp":  true,
	".epub": true,
	".rtf":  true,
	".doc":  true,
	".xls":  true,
	".ppt":  true,
}

// IsAudio checks if the file name has an audio extension.
func IsAudio(fileName string) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	return audioExtensions[ext]
}

// IsSupportedDocument checks if the file is a readable document.
func IsSupportedDocument(fileName string) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	return ext == ".pdf" || officeExtensions[ext] || textExtensions[ext]
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

	var text string
	var err error

	switch ext {
	case ".pdf":
		text, err = extractPDFText(ctx, filePath, maxChars)
	case ".docx":
		text, err = extractDocxText(filePath, maxChars)
	case ".xlsx":
		text, err = extractXlsxText(filePath, maxChars)
	case ".pptx":
		text, err = extractPptxText(filePath, maxChars)
	case ".odt", ".ods", ".odp":
		text, err = extractOdfText(filePath, maxChars)
	case ".epub":
		text, err = extractEpubText(filePath, maxChars)
	case ".rtf":
		text, err = extractRtfText(filePath, maxChars)
	case ".doc", ".xls", ".ppt":
		text, err = extractLegacyOfficeText(ctx, filePath, ext, maxChars)
	default:
		text, err = extractPlainText(filePath, maxChars)
	}

	if err != nil {
		return "", err
	}

	text = strings.TrimSpace(text)
	if len(text) > maxChars {
		text = text[:maxChars] + "\n\n[...conteúdo truncado para otimização de contexto...]"
	}

	return text, nil
}

// extractPlainText reads standard UTF-8/plain text files.
func extractPlainText(filePath string, maxChars int) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	if !utf8.Valid(data) {
		data = bytes.ToValidUTF8(data, []byte("?"))
	}

	return string(data), nil
}

// extractPDFText attempts to extract text from a PDF file using pdftotext or basic stream parser.
func extractPDFText(ctx context.Context, filePath string, maxChars int) (string, error) {
	if pdftotextPath, err := exec.LookPath("pdftotext"); err == nil && pdftotextPath != "" {
		cmd := exec.CommandContext(ctx, pdftotextPath, "-layout", "-nopgbrk", filePath, "-")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			text := strings.TrimSpace(out.String())
			if text != "" {
				return text, nil
			}
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read pdf file: %w", err)
	}

	extracted := extractRawPDFStrings(data)
	if strings.TrimSpace(extracted) == "" {
		return "", fmt.Errorf("não foi possível extrair texto legível do PDF")
	}

	return extracted, nil
}

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

// extractDocxText extracts text from Microsoft Word documents (.docx).
func extractDocxText(filePath string, maxChars int) (string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("abrir arquivo docx: %w", err)
	}
	defer zr.Close()

	var sb strings.Builder

	// Read main document and headers/footers
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if name == "word/document.xml" || strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			text := parseDocxXML(rc)
			rc.Close()
			if text != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n\n")
				}
				sb.WriteString(text)
			}
		}
		if sb.Len() >= maxChars {
			break
		}
	}

	if sb.Len() == 0 {
		return "", fmt.Errorf("nenhum texto encontrado no arquivo docx")
	}

	return sb.String(), nil
}

func parseDocxXML(r io.Reader) string {
	decoder := xml.NewDecoder(r)
	var sb strings.Builder
	inText := false

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText = true
			} else if t.Name.Local == "tab" {
				sb.WriteString("\t")
			} else if t.Name.Local == "br" || t.Name.Local == "cr" {
				sb.WriteString("\n")
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			} else if t.Name.Local == "p" {
				sb.WriteString("\n")
			} else if t.Name.Local == "tr" {
				sb.WriteString("\n")
			} else if t.Name.Local == "tc" {
				sb.WriteString("\t")
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		}
	}

	return cleanExtractedText(sb.String())
}

// extractXlsxText extracts data from Microsoft Excel spreadsheets (.xlsx).
func extractXlsxText(filePath string, maxChars int) (string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("abrir arquivo xlsx: %w", err)
	}
	defer zr.Close()

	// 1. Extract shared strings
	sharedStrings := make([]string, 0)
	for _, f := range zr.File {
		if strings.ToLower(f.Name) == "xl/sharedstrings.xml" {
			rc, err := f.Open()
			if err == nil {
				sharedStrings = parseXlsxSharedStrings(rc)
				rc.Close()
			}
			break
		}
	}

	// 2. Extract worksheets in order
	var sheetFiles []*zip.File
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
			sheetFiles = append(sheetFiles, f)
		}
	}
	sort.Slice(sheetFiles, func(i, j int) bool {
		return sheetFiles[i].Name < sheetFiles[j].Name
	})

	var sb strings.Builder
	for idx, sf := range sheetFiles {
		rc, err := sf.Open()
		if err != nil {
			continue
		}
		sheetData := parseXlsxSheetXML(rc, sharedStrings)
		rc.Close()

		if strings.TrimSpace(sheetData) != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(fmt.Sprintf("--- Aba %d (%s) ---\n", idx+1, filepath.Base(sf.Name)))
			sb.WriteString(sheetData)
		}

		if sb.Len() >= maxChars {
			break
		}
	}

	if sb.Len() == 0 {
		return "", fmt.Errorf("nenhum dado encontrado na planilha xlsx")
	}

	return sb.String(), nil
}

func parseXlsxSharedStrings(r io.Reader) []string {
	decoder := xml.NewDecoder(r)
	var list []string
	var current strings.Builder
	inT := false

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inT = true
				current.Reset()
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inT = false
				list = append(list, current.String())
			}
		case xml.CharData:
			if inT {
				current.Write(t)
			}
		}
	}
	return list
}

func parseXlsxSheetXML(r io.Reader, sharedStrings []string) string {
	decoder := xml.NewDecoder(r)
	var sb strings.Builder
	var rowCells []string
	var currentVal strings.Builder
	isShared := false
	inValue := false

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Local == "c" {
				isShared = false
				for _, attr := range t.Attr {
					if attr.Name.Local == "t" && attr.Value == "s" {
						isShared = true
						break
					}
				}
				currentVal.Reset()
			} else if t.Name.Local == "v" || t.Name.Local == "t" {
				inValue = true
			}
		case xml.EndElement:
			if t.Name.Local == "v" || t.Name.Local == "t" {
				inValue = false
			} else if t.Name.Local == "c" {
				val := currentVal.String()
				if isShared {
					idx, err := strconv.Atoi(val)
					if err == nil && idx >= 0 && idx < len(sharedStrings) {
						val = sharedStrings[idx]
					}
				}
				rowCells = append(rowCells, strings.TrimSpace(val))
			} else if t.Name.Local == "row" {
				if len(rowCells) > 0 {
					sb.WriteString(strings.Join(rowCells, " | "))
					sb.WriteString("\n")
					rowCells = rowCells[:0]
				}
			}
		case xml.CharData:
			if inValue {
				currentVal.Write(t)
			}
		}
	}
	return cleanExtractedText(sb.String())
}

// extractPptxText extracts text from Microsoft PowerPoint presentations (.pptx).
func extractPptxText(filePath string, maxChars int) (string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("abrir arquivo pptx: %w", err)
	}
	defer zr.Close()

	var slideFiles []*zip.File
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			slideFiles = append(slideFiles, f)
		}
	}
	sort.Slice(slideFiles, func(i, j int) bool {
		return slideFiles[i].Name < slideFiles[j].Name
	})

	var sb strings.Builder
	for idx, sf := range slideFiles {
		rc, err := sf.Open()
		if err != nil {
			continue
		}
		slideText := parseDocxXML(rc)
		rc.Close()

		if strings.TrimSpace(slideText) != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(fmt.Sprintf("--- Slide %d ---\n", idx+1))
			sb.WriteString(slideText)
		}

		if sb.Len() >= maxChars {
			break
		}
	}

	if sb.Len() == 0 {
		return "", fmt.Errorf("nenhum texto encontrado na apresentação pptx")
	}

	return sb.String(), nil
}

// extractOdfText extracts text from OpenDocument formats (.odt, .ods, .odp).
func extractOdfText(filePath string, maxChars int) (string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("abrir arquivo open document: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if strings.ToLower(f.Name) == "content.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()

			decoder := xml.NewDecoder(rc)
			var sb strings.Builder
			for {
				token, err := decoder.Token()
				if err != nil {
					break
				}
				switch t := token.(type) {
				case xml.StartElement:
					if t.Name.Local == "p" || t.Name.Local == "h" {
						// Parágrafo
					} else if t.Name.Local == "tab" {
						sb.WriteString("\t")
					} else if t.Name.Local == "line-break" {
						sb.WriteString("\n")
					}
				case xml.EndElement:
					if t.Name.Local == "p" || t.Name.Local == "h" || t.Name.Local == "table-row" {
						sb.WriteString("\n")
					} else if t.Name.Local == "table-cell" {
						sb.WriteString(" | ")
					}
				case xml.CharData:
					sb.Write(t)
				}
				if sb.Len() >= maxChars {
					break
				}
			}
			res := cleanExtractedText(sb.String())
			if res == "" {
				return "", fmt.Errorf("documento odt/ods/odp vazio")
			}
			return res, nil
		}
	}

	return "", fmt.Errorf("content.xml não encontrado no arquivo open document")
}

// extractEpubText extracts text from EPUB e-books (.epub).
func extractEpubText(filePath string, maxChars int) (string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("abrir arquivo epub: %w", err)
	}
	defer zr.Close()

	var htmlFiles []*zip.File
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.HasSuffix(name, ".xhtml") || strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".htm") {
			htmlFiles = append(htmlFiles, f)
		}
	}
	sort.Slice(htmlFiles, func(i, j int) bool {
		return htmlFiles[i].Name < htmlFiles[j].Name
	})

	var sb strings.Builder
	tagRegex := regexp.MustCompile(`<[^>]+>`)

	for _, hf := range htmlFiles {
		rc, err := hf.Open()
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(rc)
		rc.Close()

		text := tagRegex.ReplaceAllString(string(data), " ")
		text = strings.ReplaceAll(text, "&nbsp;", " ")
		text = strings.ReplaceAll(text, "&lt;", "<")
		text = strings.ReplaceAll(text, "&gt;", ">")
		text = strings.ReplaceAll(text, "&amp;", "&")
		text = strings.ReplaceAll(text, "&quot;", "\"")

		cleaned := cleanExtractedText(text)
		if cleaned != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(cleaned)
		}

		if sb.Len() >= maxChars {
			break
		}
	}

	if sb.Len() == 0 {
		return "", fmt.Errorf("nenhum texto legível encontrado no arquivo epub")
	}

	return sb.String(), nil
}

// extractRtfText extracts readable text from Rich Text Format (.rtf).
func extractRtfText(filePath string, maxChars int) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("ler rtf: %w", err)
	}

	var sb strings.Builder
	inControlWord := false
	inHex := false
	var hexBuf []byte

	for i := 0; i < len(data); i++ {
		b := data[i]

		if inHex {
			hexBuf = append(hexBuf, b)
			if len(hexBuf) == 2 {
				inHex = false
				decoded, err := hex.DecodeString(string(hexBuf))
				if err == nil {
					sb.Write(decoded)
				}
				hexBuf = hexBuf[:0]
			}
			continue
		}

		if b == '\\' {
			if i+1 < len(data) {
				next := data[i+1]
				if next == '\'' {
					i++
					inHex = true
					continue
				}
				if next == '\\' || next == '{' || next == '}' {
					sb.WriteByte(next)
					i++
					continue
				}
			}
			inControlWord = true
			continue
		}

		if inControlWord {
			if b == ' ' {
				inControlWord = false
			} else if b == '\n' || b == '\r' {
				inControlWord = false
			} else if (b < 'a' || b > 'z') && (b < 'A' || b > 'Z') && (b < '0' || b > '9') && b != '-' {
				inControlWord = false
				i-- // reprocess non-word char
			}
			continue
		}

		if b == '{' || b == '}' || b == '\r' {
			continue
		}

		sb.WriteByte(b)
		if sb.Len() >= maxChars {
			break
		}
	}

	res := cleanExtractedText(sb.String())
	if res == "" {
		return "", fmt.Errorf("conteúdo rtf vazio ou ilegível")
	}

	return res, nil
}

// extractLegacyOfficeText extracts plain text from binary formats (.doc, .xls, .ppt).
func extractLegacyOfficeText(ctx context.Context, filePath, ext string, maxChars int) (string, error) {
	// Try host tools if available (catdoc, antiword, strings)
	var cmdName string
	switch ext {
	case ".doc":
		if p, err := exec.LookPath("antiword"); err == nil && p != "" {
			cmdName = "antiword"
		} else if p, err := exec.LookPath("catdoc"); err == nil && p != "" {
			cmdName = "catdoc"
		}
	case ".xls":
		if p, err := exec.LookPath("xls2csv"); err == nil && p != "" {
			cmdName = "xls2csv"
		}
	}

	if cmdName != "" {
		cmd := exec.CommandContext(ctx, cmdName, filePath)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			txt := strings.TrimSpace(out.String())
			if txt != "" {
				return txt, nil
			}
		}
	}

	// Pure Go fallback: stream printable sequences
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	var current strings.Builder
	for i := 0; i < len(data); i++ {
		b := data[i]
		if (b >= 32 && b <= 126) || b == '\n' || b == '\t' || b >= 160 {
			current.WriteByte(b)
		} else {
			if current.Len() >= 4 {
				sb.WriteString(current.String())
				sb.WriteString(" ")
			}
			current.Reset()
		}
	}
	if current.Len() >= 4 {
		sb.WriteString(current.String())
	}

	res := cleanExtractedText(sb.String())
	if res == "" {
		return "", fmt.Errorf("não foi possível extrair texto do documento binário %s", ext)
	}

	return res, nil
}

func cleanExtractedText(text string) string {
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, "\n")
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
