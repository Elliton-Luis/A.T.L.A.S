package catholic

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	reSantoTitle = regexp.MustCompile(`(?i)<h1[^>]*class=["'][^"']*entry-title[^"']*["'][^>]*>([\s\S]*?)</h1>`)
	reSpanInside = regexp.MustCompile(`(?i)<span[^>]*>([\s\S]*?)</span>`)
	reContent    = regexp.MustCompile(`(?i)<div[^>]*class=["'][^"']*entry-content content-santo[^"']*["'][^>]*>([\s\S]*?)(?:<footer|<div id=["']content-share|class=["']navigation|$)`)
	reParagraphs = regexp.MustCompile(`(?i)<p[^>]*>([\s\S]*?)</p>`)
	reTags       = regexp.MustCompile(`<[^>]+>`)
)

// LiturgiaFallbackResponse matches the structure of https://liturgia.up.railway.app/
type LiturgiaFallbackResponse struct {
	Data     string `json:"data"`
	Liturgia string `json:"liturgia"`
	Cor      string `json:"cor"`
	Dia      string `json:"dia"`
}

// FetchSantoDoDia obtains the Catholic Saint of the Day.
// It first attempts Canção Nova (detailed biography & prayer),
// and gracefully falls back to the Liturgia API if unavailable.
func FetchSantoDoDia(ctx context.Context) (string, error) {
	// Attempt 1: Canção Nova
	text, err := fetchFromCancaoNova(ctx)
	if err == nil && text != "" {
		return text, nil
	}

	// Attempt 2: Fallback to Liturgia API
	fbText, fbErr := fetchFromLiturgiaFallback(ctx)
	if fbErr == nil && fbText != "" {
		return fbText, nil
	}

	if err != nil {
		return "", fmt.Errorf("falha ao consultar Santo do Dia: %w", err)
	}
	return "", fmt.Errorf("falha ao consultar Santo do Dia: %w", fbErr)
}

func fetchFromCancaoNova(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://santo.cancaonova.com/", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "pt-BR,pt;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status cancao nova %d", resp.StatusCode)
	}

	// Read up to 256KB of HTML
	buf := make([]byte, 256*1024)
	n, _ := resp.Body.Read(buf)
	rawHTML := string(buf[:n])

	title := extractTitle(rawHTML)
	if title == "" {
		return "", fmt.Errorf("titulo do santo nao encontrado")
	}

	paragraphs := extractParagraphs(rawHTML)
	if len(paragraphs) == 0 {
		return "", fmt.Errorf("conteudo do santo nao encontrado")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🌟 *Santo do Dia — %s*\n\n", title))

	// Find prayer or devotional invocation (e.g. "rogai por nós")
	var prayer string
	var bioParts []string

	for _, p := range paragraphs {
		clean := cleanText(p)
		if clean == "" || strings.HasPrefix(strings.ToLower(clean), "referencia") || strings.HasPrefix(strings.ToLower(clean), "o post") {
			continue
		}
		if strings.Contains(strings.ToLower(clean), "rogai por nós") || strings.Contains(strings.ToLower(clean), "rogai por nos") {
			prayer = clean
			continue
		}
		if len(bioParts) < 3 {
			bioParts = append(bioParts, clean)
		}
	}

	for _, p := range bioParts {
		sb.WriteString(p)
		sb.WriteString("\n\n")
	}

	if prayer != "" {
		sb.WriteString(fmt.Sprintf("🙏 *%s*\n\n", prayer))
	} else if len(paragraphs) > 0 {
		sb.WriteString(fmt.Sprintf("🙏 *%s, rogai por nós!*\n\n", title))
	}

	sb.WriteString("🔗 [Ler história completa no Portal Canção Nova](https://santo.cancaonova.com/)")

	result := sb.String()
	if len(result) > 3800 {
		result = result[:3750] + "\n\n...\n🔗 [Continuar lendo no Portal Canção Nova](https://santo.cancaonova.com/)"
	}

	return result, nil
}

func fetchFromLiturgiaFallback(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://liturgia.up.railway.app/", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "AtlasBot/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status liturgia api %d", resp.StatusCode)
	}

	var data LiturgiaFallbackResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}

	if data.Liturgia == "" {
		return "", fmt.Errorf("liturgia vazia")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🌟 *Santo / Celebração do Dia — %s*\n", data.Liturgia))
	if data.Data != "" {
		sb.WriteString(fmt.Sprintf("📅 _%s_ ", data.Data))
	}
	if data.Cor != "" {
		sb.WriteString(fmt.Sprintf("(Cor: %s)\n\n", data.Cor))
	} else {
		sb.WriteString("\n\n")
	}

	if data.Dia != "" {
		sb.WriteString("🙏 *Oração do Dia:*\n")
		sb.WriteString(fmt.Sprintf("_%s_\n\n", strings.TrimSpace(data.Dia)))
	}

	sb.WriteString("🔗 [Acessar Portal Canção Nova](https://liturgia.cancaonova.com/)")
	return sb.String(), nil
}

func extractTitle(content string) string {
	matches := reSantoTitle.FindStringSubmatch(content)
	if len(matches) < 2 {
		return ""
	}
	raw := matches[1]
	spanMatch := reSpanInside.FindStringSubmatch(raw)
	if len(spanMatch) >= 2 {
		raw = spanMatch[1]
	}
	return cleanText(raw)
}

func extractParagraphs(content string) []string {
	contentMatch := reContent.FindStringSubmatch(content)
	searchArea := content
	if len(contentMatch) >= 2 {
		searchArea = contentMatch[1]
	}

	matches := reParagraphs.FindAllStringSubmatch(searchArea, -1)
	var paras []string
	for _, m := range matches {
		if len(m) >= 2 {
			c := cleanText(m[1])
			if c != "" && !strings.Contains(c, "share-buttons") && !strings.Contains(c, "<img") {
				paras = append(paras, c)
			}
		}
	}
	return paras
}

func cleanText(s string) string {
	s = reTags.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimSpace(s)
}
