package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"atlas/pkg/web"
)

var (
	linkRegex  = regexp.MustCompile(`(?i)<a\s+[^>]*href=["']([^"']+)["'][^>]*>`)
	titleRegex = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// Options configures crawler execution limits and behavior.
type Options struct {
	MaxPages     int
	MaxDepth     int
	Timeout      time.Duration
	SameHostOnly bool
	MaxCharsPage int
}

// DefaultOptions returns conservative, safe crawler defaults.
func DefaultOptions() Options {
	return Options{
		MaxPages:     5,
		MaxDepth:     1,
		Timeout:      20 * time.Second,
		SameHostOnly: true,
		MaxCharsPage: 2000,
	}
}

// Page represents an individual crawled web page.
type Page struct {
	URL   string
	Title string
	Text  string
	Depth int
}

// CrawlResult contains the aggregated output of a crawling session.
type CrawlResult struct {
	SeedURL   string
	Host      string
	Pages     []Page
	TotalText string
	Duration  time.Duration
}

// Crawler executes multi-page discovery and content extraction.
type Crawler struct {
	client *http.Client
}

// New creates a new Crawler instance with reasonable connection pooling.
func New() *Crawler {
	return &Crawler{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Crawl visits the seedURL and traverses internal links up to opts.MaxDepth and opts.MaxPages.
func (c *Crawler) Crawl(ctx context.Context, seedURL string, opts Options) (*CrawlResult, error) {
	if opts.MaxPages <= 0 {
		opts.MaxPages = 5
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	parsedSeed, err := url.Parse(seedURL)
	if err != nil || parsedSeed.Scheme == "" || parsedSeed.Host == "" {
		return nil, fmt.Errorf("URL inicial inválida: %s", seedURL)
	}

	startTime := time.Now()
	visited := make(map[string]bool)
	var mu sync.Mutex

	var pages []Page
	type item struct {
		u     string
		depth int
	}

	queue := []item{{u: seedURL, depth: 0}}
	visited[seedURL] = true

	for len(queue) > 0 {
		select {
		case <-ctx.Done():
			break
		default:
		}

		if len(pages) >= opts.MaxPages {
			break
		}

		curr := queue[0]
		queue = queue[1:]

		pageContent, title, links, err := c.fetchAndExtract(ctx, curr.u, opts.MaxCharsPage)
		if err != nil {
			continue
		}

		page := Page{
			URL:   curr.u,
			Title: title,
			Text:  pageContent,
			Depth: curr.depth,
		}
		pages = append(pages, page)

		// Discover next level links
		if curr.depth < opts.MaxDepth {
			for _, l := range links {
				absURL := resolveURL(parsedSeed, curr.u, l)
				if absURL == "" {
					continue
				}

				parsedLink, err := url.Parse(absURL)
				if err != nil {
					continue
				}

				if opts.SameHostOnly && !strings.EqualFold(parsedLink.Host, parsedSeed.Host) {
					continue
				}

				// Normalize URL (strip fragment)
				parsedLink.Fragment = ""
				normalized := parsedLink.String()

				mu.Lock()
				if !visited[normalized] && len(visited) < opts.MaxPages*3 {
					visited[normalized] = true
					queue = append(queue, item{u: normalized, depth: curr.depth + 1})
				}
				mu.Unlock()
			}
		}
	}

	var sb strings.Builder
	for i, p := range pages {
		sb.WriteString(fmt.Sprintf("--- Página %d: %s (%s) ---\n%s\n\n", i+1, p.Title, p.URL, p.Text))
	}

	return &CrawlResult{
		SeedURL:   seedURL,
		Host:      parsedSeed.Host,
		Pages:     pages,
		TotalText: strings.TrimSpace(sb.String()),
		Duration:  time.Since(startTime),
	}, nil
}

func (c *Crawler) fetchAndExtract(ctx context.Context, targetURL string, maxChars int) (string, string, []string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", "", nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ATLAS-Agent/2.0; +https://github.com/atlas)")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	// Read limited HTML
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", "", nil, err
	}
	html := string(bodyBytes)

	// Extract Title
	title := ""
	if m := titleRegex.FindStringSubmatch(html); len(m) > 1 {
		title = strings.TrimSpace(m[1])
	}
	if title == "" {
		title = targetURL
	}

	// Extract Links
	var links []string
	matches := linkRegex.FindAllStringSubmatch(html, -1)
	for _, m := range matches {
		if len(m) > 1 {
			rawLink := strings.TrimSpace(m[1])
			if rawLink != "" && !strings.HasPrefix(rawLink, "javascript:") && !strings.HasPrefix(rawLink, "mailto:") && !strings.HasPrefix(rawLink, "tel:") && !strings.HasPrefix(rawLink, "#") {
				links = append(links, rawLink)
			}
		}
	}

	// Extract Clean Text using pkg/web FetchPageText logic or fallback
	cleanText, err := web.FetchPageText(ctx, targetURL, maxChars)
	if err != nil {
		cleanText = targetURL
	}

	return cleanText, title, links, nil
}

func resolveURL(seed *url.URL, currentURL, href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if u.IsAbs() {
		return u.String()
	}

	base, err := url.Parse(currentURL)
	if err != nil {
		base = seed
	}
	return base.ResolveReference(u).String()
}
