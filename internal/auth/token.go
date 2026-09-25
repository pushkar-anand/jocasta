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

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// tokenPrefix marks a value as one of ours before it is ever looked up, so a
// bearer value from somewhere else fails on sight, before any database round
// trip.
const tokenPrefix = "jct_"

// tokenEntropyBytes is how much random material backs a token.
const tokenEntropyBytes = 32

// CreateToken issues a new API token for userID and returns its plaintext.
// Only the token's hash is kept, as with a password, so this is the one moment
// a caller can show the plaintext to whoever is meant to use it.
func (a *Auth) CreateToken(
	ctx context.Context,
	userID int64,
	name string,
	scope dbtype.TokenScope,
) (plaintext string, token *models.ApiToken, err error) {
	if !scope.Valid() {
		return "", nil, fmt.Errorf("token scope %q: %w", scope, ErrInvalidToken)
	}

	plaintext, err = generateToken()
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	token, err = a.tokens.CreateAPIToken(ctx, models.CreateAPITokenParams{
		UserID:    userID,
		Name:      name,
		TokenHash: hashToken(plaintext),
		Scope:     scope,
	})
	if err != nil {
		return "", nil, fmt.Errorf("create token: %w", err)
	}

	return plaintext, token, nil
}

// VerifyToken returns the stored token plaintext names and records that it was
// used. It returns [ErrInvalidToken] when plaintext names no token.
func (a *Auth) VerifyToken(ctx context.Context, plaintext string) (*models.ApiToken, error) {
	if !strings.HasPrefix(plaintext, tokenPrefix) {
		return nil, ErrInvalidToken
	}

	// This runs on every API request, so the lookup and the last_used_at
	// update share one query.
	token, err := a.tokens.TouchAPITokenByHash(ctx, models.TouchAPITokenByHashParams{
		LastUsedAt: dbtype.NewNullTime(a.now()),
		TokenHash:  hashToken(plaintext),
	})

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrInvalidToken
	case err != nil:
		return nil, fmt.Errorf("token lookup: %w", err)
	}

	return token, nil
}

// ListTokens returns userID's tokens, newest first.
func (a *Auth) ListTokens(ctx context.Context, userID int64) ([]*models.ApiToken, error) {
	return a.tokens.ListAPITokensByUser(ctx, userID)
}

// RevokeToken deletes token id when userID owns it. A token that does not
// exist or belongs to someone else is left alone, and no error is returned.
func (a *Auth) RevokeToken(ctx context.Context, userID, id int64) error {
	return a.tokens.DeleteAPIToken(ctx, models.DeleteAPITokenParams{ID: id, UserID: userID})
}

// generateToken returns a new random bearer token carrying tokenPrefix.
func generateToken() (string, error) {
	b := make([]byte, tokenEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return tokenPrefix + hex.EncodeToString(b), nil
}

// hashToken is the form a token is stored and looked up in. A plain SHA-256
// is enough: the token carries 256 random bits, so a slow hash would add no
// protection against guessing.
func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
