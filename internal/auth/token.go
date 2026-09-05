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
// bearer value from somewhere else fails on sight rather than costing a
// database round trip.
const tokenPrefix = "jct_"

// tokenEntropyBytes is how much random material backs a token
const tokenEntropyBytes = 32

// CreateToken issues a new API token for userID and returns its plaintext.
// That plaintext is not retrievable again -- only its hash is kept, the same
// reasoning as a password hash -- so this is the one moment a caller can show
// it to whoever is meant to use it.
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

// VerifyToken verifies plaintext is a valid token for the user it was issued to.
func (a *Auth) VerifyToken(ctx context.Context, plaintext string) (*models.ApiToken, error) {
	if !strings.HasPrefix(plaintext, tokenPrefix) {
		return nil, ErrInvalidToken
	}

	// This runs on every API request, so the lookup and the last_used_at
	// update happen in the same round trip the query does.
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

// RevokeToken revokes the given token for the given user.
func (a *Auth) RevokeToken(ctx context.Context, userID, id int64) error {
	return a.tokens.DeleteAPIToken(ctx, models.DeleteAPITokenParams{ID: id, UserID: userID})
}

// generateToken returns a new bearer value for Authorization header
func generateToken() (string, error) {
	b := make([]byte, tokenEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return tokenPrefix + hex.EncodeToString(b), nil
}

// hashToken is what actually gets stored and looked up.
// there is nothing to guess here.
func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
