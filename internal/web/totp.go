package web

import (
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/auth"
)

// totpData is what the second-factor page needs to render alone. It has no
// view, as for loginData, because this page also has no signed-in shell.
type totpData struct {
	Title string
	Error string
	// Recovery sets up the code field for a recovery code. This is a full
	// keyboard, instead of the numeric keypad for an authenticator code.
	Recovery bool
}

// totpMethodRecovery is the method value that asks for the recovery-code form
// of the page. It is a query parameter on GET, and a form field on POST.
const totpMethodRecovery = "recovery"

// loginTOTP serves the second-factor page. A request with no pending sign-in
// (never started, or its session is gone) gets the same answer as a bad
// /login attempt. There is nothing more to say about it. ?method=recovery
// serves the recovery-code form of the page, which works without
// JavaScript.
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

		h.htmlWriter.Success(w, r, TemplateTOTP, totpData{
			Title:    "Enter your code",
			Recovery: r.URL.Query().Get("method") == totpMethodRecovery,
		})

		return nil
	}
}

// loginTOTPForm completes a pending sign-in with an authenticator code or a
// recovery code. A successful recovery code cannot be used again.
func (h *Handler) loginTOTPForm(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type totpForm struct {
		// max=64 fits a recovery code (xxxx-xxxx-xxxx-xxxx, 19 chars) and a
		// 6-digit TOTP value. One field covers both.
		Code string `schema:"code" validate:"required,min=6,max=64"`
		// Method is the page form of the code. Verification ignores it,
		// because it accepts both kinds of code. An error page reads it to
		// return the visitor to the same form.
		Method string `schema:"method" validate:"omitempty,oneof=recovery"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		input, err := h.reader.ReadAndValidateForm[totpForm](r)
		if err != nil {
			return err
		}

		if _, err := a.VerifyTOTP(ctx, sm, input.Code); err != nil {
			h.log.WarnContext(ctx, "sign-in refused", logger.Err(err))
			return err
		}

		http.Redirect(w, r, "/", http.StatusFound)

		return nil
	}
}
