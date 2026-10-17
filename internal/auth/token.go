package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// tokenPrefix marks a value as a jocasta token before a lookup. So a bearer
// value from a different source fails at once, before a database round
// trip.
const tokenPrefix = "jct_"

// tokenEntropyBytes is the amount of random data in a token.
const tokenEntropyBytes = 32

// CreateToken makes a new API token for userID, and returns its plaintext.
// Only the hash of the token is kept, as for a password. So only at this time
// can a caller show the plaintext to its user. The token stops working at
// expiresAt. If expiresAt is zero, it never stops.
func (a *Auth) CreateToken(
	ctx context.Context,
	userID int64,
	name string,
	scope dbtype.TokenScope,
	expiresAt time.Time,
) (plaintext string, token *models.ApiToken, err error) {
	if !scope.Valid() {
		return "", nil, fmt.Errorf("token scope %q: %w", scope, ErrInvalidToken)
	}

	plaintext, err = generateToken()
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	var expires dbtype.NullTime
	if !expiresAt.IsZero() {
		expires = dbtype.NewNullTime(expiresAt)
	}

	token, err = a.store.CreateAPIToken(ctx, models.CreateAPITokenParams{
		UserID:    userID,
		Name:      name,
		TokenHash: hashToken(plaintext),
		Scope:     scope,
		ExpiresAt: expires,
	})
	if err != nil {
		return "", nil, fmt.Errorf("create token: %w", err)
	}

	return plaintext, token, nil
}

// VerifyToken returns the stored token that plaintext names, and records that
// it was used. It returns [ErrInvalidToken] when plaintext names no token, or
// an expired token.
func (a *Auth) VerifyToken(ctx context.Context, plaintext string) (*models.ApiToken, error) {
	if !strings.HasPrefix(plaintext, tokenPrefix) {
		return nil, ErrInvalidToken
	}

	// This runs on every API request. So the lookup and the last_used_at
	// update share one query.
	token, err := a.store.TouchAPITokenByHash(ctx, models.TouchAPITokenByHashParams{
		Now:       dbtype.NewNullTime(a.now()),
		TokenHash: hashToken(plaintext),
	})

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrInvalidToken
	case err != nil:
		return nil, fmt.Errorf("token lookup: %w", err)
	}

	return token, nil
}

// ListTokens returns the tokens of userID, newest first.
func (a *Auth) ListTokens(ctx context.Context, userID int64) ([]*models.ApiToken, error) {
	return a.store.ListAPITokensByUser(ctx, userID)
}

// RevokeToken deletes token id when userID owns it. It does not change a token
// that does not exist, or that belongs to a different user. It then returns no
// error.
func (a *Auth) RevokeToken(ctx context.Context, userID, id int64) error {
	return a.store.DeleteAPIToken(ctx, models.DeleteAPITokenParams{ID: id, UserID: userID})
}

// generateToken returns a new random bearer token with tokenPrefix.
func generateToken() (string, error) {
	b := make([]byte, tokenEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return tokenPrefix + hex.EncodeToString(b), nil
}

// hashToken is the form in which a token is stored and looked up. A plain
// SHA-256 is enough. The token has 256 random bits. So a slow hash would add
// no protection against guesses.
func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
