package api

import (
	"log/slog"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
)

type Handler struct {
	*http.ServeMux
	jsonWriter *response.JSONWriter
}

func NewHandler(l *slog.Logger) http.Handler {
	jw := response.NewJSONWriter(
		l,
	)

	mux := http.NewServeMux()

	mux.HandleFunc("/livez", jw.Handle(healthHandler(jw)))

	return &Handler{
		ServeMux:   mux,
		jsonWriter: jw,
	}
}
