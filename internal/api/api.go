package api

import (
	"log/slog"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/db"
)

type Handler struct {
	*http.ServeMux
	jsonWriter *response.JSONWriter
}

func NewHandler(l *slog.Logger, conn *db.DB) http.Handler {
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
