package hud

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"
	"time"

	"atlas/internal/config"
	"atlas/internal/database"
)

//go:embed template.html
var hudHTML []byte

type Handler struct {
	cfg *config.Config
	db  *database.DB
}

func NewHandler(cfg *config.Config, db *database.DB) *Handler {
	return &Handler{
		cfg: cfg,
		db:  db,
	}
}

// ServeHUD renders the dark-themed futuristic executive HUD.
func (h *Handler) ServeHUD(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' https://telegram.org;")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(hudHTML)
}

// HandleDataAPI returns live stats, events, goals, ideas, and routines for the HUD.
func (h *Handler) HandleDataAPI(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	now := time.Now().In(h.cfg.Location)
	todayStr := now.Format("2006-01-02")

	events, _ := h.db.GetEventsForDay(ctx, todayStr)
	goals, _ := h.db.GetActiveGoals(ctx, 10)
	ideas, _ := h.db.GetRecentIdeas(ctx, 10)
	routines, _ := h.db.GetAllRoutines(ctx)

	payload := map[string]interface{}{
		"events":   events,
		"goals":    goals,
		"ideas":    ideas,
		"routines": routines,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// HandleToggleRoutine switches a routine on/off from the HUD.
func (h *Handler) HandleToggleRoutine(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) || r.Method != http.MethodPost {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	_, _ = h.db.ToggleRoutine(r.Context(), req.ID)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// HandleCompleteEvent marks an event as completed.
func (h *Handler) HandleCompleteEvent(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) || r.Method != http.MethodPost {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	_ = h.db.MarkEventStatus(r.Context(), req.ID, "concluido")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (h *Handler) authenticate(r *http.Request) bool {
	// 1. Check Telegram WebApp initData HMAC-SHA256 signature
	initData := r.Header.Get("X-Telegram-Init-Data")
	if initData != "" {
		if ValidateTelegramWebAppData(initData, h.cfg.TelegramBotToken, h.cfg.TelegramChatID) {
			return true
		}
	}

	// 2. Check token in query param or header (constant time compare)
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("X-Atlas-Token")
	}

	if token != "" {
		if h.cfg.CalendarToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.cfg.CalendarToken)) == 1 {
			return true
		}
		if h.cfg.TelegramSecretToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.cfg.TelegramSecretToken)) == 1 {
			return true
		}
	}

	return false
}
