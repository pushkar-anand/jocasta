package web

import (
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
)

// loginData is what the login page needs to render standalone -- it carries
// no view, since that struct is what the signed-in shell needs and this page
// has none of it.
type loginData struct {
	Title string
	Error string
}

// login serves the sign-in page. /login has to stay reachable without a
// session, so auth.Middleware does not gate it -- checking for one already
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
		// on htmlWriter turn it into the sign-in page with its message, rather
		// than this handler rendering that page itself.
		if _, err := a.Login(ctx, sm, input.Username, input.Password, input.RememberMe); err != nil {
			return err
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
