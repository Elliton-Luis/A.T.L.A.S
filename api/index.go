package handler

import (
	"log"
	"net/http"
	"sync"

	"atlas/pkg/config"
	"atlas/pkg/server"
)

var (
	srv     *server.Server
	once    sync.Once
	initErr error
)

func initServer() {
	cfg, err := config.Load("")
	if err != nil {
		initErr = err
		log.Printf("[Vercel] Falha ao carregar configuracoes: %v", err)
		return
	}

	s, err := server.New(cfg)
	if err != nil {
		initErr = err
		log.Printf("[Vercel] Falha ao inicializar servidor ATLAS: %v", err)
		return
	}
	srv = s
}

// Handler is the Vercel Serverless Function entrypoint.
func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(initServer)
	if initErr != nil {
		http.Error(w, "Atlas Serverless Error: "+initErr.Error(), http.StatusInternalServerError)
		return
	}
	srv.ServeHTTP(w, r)
}
