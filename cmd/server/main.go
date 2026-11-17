package main

import (
	"log"
	"net/http"
	"os"

	"github.com/revoke-wtf/console-api/internal/console"
)

func main() {
	port := os.Getenv("PORT")

	if port == "" {
		port = "8080"
	}

	config := console.Default()

	if config.Secret == "" {
		log.Println("warning: CONSOLE_SECRET is empty, every request will be refused")
	}

	if len(config.Accounts) == 0 {
		log.Println("warning: CONSOLE_ACCOUNTS is empty, no account may use the console")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", config.Health)
	mux.HandleFunc("/v1/whoami", config.Whoami)
	mux.HandleFunc("/v1/command", config.Command)
	mux.HandleFunc("/v1/events", config.Events)

	log.Printf("console API listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
