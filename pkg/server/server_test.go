package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"atlas/pkg/config"
	"atlas/pkg/database"
	"atlas/pkg/sonar"
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

func TestEventConfirmationReplacement(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "atlas_ev_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		DBPath: filepath.Join(tmpDir, "test.sqlite"),
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to initialize server: %v", err)
	}
	defer srv.Close()

	ctx := context.Background()
	chatID := int64(9999)

	// 1. Criar evento original (errado)
	oldID, err := srv.db.InsertEvento(ctx, "Consulta Dentista", "2026-11-20 14:00:00", "2026-11-20 13:00:00", "2026-11-20 13:30:00")
	if err != nil {
		t.Fatalf("failed to insert initial event: %v", err)
	}

	// 2. Simular novo intent com horário corrigido
	newIntentJSON := `{"tipo":"evento","descricao":"Consulta Dentista","horario_oficial":"2026-11-20 16:00:00","frequencia":"pontual"}`

	// Gravar confirmação pendente
	if err := srv.db.SavePendingConfirmation(ctx, chatID, "substituir_evento", oldID, newIntentJSON); err != nil {
		t.Fatalf("failed to save pending confirmation: %v", err)
	}

	// 3. Usuário responde "Sim"
	pending, err := srv.db.GetPendingConfirmation(ctx, chatID)
	if err != nil || pending == nil {
		t.Fatalf("failed to fetch pending confirmation: %v", err)
	}

	handled, reply := srv.handlePendingConfirmation(ctx, chatID, "Sim", pending)
	if !handled {
		t.Fatalf("expected confirmation to be handled on 'Sim'")
	}
	if !strings.Contains(reply, "apagado com sucesso") {
		t.Errorf("expected success deletion message, got: %s", reply)
	}

	// Verificar que evento anterior foi excluído
	if _, err := srv.db.GetEventoByID(ctx, oldID); err == nil {
		t.Errorf("expected old event %d to be deleted, but it still exists", oldID)
	}

	// Verificar que novo evento foi criado
	latest, err := srv.db.GetLatestPendingEvento(ctx)
	if err != nil || latest == nil {
		t.Fatalf("expected new event to exist: %v", err)
	}
	if !strings.Contains(latest.HorarioOficial, "2026-11-20") || !strings.Contains(latest.HorarioOficial, "16:00") {
		t.Errorf("expected new event time to contain 2026-11-20 16:00, got %s", latest.HorarioOficial)
	}

	// 4. Teste de resposta "Não"
	if err := srv.db.SavePendingConfirmation(ctx, chatID, "substituir_evento", latest.ID, newIntentJSON); err != nil {
		t.Fatalf("failed to save second confirmation: %v", err)
	}
	pending2, _ := srv.db.GetPendingConfirmation(ctx, chatID)
	handledNo, replyNo := srv.handlePendingConfirmation(ctx, chatID, "Não", pending2)
	if !handledNo {
		t.Fatalf("expected 'Não' to be handled")
	}
	if !strings.Contains(replyNo, "Mantive o compromisso original") {
		t.Errorf("expected original kept message, got: %s", replyNo)
	}

	// Confirmar que o evento não foi apagado
	if _, err := srv.db.GetEventoByID(ctx, latest.ID); err != nil {
		t.Errorf("event should have been kept after 'Não'")
	}
}

