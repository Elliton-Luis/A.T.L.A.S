package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestBriefingConfigurationAndScheduleAreAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "briefing.db")
	db, err := Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	body := `[{"acao":"liturgia","titulo":"Liturgia"}]`
	if err := db.SaveBriefingConfig(ctx, body, "07:00"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveBriefingConfig(ctx, `[{"acao":"santo_do_dia"}]`, "25:99"); err == nil {
		t.Fatal("horário inválido aceito")
	}
	db.Close()
	db, err = Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	actual, err := db.GetBriefingConfig(ctx)
	if err != nil || actual != body {
		t.Fatalf("configuração perdeu atomicidade: %s (%v)", actual, err)
	}
	routines, err := db.GetActiveRoutines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, r := range routines {
		if r.Tipo == "briefing" {
			t.Fatal("emissor padrão continuou ativo")
		}
		if r.Nome == "Briefing matinal" && r.Horario == "07:00" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("esperado um briefing, recebido %d", count)
	}
}

func TestBriefingCacheIsDatedAndExpires(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "briefing.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.SaveBriefingCache(ctx, "2026-11-14", "liturgia", "conteúdo", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if content, err := db.GetBriefingCache(ctx, "2026-11-14", "liturgia"); err != nil || content != "conteúdo" {
		t.Fatal(content, err)
	}
	if content, _ := db.GetBriefingCache(ctx, "2026-11-15", "liturgia"); content != "" {
		t.Fatal("conteúdo de ontem retornou hoje")
	}
	if err := db.SaveBriefingCache(ctx, "2026-11-14", "liturgia", "velho", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if content, _ := db.GetBriefingCache(ctx, "2026-11-14", "liturgia"); content != "" {
		t.Fatal("cache expirado retornado")
	}
}
