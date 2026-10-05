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

// apiToken is what the tokens page shows for one row. The generated model
// carries dbtype wrappers a template cannot call ago or eq against directly,
// the same reason inventory's own view types exist.
type apiToken struct {
	ID         int64
	Name       string
	Scope      string
	CreatedAt  time.Time
	LastUsedAt time.Time // zero when the token has never been used.
	ExpiresAt  time.Time // zero when the token never expires; in local time.
	Expired    bool
}

// newAPIToken builds the row for t, judging expiry against now, the clock the
// rest of the page reads.
func newAPIToken(t *models.ApiToken, now time.Time) apiToken {
	v := apiToken{
		ID:        t.ID,
		Name:      t.Name,
		Scope:     string(t.Scope),
		CreatedAt: t.CreatedAt.Time,
	}

	if t.LastUsedAt.Valid {
		v.LastUsedAt = t.LastUsedAt.Time.Time
	}

	if t.ExpiresAt.Valid {
		v.ExpiresAt = t.ExpiresAt.Time.Local()
		v.Expired = !now.Before(v.ExpiresAt)
	}

	return v
}

// tokenLifetime is one expiry choice in the create dialog.
type tokenLifetime struct {
	Value string // the form value.
	Label string

	// years and days are how far from now the token expires. Both zero
	// means it never does.
	years, days int
}

// tokenLifetimes are the create dialog's expiry choices, the default first.
// The form's validate tag lists the same values.
var tokenLifetimes = []tokenLifetime{
	{Value: "never", Label: "Never"},
	{Value: "30d", Label: "30 days", days: 30},
	{Value: "90d", Label: "90 days", days: 90},
	{Value: "1y", Label: "1 year", years: 1},
}

// tokenExpiry returns when a token created at now with the choice named value
// expires, or the zero time when it never does or value names no choice.
func tokenExpiry(value string, now time.Time) time.Time {
	for _, l := range tokenLifetimes {
		if l.Value == value && (l.years != 0 || l.days != 0) {
			return now.AddDate(l.years, 0, l.days)
		}
	}

	return time.Time{}
}

// tokensData is the settings page listing a user's API tokens.
type tokensData struct {
	view
	Tokens []apiToken

	// Lifetimes are the expiry choices the create dialog offers.
	Lifetimes []tokenLifetime

	// PlaintextToken, NewName and NewScope are the one-shot completion state for
	// the token createToken just made. The plaintext is never stored, so the GET
	// the create redirects to is the only load it appears on; name and scope
	// ride a flash so the block can label what was made.
	PlaintextToken string
	NewName        string
	NewScope       string

	// Revoked marks the list region revokeToken returns, so it announces the
	// removal once, on that render alone.
	Revoked bool
}

// One-shot flashes createToken leaves for the GET it redirects to. The
// plaintext waits on the handler's shelf and the flash holds only its shelf
// id; name and scope label the completion block beside it.
const (
	flashTokenShelfID = "flash.token_shelf_id"
	flashTokenName    = "flash.token_name"
	flashTokenScope   = "flash.token_scope"
)

// tokens serves the token settings page.
func (h *Handler) tokens(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		list, err := h.tokenList(ctx, sm, a, r)
		if err != nil {
			return err
		}

		plaintext, _ := h.secrets.take(sm.PopFlash(ctx, flashTokenShelfID))

		h.htmlWriter.Success(w, r, templatePageTokens, tokensData{
			Title:          "API tokens",
			Section:        "API tokens",
			Role:           sm.CurrentRole(ctx),
			SignedInAs:     sm.CurrentUsername(ctx),
			Tokens:         list,
			Lifetimes:      tokenLifetimes,
			PlaintextToken: plaintext,
			NewName:        sm.PopFlash(ctx, flashTokenName),
			NewScope:       sm.PopFlash(ctx, flashTokenScope),
		})

		return nil
	}
}

// createToken issues a new token for the signed-in user, then redirects to the
// list. The plaintext, which can be seen only this once, rides the redirect on
// the handler's shelf, so reloading the landing page does not mint a second
// token.
func (h *Handler) createToken(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type createTokenForm struct {
		Name  string `schema:"name" validate:"required,min=1,max=100"`
		Scope string `schema:"scope" validate:"required,oneof=read read_write"`

		// Expires names one of tokenLifetimes. A form without it, as from a
		// page loaded before the choice existed, makes a token that never
		// expires.
		Expires string `schema:"expires" validate:"omitempty,oneof=never 30d 90d 1y"`
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
		// read tokens only, so a read-only session cannot hand itself write
		// access to the JSON API through one.
		if dbtype.TokenScope(input.Scope) == dbtype.TokenReadWrite && !sm.CurrentRole(ctx).CanWrite() {
			return auth.ErrForbidden
		}

		expiresAt := tokenExpiry(input.Expires, h.store.Now())

		plaintext, _, err := a.CreateToken(ctx, userID, input.Name, dbtype.TokenScope(input.Scope), expiresAt)
		if err != nil {
			return err
		}

		sm.Flash(ctx, flashTokenShelfID, h.secrets.put(plaintext))
		sm.Flash(ctx, flashTokenName, input.Name)
		sm.Flash(ctx, flashTokenScope, input.Scope)
		http.Redirect(w, r, "/settings/tokens", http.StatusSeeOther)

		return nil
	}
}

// revokeToken deletes one of the signed-in user's tokens and answers with the
// list region as it now stands. It returns the whole region so the last revoke
// shows the "none yet" line in place of an empty table, and so any plaintext
// still on the page from a create just before it goes with the swap.
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

		list, err := h.tokenList(ctx, sm, a, r)
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePartialTokenList, tokensData{Tokens: list, Revoked: true})

		return nil
	}
}

// tokenList reads the signed-in user's tokens, newest first, as the view the
// template renders.
func (h *Handler) tokenList(ctx context.Context, sm *auth.Session, a *auth.Auth, r *http.Request) ([]apiToken, error) {
	userID, err := currentUserID(sm, r)
	if err != nil {
		return nil, err
	}

	rows, err := a.ListTokens(ctx, userID)
	if err != nil {
		return nil, err
	}

	now := h.store.Now()

	list := make([]apiToken, len(rows))
	for i, row := range rows {
		list[i] = newAPIToken(row, now)
	}

	return list, nil
}

// currentUserID reads the id Login put in the session. The route sits behind
// auth.Middleware, so finding none here means the middleware let through a
// request it should have redirected, which is worth its own error.
func currentUserID(sm *auth.Session, r *http.Request) (int64, error) {
	id, ok := sm.CurrentUserID(r.Context())
	if !ok {
		return 0, fmt.Errorf("no signed-in user in session")
	}

	return id, nil
}
