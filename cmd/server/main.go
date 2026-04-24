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

	"github.com/chang/file_server/internal/handler"
	"github.com/chang/file_server/internal/settings"
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

	mux := http.NewServeMux()
	h := handler.Register(mux, dataDir, webDir, settingsStore)

	srv := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		log.Printf("server starting on %s (data: %s)", srv.Addr, dataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutdown: draining connections")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	h.Close()
	log.Println("shutdown: complete")
}
