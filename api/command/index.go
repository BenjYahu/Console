package handler

import (
	"net/http"

	"github.com/BenjYahu/Console/internal/console"
)

func Handler(writer http.ResponseWriter, request *http.Request) {
	console.Default().Command(writer, request)
}
