package catholic

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type WPPost struct {
	ID    int `json:"id"`
	Title struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Link    string `json:"link"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
	Excerpt struct {
		Rendered string `json:"rendered"`
	} `json:"excerpt"`
}

var (
	reScriptTag = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyleTag  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reFigureTag = regexp.MustCompile(`(?is)<figure[^>]*>.*?</figure>`)
	reIframeTag = regexp.MustCompile(`(?is)<iframe[^>]*>.*?</iframe>`)
	reDivAds    = regexp.MustCompile(`(?is)<div[^>]*class=["'][^"']*litur-anuncio[^"']*["'][^>]*>.*?</div>`)
	rePTags     = regexp.MustCompile(`(?i)<p[^>]*>([\s\S]*?)</p>`)
)

// DetermineCurrentHour returns the traditional canonical hour name according to local time.
func DetermineCurrentHour(t time.Time) string {
	h := t.Hour()
	switch {
	case h >= 6 && h < 9:
		return "Laudes"
	case h >= 9 && h < 12:
		return "Hora Terça"
	case h >= 12 && h < 15:
		return "Hora Sexta"
	case h >= 15 && h < 18:
		return "Hora Nona"
	case h >= 18 && h < 21:
		return "Vésperas"
	case h >= 21:
		return "Completas"
	default:
		return "Ofício das Leituras"
	}
}

// FetchLiturgiaDasHoras retrieves the canonical hour from the official WordPress REST API.
func FetchLiturgiaDasHoras(ctx context.Context, hourName string, loc *time.Location) (string, error) {
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)

	targetHour := strings.TrimSpace(hourName)
	if targetHour == "" || strings.EqualFold(targetHour, "auto") || strings.EqualFold(targetHour, "hoje") || strings.EqualFold(targetHour, "agora") {
		targetHour = DetermineCurrentHour(now)
	}

	searchKey := targetHour
	switch strings.ToLower(targetHour) {
	case "laudes", "manha", "manhã":
		searchKey = "Laudes"
	case "vesperas", "vésperas", "tarde":
		searchKey = "Vésperas"
	case "completas", "noite":
		searchKey = "Completas"
	case "oficio", "ofício", "oficio das leituras", "leituras", "matinas":
		searchKey = "Ofício das Leituras"
	case "terca", "terça":
		searchKey = "Hora Terça"
	case "sexta":
		searchKey = "Hora Sexta"
	case "nona":
		searchKey = "Hora Nona"
	}

	apiURL := fmt.Sprintf("https://liturgiadashoras.online/wp-json/wp/v2/posts?search=%s&per_page=1", url.QueryEscape(searchKey))
	client := &http.Client{Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("erro ao conectar à Liturgia das Horas: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status api liturgiadashoras %d", resp.StatusCode)
	}

	var posts []WPPost
	if err := json.NewDecoder(resp.Body).Decode(&posts); err != nil {
		return "", fmt.Errorf("erro ao decodificar posts: %w", err)
	}

	if len(posts) == 0 {
		return "", fmt.Errorf("nenhuma publicação encontrada para '%s' hoje", searchKey)
	}

	post := posts[0]
	cleanTitle := cleanText(html.UnescapeString(post.Title.Rendered))
	cleanBody := cleanPostContent(post.Content.Rendered)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⛪ *Liturgia das Horas — %s*\n", cleanTitle))
	sb.WriteString(fmt.Sprintf("📅 _%s_\n\n", now.Format("02/01/2006")))

	if cleanBody != "" {
		sb.WriteString(cleanBody)
		sb.WriteString("\n\n")
	}

	if post.Link != "" {
		sb.WriteString(fmt.Sprintf("🔗 [Rezar na íntegra com áudios e salmos no portal](%s)", post.Link))
	} else {
		sb.WriteString("🔗 [Acessar Portal Liturgia das Horas](https://liturgiadashoras.online/)")
	}

	full := sb.String()
	if len(full) > 3900 {
		full = full[:3800] + "\n\n...\n🔗 [Continuar rezando no Portal Liturgia das Horas](" + post.Link + ")"
	}

	return full, nil
}

func cleanPostContent(raw string) string {
	raw = reScriptTag.ReplaceAllString(raw, "")
	raw = reStyleTag.ReplaceAllString(raw, "")
	raw = reFigureTag.ReplaceAllString(raw, "")
	raw = reIframeTag.ReplaceAllString(raw, "")
	raw = reDivAds.ReplaceAllString(raw, "")

	matches := rePTags.FindAllStringSubmatch(raw, -1)
	var paras []string

	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		p := cleanText(m[1])
		if p == "" || strings.HasPrefix(strings.ToLower(p), "copiar chave pix") ||
			strings.Contains(p, "chave pix") || strings.Contains(strings.ToLower(p), "apoie a liturgia") {
			continue
		}
		paras = append(paras, p)
	}

	if len(paras) == 0 {
		return ""
	}

	// Extract opening verses + prominent middle/conclusion
	var selected []string
	totalLen := 0

	for _, p := range paras {
		// Stop before reaching 2500 chars to leave space for title and link
		if totalLen+len(p) > 2600 {
			break
		}
		selected = append(selected, p)
		totalLen += len(p)
	}

	return strings.Join(selected, "\n\n")
}
