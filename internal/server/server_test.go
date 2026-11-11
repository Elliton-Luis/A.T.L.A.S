package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"atlas/internal/config"
)

func TestServerEndpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "atlas_srv_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		DBPath:        filepath.Join(tmpDir, "test.sqlite"),
		CalendarToken: "test_token",
		CronSecret:    "secret_123",
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to initialize server: %v", err)
	}
	defer srv.Close()

	// 1. Test /health
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on /health, got %d", rec.Code)
	}

	// 2. Test /api/cron/tick unauthorized
	req = httptest.NewRequest("GET", "/api/cron/tick", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 on /api/cron/tick without token, got %d", rec.Code)
	}

	// 3. Test /api/cron/tick authorized
	req = httptest.NewRequest("GET", "/api/cron/tick?token=secret_123", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on /api/cron/tick with token, got %d", rec.Code)
	}
}
