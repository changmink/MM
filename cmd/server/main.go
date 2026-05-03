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

	// serverCtx는 SIGINT/SIGTERM에 의해 취소된다. 장기 백그라운드 작업
	// (import Job 레지스트리, 향후 워커들)이 WithServerCtx를 통해 이 컨텍스트를
	// 파생하므로, graceful shutdown 시 모두 깔끔하게 풀린다.
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
