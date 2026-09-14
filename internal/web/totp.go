package web

import (
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
)

// totpData is what the second-factor page needs to render standalone -- it
// carries no view, same as loginData, since this page has no signed-in shell
// either.
type totpData struct {
	Title string
	Error string
}

// loginTOTP serves the second-factor page. Reaching it with no pending
// sign-in -- never started, or the session that started it is gone --
// answers the same way a bad /login attempt does, since there's no more to
// say about it than that.
func (h *Handler) loginTOTP(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		if _, ok := sm.CurrentUserID(ctx); ok {
			http.Redirect(w, r, "/", http.StatusFound)
			return nil
		}

		if !sm.HasPendingTOTP(ctx) {
			return auth.ErrInvalidCredentials
		}

		h.htmlWriter.Success(w, r, TemplateTOTP, totpData{Title: "Enter your code"})

		return nil
	}
}

func (h *Handler) loginTOTPForm(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type totpForm struct {
		// max=64 fits a recovery code (xxxx-xxxx-xxxx-xxxx, 19 chars), not just
		// a 6-digit TOTP value -- one field covers both.
		Code string `schema:"code" validate:"required,min=6,max=64"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		input, err := h.reader.ReadAndValidateForm[totpForm](r)
		if err != nil {
			return err
		}

		if _, err := a.VerifyTOTP(ctx, sm, input.Code); err != nil {
			return err
		}

		http.Redirect(w, r, "/", http.StatusFound)

		return nil
	}
}