func TestClearAllEventosWithConfirmation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "atlas_clear_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.sqlite")
	db, err := database.Open(dbPath, "", "")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	_, _ = db.InsertEvento(ctx, "Evento 1", "2026-11-20 10:00:00", "", "")
	_, _ = db.InsertEvento(ctx, "Evento 2", "2026-11-20 14:00:00", "", "")

	count, _ := db.CountAllEventos(ctx)
	if count != 2 {
		t.Fatalf("expected 2 events, got %d", count)
	}

	srv := &Server{
		db:  db,
		cfg: &config.Config{TelegramChatID: 12345},
	}
	chatID := int64(12345)

	// 1. Salva confirmação pendente de limpar agenda
	if err := srv.db.SavePendingConfirmation(ctx, chatID, "limpar_agenda", 0, ""); err != nil {
		t.Fatalf("failed to save pending confirmation: %v", err)
	}

	// 2. Testa resposta "Não" (deve cancelar e manter eventos)
	pending, _ := srv.db.GetPendingConfirmation(ctx, chatID)
	handledNo, replyNo := srv.handlePendingConfirmation(ctx, chatID, "Não", pending)
	if !handledNo || !strings.Contains(replyNo, "Operação cancelada") {
		t.Errorf("expected cancellation message, got: %s", replyNo)
	}
	countAfterNo, _ := db.CountAllEventos(ctx)
	if countAfterNo != 2 {
		t.Fatalf("expected 2 events kept after 'Não', got %d", countAfterNo)
	}

	// 3. Salva confirmação pendente novamente e responde "Sim"
	_ = srv.db.SavePendingConfirmation(ctx, chatID, "limpar_agenda", 0, "")
	pending2, _ := srv.db.GetPendingConfirmation(ctx, chatID)
	handledYes, replyYes := srv.handlePendingConfirmation(ctx, chatID, "Sim", pending2)
	if !handledYes || !strings.Contains(replyYes, "Agenda limpa com sucesso") {
		t.Errorf("expected success message, got: %s", replyYes)
	}

	countAfterYes, _ := db.CountAllEventos(ctx)
	if countAfterYes != 0 {
		t.Fatalf("expected 0 events after 'Sim', got %d", countAfterYes)
	}
}

func TestClearAllRoutinesWithConfirmation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "atlas_clear_routines_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.sqlite")
	db, err := database.Open(dbPath, "", "")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	count, _ := db.CountAllRoutines(ctx)
	if count == 0 {
		t.Fatalf("expected seeded routines, got 0")
	}

	srv := &Server{
		db:  db,
		cfg: &config.Config{TelegramChatID: 12345},
	}
	chatID := int64(12345)

	// 1. Salva confirmação pendente de limpar lembretes
	if err := srv.db.SavePendingConfirmation(ctx, chatID, "limpar_lembretes", 0, ""); err != nil {
		t.Fatalf("failed to save pending confirmation: %v", err)
	}

	// 2. Responde "Não" (deve cancelar)
	pending, _ := srv.db.GetPendingConfirmation(ctx, chatID)
	handledNo, replyNo := srv.handlePendingConfirmation(ctx, chatID, "Não", pending)
	if !handledNo || !strings.Contains(replyNo, "Operação cancelada") {
		t.Errorf("expected cancellation message, got: %s", replyNo)
	}
	countAfterNo, _ := db.CountAllRoutines(ctx)
	if countAfterNo == 0 {
		t.Fatalf("routines should not have been deleted after 'Não'")
	}

	// 3. Responde "Sim" (deve apagar todos)
	_ = srv.db.SavePendingConfirmation(ctx, chatID, "limpar_lembretes", 0, "")
	pending2, _ := srv.db.GetPendingConfirmation(ctx, chatID)
	handledYes, replyYes := srv.handlePendingConfirmation(ctx, chatID, "Sim", pending2)
	if !handledYes || !strings.Contains(replyYes, "removidos com sucesso") {
		t.Errorf("expected success message, got: %s", replyYes)
	}

	countAfterYes, _ := db.CountAllRoutines(ctx)
	if countAfterYes != 0 {
		t.Fatalf("expected 0 routines after 'Sim', got %d", countAfterYes)
	}
}

func TestClearAllDataWithConfirmation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "atlas_clear_all_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "atlas.db")
	db, err := database.Open(dbPath, "", "")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	_, _ = db.InsertEventoCompleto(ctx, "Dentista", "2026-11-20 10:00:00", "09:00", "09:30", "pontual", nil, nil, "", 20, 30)
	_, _ = db.InsertRoutine(ctx, "Tomar creatina", "09:00", "custom", nil)

	cfg := &config.Config{
		DBPath:         dbPath,
		TelegramChatID: 999999,
	}
	srv := &Server{
		cfg: cfg,
		db:  db,
	}

	chatID := int64(999999)

	// Simular confirmação "Sim" para limpar tudo
	_ = srv.db.SavePendingConfirmation(ctx, chatID, "limpar_tudo", 0, "")
	pending, _ := srv.db.GetPendingConfirmation(ctx, chatID)
	handled, reply := srv.handlePendingConfirmation(ctx, chatID, "Sim", pending)
	if !handled || !strings.Contains(reply, "completamente limpo") {
		t.Errorf("expected clean message, got: %s", reply)
	}

	evCount, _ := db.CountAllEventos(ctx)
	rtCount, _ := db.CountAllRoutines(ctx)
	if evCount != 0 || rtCount != 0 {
		t.Errorf("expected 0 events and 0 routines, got %d events and %d routines", evCount, rtCount)
	}
}

