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
		log.Println("using MongoDB via MONGO_URI")
	} else {
		log.Println("using the in-memory store: fine locally, but every serverless instance would see different events (set MONGO_URI)")
	}

	mux := http.NewServeMux()

	// Same paths Vercel serves: /api/v1/... (vercel.json also keeps /v1/... working).
	for _, prefix := range []string{"/api/v1", "/v1"} {
		mux.HandleFunc(prefix+"/health", config.Health)
		mux.HandleFunc(prefix+"/whoami", config.Whoami)
		mux.HandleFunc(prefix+"/command", config.Command)
		mux.HandleFunc(prefix+"/events", config.Events)
	}

	log.Printf("console API listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
