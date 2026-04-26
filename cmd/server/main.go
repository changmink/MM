package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"file_server/internal/handler"
	"file_server/internal/settings"
)

func main() {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "/data"
	}
	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		webDir = "web"
	}

	settingsStore, err := settings.New(dataDir)
	if err != nil {
		log.Fatalf("settings: %v", err)
	}

	// serverCtx is cancelled by SIGINT/SIGTERM. Long-lived background work
	// (import job registry, future workers) derives from it via WithServerCtx
	// so graceful shutdown unwinds them cleanly.
	serverCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	mux := http.NewServeMux()
	h := handler.Register(mux, dataDir, webDir, settingsStore, handler.WithServerCtx(serverCtx))

	srv := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		log.Printf("server starting on %s (data: %s)", srv.Addr, dataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-serverCtx.Done()
	log.Println("shutdown: draining connections")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	h.Close()
	log.Println("shutdown: complete")
}