func TestSonarBridgeCommand(t *testing.T) {
	sonarClient := sonar.NewClient("", "Li3cht3nst3in2", "/home/chocotonilitz/Programacao/projetos/sonar/sonar_bridge.php", "/home/chocotonilitz/Programacao/projetos")

	res, err := sonarClient.Execute(context.Background(), "echo 'SONAR_BRIDGE_ONLINE'", "")
	if err != nil {
		t.Fatalf("sonarClient.Execute failed: %v", err)
	}

	if !res.OK {
		t.Errorf("expected res.OK to be true")
	}

	if res.Output != "SONAR_BRIDGE_ONLINE" {
		t.Errorf("expected SONAR_BRIDGE_ONLINE, got: %s", res.Output)
	}
}

func TestMatchFastPathPriorities(t *testing.T) {
	testCases := []struct {
		input       string
		expectedCmd string
		expectMatch bool
	}{
		{"me mostre o calendario", "/mes", true},
		{"mostre o calendário", "/mes", true},
		{"calendário do mês", "/mes", true},
		{"ver calendario", "/mes", true},
		{"calendario", "/mes", true},
		{"o que tenho hoje", "/hoje", true},
		{"agenda de hoje", "/hoje", true},
		{"hoje", "/hoje", true},
		{"minha agenda", "/hoje", true},
		{"limpar agenda", "/limpar_agenda", true},
		{"apagar todos os compromissos", "/limpar_agenda", true},
		{"deletar agenda", "/limpar_agenda", true},
		{"excluir todos os compromissos", "/limpar_agenda", true},
		{"remover eventos", "/limpar_agenda", true},
		{"zerar compromissos", "/limpar_agenda", true},
		{"eliminar tarefas", "/limpar_agenda", true},
		{"deletar tudo", "/limpar_tudo", true},
		{"limpar tudo", "/limpar_tudo", true},
		{"apagar tudo", "/limpar_tudo", true},
		{"limpar lembretes", "/limpar_lembretes", true},
		{"apagar todos os lembretes", "/limpar_lembretes", true},
		{"deletar rotinas", "/limpar_lembretes", true},
		{"excluir todos os avisos", "/limpar_lembretes", true},
		{"zerar alarmes", "/limpar_lembretes", true},
		{"exibir metas", "/metas", true},
		{"listar objetivos", "/metas", true},
		{"consultar alvos", "/metas", true},
		{"mostrar projetos", "/ideias", true},
		{"listar ideias", "/ideias", true},
		{"listar rotinas", "/lembretes", true},
		{"exibir avisos", "/lembretes", true},
		{"ver mes", "/mes", true},
		{"grade mensal", "/mes", true},
		{"liturgia de hoje", "/liturgia", true},
		{"meu perfil", "/perfil", true},
		{"minhas metas", "/metas", true},
		{"git status", "/sonar git status", true},
		{"$ ls -la", "/sonar ls -la", true},
		{"sonar df -h", "/sonar df -h", true},
		{"py print(42)", "/py print(42)", true},
		{"python import sys; print(sys.version)", "/py import sys; print(sys.version)", true},
		{"crawl https://example.com", "/crawl https://example.com", true},
		{"swarm Qual a melhor arquitetura?", "/swarm Qual a melhor arquitetura?", true},
		{"conselho Devo aceitar a proposta?", "/swarm Devo aceitar a proposta?", true},
		{"briefing matinal", "/briefing", true},
		{"review", "/review", true},
		{"emails", "/emails", true},
		{"ver emails", "/emails", true},
		{"o que tem no meu email", "/emails", true},
	}

	for _, tc := range testCases {
		cmd, ok := matchFastPath(tc.input)
		if ok != tc.expectMatch {
			t.Errorf("input '%s': expected match %v, got %v", tc.input, tc.expectMatch, ok)
		}
		if ok && cmd != tc.expectedCmd {
			t.Errorf("input '%s': expected cmd '%s', got '%s'", tc.input, tc.expectedCmd, cmd)
		}
	}
}
