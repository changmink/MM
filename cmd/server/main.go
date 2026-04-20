package main

import (
	"log"
	"net/http"
	"os"

	"github.com/chang/file_server/internal/handler"
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

	mux := http.NewServeMux()
	handler.Register(mux, dataDir, webDir)

	addr := ":8080"
	log.Printf("server starting on %s (data: %s)", addr, dataDir)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
