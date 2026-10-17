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

// apiToken is what the tokens page shows for one row. The generated model has
// dbtype wrappers. A template cannot call ago or eq on them directly. The view
// types of inventory exist for the same reason.
type apiToken struct {
	ID         int64
	Name       string
	Scope      string
	CreatedAt  time.Time
	LastUsedAt time.Time // zero when the token has never been used.
	ExpiresAt  time.Time // zero when the token never expires; in local time.
	Expired    bool
}

// newAPIToken builds the row for t. It checks expiry against now, the clock of
// the rest of the page.
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

	// years and days are how far from now the token expires. If both are
	// zero, it never expires.
	years, days int
}

// tokenLifetimes are the expiry choices of the create dialog, the default
// first. The validate tag of the form lists the same values.
var tokenLifetimes = []tokenLifetime{
	{Value: "never", Label: "Never"},
	{Value: "30d", Label: "30 days", days: 30},
	{Value: "90d", Label: "90 days", days: 90},
	{Value: "1y", Label: "1 year", years: 1},
}

// tokenExpiry returns when a token expires, if created at now with the choice
// that value names. It returns the zero time when the token never expires, or
// value names no choice.
func tokenExpiry(value string, now time.Time) time.Time {
	for _, l := range tokenLifetimes {
		if l.Value == value && (l.years != 0 || l.days != 0) {
			return now.AddDate(l.years, 0, l.days)
		}
	}

	return time.Time{}
}

// tokensData is the settings page that lists the API tokens of a user.
type tokensData struct {
	view
	Tokens []apiToken

	// Lifetimes are the expiry choices of the create dialog.
	Lifetimes []tokenLifetime

	// PlaintextToken, NewName and NewScope are the completion state of the
	// token that createToken just made. They are set only on the page that
	// answers its POST. The plaintext is never stored. So only that page
	// shows it.
	PlaintextToken string
	NewName        string
	NewScope       string

	// Revoked marks the list region that revokeToken returns. So it announces
	// the removal once, only on that render.
	Revoked bool
}

// tokens serves the token settings page.
func (h *Handler) tokens(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		return h.renderTokens(w, r, sm, a, tokensData{})
	}
}

// renderTokens shows the token settings page, with the completion state in
// data from a create, if any.
func (h *Handler) renderTokens(w http.ResponseWriter, r *http.Request, sm *auth.Session, a *auth.Auth, data tokensData) error {
	ctx := r.Context()

	list, err := h.tokenList(ctx, sm, a, r)
	if err != nil {
		return err
	}

	data.view = view{
		Title:      "API tokens",
		Section:    "API tokens",
		Role:       sm.CurrentRole(ctx),
		SignedInAs: sm.CurrentUsername(ctx),
	}
	data.Tokens = list
	data.Lifetimes = tokenLifetimes

	h.htmlWriter.Success(w, r, templatePageTokens, data)

	return nil
}

// createToken makes a new token for the signed-in user, and answers with the
// list that shows its plaintext. The page is the response of the POST, not a
// redirect. The plaintext shows only this once. So it is never stored, also
// not in the session. A reload offers to send the form again, which would make
// a second token.
func (h *Handler) createToken(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type createTokenForm struct {
		Name  string `schema:"name" validate:"required,min=1,max=100"`
		Scope string `schema:"scope" validate:"required,oneof=read read_write"`

		// Expires names one of tokenLifetimes. A form without it, as from a
		// page from before the choice existed, makes a token that never
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

		// A token cannot do more than the account that makes it. A read user
		// gets only read tokens. So a read-only session cannot give itself
		// write access to the JSON API through a token.
		if dbtype.TokenScope(input.Scope) == dbtype.TokenReadWrite && !sm.CurrentRole(ctx).CanWrite() {
			return auth.ErrForbidden
		}

		expiresAt := tokenExpiry(input.Expires, h.store.Now())

		plaintext, _, err := a.CreateToken(ctx, userID, input.Name, dbtype.TokenScope(input.Scope), expiresAt)
		if err != nil {
			return err
		}

		return h.renderTokens(w, r, sm, a, tokensData{
			PlaintextToken: plaintext,
			NewName:        input.Name,
			NewScope:       input.Scope,
		})
	}
}

// revokeToken deletes a token of the signed-in user, and answers with the
// current list region. It returns the whole region. So the last revoke shows
// the "none yet" line instead of an empty table. Also, plaintext on the page
// from a create just before goes with the swap.
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

// tokenList reads the tokens of the signed-in user, newest first, as the view
// that the template renders.
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

// currentUserID reads the id that Login put in the session. The route is behind
// auth.Middleware. So if there is none here, the middleware passed a request
// that it should have redirected. This needs its own error.
func currentUserID(sm *auth.Session, r *http.Request) (int64, error) {
	id, ok := sm.CurrentUserID(r.Context())
	if !ok {
		return 0, fmt.Errorf("no signed-in user in session")
	}

	return id, nil
}
