package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/pushkar-anand/build-with-go/ctxval"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// loginData is what the login page needs to render standalone. It carries no
// view, since that struct is what the signed-in shell needs and this page has
// none of it.
type loginData struct {
	Title      string
	Error      string
	Username   string
	RememberMe bool
}

// login serves the sign-in page. /login has to stay reachable without a
// session, so auth.Middleware does not gate it, and checking for one already
// held is this handler's own job.
func (h *Handler) login(
	sm *auth.Session,
) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if _, ok := sm.CurrentUserID(r.Context()); ok {
			http.Redirect(w, r, "/", http.StatusFound)
			return nil
		}

		h.htmlWriter.Success(w, r, TemplateLogin, loginData{Title: "Sign in"})

		return nil
	}
}

// loginForm verifies credentials and redirects to the overview or the second
// authentication step. Authentication failures use the shared error renderer.
func (h *Handler) loginForm(
	sm *auth.Session,
	a *auth.Auth,
) response.HandlerFunc {
	type loginForm struct {
		Username   string `schema:"username" validate:"required,min=3,max=100"`
		Password   string `schema:"password" validate:"required,min=8,max=1000"`
		RememberMe bool   `schema:"remember_me"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		input, err := h.reader.ReadAndValidateForm[loginForm](r)
		if err != nil {
			return err
		}

		// auth.ErrInvalidCredentials reaches the client the same way any other
		// handler error does: the status mapper and error-page data configured
		// on htmlWriter turn it into the sign-in page with its message.
		// No address, as over a Unix socket, leaves only the account's limit.
		addr, _ := ctxval.ClientAddrFromContext(ctx)

		result, err := a.Login(ctx, sm, addr, input.Username, input.Password, input.RememberMe)
		if err != nil {
			h.logRefused(ctx, addr, err)
			return err
		}

		if result.TOTPPending {
			http.Redirect(w, r, "/login/totp", http.StatusFound)
			return nil
		}

		h.logSignedIn(ctx, addr, result.User)
		http.Redirect(w, r, "/", http.StatusFound)

		return nil
	}
}

// logSignedIn records a completed sign-in: the account, and the client's
// address under the access log's own key, so the two lines can be joined.
func (h *Handler) logSignedIn(ctx context.Context, addr netip.Addr, user *models.User) {
	h.log.InfoContext(ctx, "signed in",
		slog.String("user", user.Username),
		slog.Int64("user_id", user.ID),
		remoteIP(addr),
	)
}

// logRefused logs why err refused a sign-in step, and the client's address.
// The username as typed stays out of the log, since people type a password
// into that field often enough. Other errors are not logged here.
func (h *Handler) logRefused(ctx context.Context, addr netip.Addr, err error) {
	var reason string

	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		reason = "username and password do not match"
	case errors.Is(err, auth.ErrLoginLocked):
		reason = "too many passwords"
	case errors.Is(err, auth.ErrInvalidTOTPCode):
		reason = "code does not match"
	case errors.Is(err, auth.ErrTOTPLocked):
		reason = "too many codes"
	default:
		return
	}

	h.log.WarnContext(ctx, "sign-in refused", slog.String("reason", reason), remoteIP(addr))
}

// remoteIP returns addr as the access log's remote_ip attribute, or an empty
// attribute, which slog drops, when the connection had no IP address.
func remoteIP(addr netip.Addr) slog.Attr {
	if !addr.IsValid() {
		return slog.Attr{}
	}

	return slog.String("remote_ip", addr.String())
}

func (h *Handler) logout(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := sm.Logout(r.Context()); err != nil {
			return err
		}

		http.Redirect(w, r, "/login", http.StatusFound)

		return nil
	}
}
