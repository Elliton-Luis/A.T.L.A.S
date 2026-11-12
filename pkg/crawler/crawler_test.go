package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCrawlerTraversal(t *testing.T) {
	// Setup mock web server with 3 linked pages
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
			<html>
			<head><title>Home Page</title></head>
			<body>
				<h1>Welcome to ATLAS Test</h1>
				<a href="/about">About Us</a>
				<a href="/contact">Contact</a>
				<a href="https://external-domain.com/ad">External</a>
			</body>
			</html>
		`)
	})
	mux.HandleFunc("/about", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
			<html>
			<head><title>About Page</title></head>
			<body>
				<p>ATLAS is an executive autonomous assistant.</p>
				<a href="/team">Our Team</a>
			</body>
			</html>
		`)
	})
	mux.HandleFunc("/contact", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
			<html>
			<head><title>Contact Page</title></head>
			<body><p>Reach us at contact@atlas.ai</p></body>
			</html>
		`)
	})
	mux.HandleFunc("/team", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
			<html>
			<head><title>Team Page</title></head>
			<body><p>Engineers and Philosophers.</p></body>
			</html>
		`)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := New()
	opts := Options{
		MaxPages:     3,
		MaxDepth:     1,
		Timeout:      5 * time.Second,
		SameHostOnly: true,
		MaxCharsPage: 500,
	}

	result, err := c.Crawl(context.Background(), server.URL, opts)
	if err != nil {
		t.Fatalf("crawl failed: %v", err)
	}

	if len(result.Pages) < 2 {
		t.Errorf("expected at least 2 pages crawled, got %d", len(result.Pages))
	}

	foundAbout := false
	for _, p := range result.Pages {
		if p.Title == "About Page" {
			foundAbout = true
		}
	}

	if !foundAbout {
		t.Errorf("expected to find 'About Page' in crawl results")
	}
}
