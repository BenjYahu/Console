// Command server runs the console API locally on PORT (default 8080), for development and for
// hosts that want a plain long-running process instead of serverless functions.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/BenjYahu/Console/internal/console"
)

func main() {
	port := os.Getenv("PORT")

	if port == "" {
		port = "8080"
	}

	config := console.Default()

	for _, warning := range config.Warnings() {
		log.Println("warning: " + warning)
	}

	if config.Shared {
		log.Println("using the shared KV store")
	} else {
		log.Println("using the in-memory store: fine locally, but every serverless instance would see different events")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", config.Health)
	mux.HandleFunc("/v1/whoami", config.Whoami)
	mux.HandleFunc("/v1/command", config.Command)
	mux.HandleFunc("/v1/events", config.Events)

	log.Printf("console API listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
