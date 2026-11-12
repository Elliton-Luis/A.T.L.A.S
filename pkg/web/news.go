package web

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type RSS struct {
	Channel Channel `xml:"channel"`
}

type Channel struct {
	Title string `xml:"title"`
	Items []Item `xml:"item"`
}

type Item struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
	Source  string `xml:"source"`
}

type NewsResult struct {
	Title string
	Link  string
	Date  string
}

// SearchGoogleNews searches real-time news via Google News RSS in Portuguese.
func SearchGoogleNews(ctx context.Context, query string, maxItems int) ([]NewsResult, error) {
	if maxItems <= 0 {
		maxItems = 5
	}
	encodedQuery := url.QueryEscape(query)
	rssURL := fmt.Sprintf("https://news.google.com/rss/search?q=%s&hl=pt-BR&gl=BR&ceid=BR:pt-419", encodedQuery)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rssURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar notícias: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status ao buscar notícias: %d", resp.StatusCode)
	}

	var rss RSS
	if err := xml.NewDecoder(resp.Body).Decode(&rss); err != nil {
		return nil, fmt.Errorf("decode rss: %w", err)
	}

	var results []NewsResult
	for i, it := range rss.Channel.Items {
		if i >= maxItems {
			break
		}
		title := cleanHTML(it.Title)
		results = append(results, NewsResult{
			Title: title,
			Link:  it.Link,
			Date:  it.PubDate,
		})
	}

	return results, nil
}

func cleanHTML(s string) string {
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&#39;", "'")
	return strings.TrimSpace(s)
}
