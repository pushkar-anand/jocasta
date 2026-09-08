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

	// PlaintextToken, NewName and NewScope are the one-shot completion state for
	// the token createToken just made. The plaintext is never stored, so the GET
	// the create redirects to is the only load it appears on; name and scope
	// ride the same flash so the block can label what was made.
	PlaintextToken string
	NewName        string
	NewScope       string

	// Revoked marks the list region revokeToken returns, so it announces the
	// removal once rather than on every render of the list.
	Revoked bool
}

// One-shot flashes createToken leaves for the GET it redirects to. The
// plaintext is the one thing never stored; name and scope label the completion
// block beside it.
const (
	flashTokenPlaintext = "flash.token_plaintext"
	flashTokenName      = "flash.token_name"
	flashTokenScope     = "flash.token_scope"
)

// tokens serves the token settings page.
func (h *Handler) tokens(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		list, err := tokenList(ctx, sm, a, r)
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePageTokens, tokensData{
			Title:          "API tokens",
			Section:        "API tokens",
			Role:           sm.CurrentRole(ctx),
			SignedInAs:     sm.CurrentUsername(ctx),
			Tokens:         list,
			PlaintextToken: sm.PopFlash(ctx, flashTokenPlaintext),
			NewName:        sm.PopFlash(ctx, flashTokenName),
			NewScope:       sm.PopFlash(ctx, flashTokenScope),
		})

		return nil
	}
}

// createToken issues a new token for the signed-in user, then redirects to the
// list. The plaintext -- the one thing this is the only chance to see -- rides
// the redirect in a one-shot flash, so a reload of the landing page re-fetches
// it rather than minting a second token.
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

		// A token cannot out-reach the account that mints it: a read user gets
		// read tokens only, so a read-only session can't hand itself write
		// access to the JSON API through one.
		if dbtype.TokenScope(input.Scope) == dbtype.TokenReadWrite && !sm.CurrentRole(ctx).CanWrite() {
			return auth.ErrForbidden
		}

		plaintext, _, err := a.CreateToken(ctx, userID, input.Name, dbtype.TokenScope(input.Scope))
		if err != nil {
			return err
		}

		sm.Flash(ctx, flashTokenPlaintext, plaintext)
		sm.Flash(ctx, flashTokenName, input.Name)
		sm.Flash(ctx, flashTokenScope, input.Scope)
		http.Redirect(w, r, "/settings/tokens", http.StatusSeeOther)

		return nil
	}
}

// revokeToken deletes one of the signed-in user's tokens and answers with the
// list region as it now stands. It returns the whole region rather than an
// empty row so the last revoke shows the "none yet" line instead of a table
// with no body, and so any plaintext still on the page from a create just
// before it goes with the swap.
func (h *Handler) revokeToken(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		userID, err := currentUserID(sm, r)
		if err != nil {
			return err
		}

		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		if err := a.RevokeToken(ctx, userID, id); err != nil {
			return err
		}

		list, err := tokenList(ctx, sm, a, r)
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePartialTokenList, tokensData{Tokens: list, Revoked: true})

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
