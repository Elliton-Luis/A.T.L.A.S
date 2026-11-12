package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	scriptRegex = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRegex  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	navRegex    = regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`)
	tagRegex    = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRegex  = regexp.MustCompile(`\s+`)
)

// FetchPageText downloads a web page and extracts its readable text content.
func FetchPageText(ctx context.Context, pageURL string, maxChars int) (string, error) {
	if maxChars <= 0 {
		maxChars = 2500
	}

	client := &http.Client{Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("falha ao acessar link: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("o site retornou status %d", resp.StatusCode)
	}

	// Limit read to 1MB to avoid memory abuse
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", fmt.Errorf("ler página: %w", err)
	}

	html := string(bodyBytes)
	clean := scriptRegex.ReplaceAllString(html, " ")
	clean = styleRegex.ReplaceAllString(clean, " ")
	clean = navRegex.ReplaceAllString(clean, " ")
	clean = tagRegex.ReplaceAllString(clean, " ")
	clean = cleanHTML(clean)
	clean = spaceRegex.ReplaceAllString(clean, " ")
	clean = strings.TrimSpace(clean)

	if len(clean) > maxChars {
		clean = clean[:maxChars]
	}

	return clean, nil
}
