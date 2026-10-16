package web

import (
	"net/http"

	"github.com/pushkar-anand/build-with-go/ctxval"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/auth"
)

// loginData is what the login page needs to render alone. It has no view. That
// struct is for the signed-in shell, and this page has no shell.
type loginData struct {
	Title      string
	Error      string
	Username   string
	RememberMe bool
}

// login serves the sign-in page. /login must stay reachable without a session.
// So auth.Middleware does not gate it. This handler must check for an existing
// session itself.
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

// loginForm verifies credentials. It redirects to the overview, or to the
// second authentication step. Authentication failures use the shared error
// renderer.
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

		// auth.ErrInvalidCredentials reaches the client as any other handler
		// error does. The status mapper and error-page data of htmlWriter turn
		// it into the sign-in page with its message. With no address, as over
		// a Unix socket, only the limit of the account applies.
		addr, _ := ctxval.ClientAddrFromContext(ctx)

		result, err := a.Login(ctx, sm, addr, input.Username, input.Password, input.RememberMe)
		if err != nil {
			h.log.WarnContext(ctx, "sign-in refused", logger.Err(err))
			return err
		}

		if result.TOTPPending {
			http.Redirect(w, r, "/login/totp", http.StatusFound)
			return nil
		}

		http.Redirect(w, r, "/", http.StatusFound)

		return nil
	}
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
