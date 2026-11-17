package handler

import (
	"net/http"

	"github.com/revoke-wtf/console-api/internal/console"
)

// Handler serves the /v1/command endpoint.
func Handler(writer http.ResponseWriter, request *http.Request) {
	console.Default().Command(writer, request)
}
