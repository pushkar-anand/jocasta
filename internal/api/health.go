package api

import (
	"errors"
	"net/http"
	"runtime/debug"

	"github.com/pushkar-anand/build-with-go/http/response"
)

func healthHandler(jw *response.JSONWriter) response.HandlerFunc {
	info, ok := debug.ReadBuildInfo()
	return func(w http.ResponseWriter, r *http.Request) error {
		if !ok {
			return errors.New("failed to read build info")
		}

		jw.Ok(w, r, map[string]string{
			"version": info.Main.Version,
		})

		return nil
	}
}
