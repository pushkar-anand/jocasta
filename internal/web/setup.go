package web

import (
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
)

// setupData is what the setup page needs to render standalone -- it carries
// no view, same as loginData, since this page has no signed-in shell either.
type setupData struct {
	Title string
	Error string
}

// setup serves the one-time first-account page. Reaching it at all already
// means the session middleware found no account yet -- CreateFirstUser is
// what refuses a second attempt, not this handler.
func (h *Handler) setup() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		h.htmlWriter.Success(w, r, TemplateSetup, setupData{Title: "Set up admin account"})
		return nil
	}
}

func (h *Handler) setupForm(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type setupForm struct {
		Username string `schema:"username" validate:"required,min=3,max=100"`
		Password string `schema:"password" validate:"required,min=8,max=1000"`
	}
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		input, err := h.reader.ReadAndValidateForm[setupForm](r)
		if err != nil {
			return err
		}

		if _, err := a.CreateFirstUser(ctx, sm, input.Username, input.Password); err != nil {
			return err
		}

		http.Redirect(w, r, "/", http.StatusFound)

		return nil
	}
}
