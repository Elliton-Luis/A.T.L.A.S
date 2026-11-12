package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"atlas/pkg/config"
	"atlas/pkg/server"
)

func main() {
	log.Println("[ATLAS] Inicializando assistente pessoal executivo (Standalone)...")

	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("[FATAL] Erro ao carregar configurações: %v", err)
	}

	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("[FATAL] Erro ao inicializar servidor: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Inicia o motor de background para o modo binário contínuo
	srv.StartScheduler(ctx)

	httpServer := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[ATLAS] Servidor HTTP operando na porta :%s (Timezone: %s)\n", cfg.HTTPPort, cfg.Timezone)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Erro no servidor HTTP: %v", err)
		}
	}()

	<-sigChan
	log.Println("[ATLAS] Encerrando com segurança...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	log.Println("[ATLAS] Desligado com sucesso.")
}
