package api

import (
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/version"
)

func (h *Handler) healthHandler() response.HandlerFunc {
	type healthResponse struct {
		Version string    `json:"version"`
		Time    time.Time `json:"time"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		res := healthResponse{
			Version: version.Get().Version,
			Time:    time.Now(),
		}

		h.jsonWriter.Ok(w, r, res)

		return nil
	}
}
