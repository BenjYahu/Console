package handler

import (
	"net/http"

	"github.com/revoke-wtf/console-api/internal/console"
)

func Handler(writer http.ResponseWriter, request *http.Request) {
	console.Default().Command(writer, request)
}
