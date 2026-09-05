package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// apiToken is what the tokens page shows for one row -- the generated model
// carries dbtype wrappers a template cannot call ago or eq against directly,
// the same reason inventory's own view types exist.
type apiToken struct {
	ID         int64
	Name       string
	Scope      string
	CreatedAt  time.Time
	LastUsedAt time.Time // zero when the token has never been used.
}

func newAPIToken(t *models.ApiToken) apiToken {
	v := apiToken{
		ID:        t.ID,
		Name:      t.Name,
		Scope:     string(t.Scope),
		CreatedAt: t.CreatedAt.Time,
	}

	if t.LastUsedAt.Valid {
		v.LastUsedAt = t.LastUsedAt.Time.Time
	}

	return v
}

// tokensData is the settings page listing a user's API tokens.
type tokensData struct {
	view
	Tokens []apiToken

	// PlaintextToken is the token CreateToken just returned, shown once on the
	// response to its own create -- it is never stored, so this is the only
	// page load it can appear on.
	PlaintextToken string
}

// tokens serves the token settings page.
func (h *Handler) tokens(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		list, err := tokenList(r.Context(), sm, a, r)
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePageTokens, tokensData{
			Title:  "API tokens",
			Tokens: list,
		})

		return nil
	}
}

// createToken issues a new token for the signed-in user and shows its
// plaintext once, on this response alone.
func (h *Handler) createToken(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type createTokenForm struct {
		Name  string `schema:"name" validate:"required,min=1,max=100"`
		Scope string `schema:"scope" validate:"required,oneof=read read_write"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		userID, err := currentUserID(sm, r)
		if err != nil {
			return err
		}

		input, err := h.reader.ReadAndValidateForm[createTokenForm](r)
		if err != nil {
			return err
		}

		plaintext, _, err := a.CreateToken(ctx, userID, input.Name, dbtype.TokenScope(input.Scope))
		if err != nil {
			return err
		}

		list, err := tokenList(ctx, sm, a, r)
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePageTokens, tokensData{
			Title:          "API tokens",
			Tokens:         list,
			PlaintextToken: plaintext,
		})

		return nil
	}
}

// revokeToken deletes one of the signed-in user's tokens. It answers with no
// content: the row it was in is gone from the client's own list once htmx
// swaps it out, and there is nothing else for this response to say.
func (h *Handler) revokeToken(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := currentUserID(sm, r)
		if err != nil {
			return err
		}

		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		if err := a.RevokeToken(r.Context(), userID, id); err != nil {
			return err
		}

		w.WriteHeader(http.StatusOK)

		return nil
	}
}

// tokenList reads the signed-in user's tokens, newest first, as the view the
// template renders.
func tokenList(ctx context.Context, sm *auth.Session, a *auth.Auth, r *http.Request) ([]apiToken, error) {
	userID, err := currentUserID(sm, r)
	if err != nil {
		return nil, err
	}

	rows, err := a.ListTokens(ctx, userID)
	if err != nil {
		return nil, err
	}

	list := make([]apiToken, len(rows))
	for i, row := range rows {
		list[i] = newAPIToken(row)
	}

	return list, nil
}

// currentUserID reads the id Login put in the session. The route sits behind
// auth.Middleware, so finding none here means the middleware let through a
// request it should have redirected -- worth its own error rather than a
// silently wrong id.
func currentUserID(sm *auth.Session, r *http.Request) (int64, error) {
	id, ok := sm.CurrentUserID(r.Context())
	if !ok {
		return 0, fmt.Errorf("no signed-in user in session")
	}

	return id, nil
}
