package api

import (
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/version"
)

type healthResponse struct {
	Version string `json:"version"`
}

func (h *Handler) healthHandler() response.HandlerFunc {
	res := healthResponse{Version: version.Get().Version}

	return func(w http.ResponseWriter, r *http.Request) error {
		h.jsonWriter.Ok(w, r, res)

		return nil
	}
}
