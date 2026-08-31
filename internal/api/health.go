package api

import (
	"errors"
	"net/http"
	"runtime/debug"

	"github.com/pushkar-anand/build-with-go/http/response"
)

func (h *Handler) healthHandler() response.HandlerFunc {
	info, ok := debug.ReadBuildInfo()

	type healthResponse struct {
		Version string `json:"version"`
	}

	var res *healthResponse
	if ok {
		res = &healthResponse{
			Version: info.Main.Version,
		}
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		if !ok {
			return errors.New("failed to read build info")
		}

		h.jsonWriter.Ok(w, r, res)

		return nil
	}
}
