package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// errTokenLookup is what a caller is told when checking its token failed for a
// reason other than the token being wrong. The SDK writes a verifier's error
// into the response, and the underlying one names storage internals.
var errTokenLookup = errors.New("token lookup failed")

// verifier checks a bearer token against the API tokens users issue, the same
// credential the JSON API takes.
//
// auth.TokenMiddleware cannot stand in for this: it reads a token's scope off
// the HTTP method, and every MCP call is a POST whatever the tool does. Here
// the scope travels with the request instead, for the server choice in
// newHandler to read.
func verifier(log *slog.Logger, a *auth.Auth) sdkauth.TokenVerifier {
	return func(ctx context.Context, raw string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		token, err := a.VerifyToken(ctx, raw)

		switch {
		case errors.Is(err, auth.ErrInvalidToken):
			return nil, fmt.Errorf("%w: missing or invalid API token", sdkauth.ErrInvalidToken)
		case err != nil:
			log.ErrorContext(ctx, "verify MCP token", slog.Any("error", err))

			return nil, errTokenLookup
		}

		return &sdkauth.TokenInfo{
			Scopes: []string{string(token.Scope)},
			UserID: strconv.FormatInt(token.UserID, 10),
		}, nil
	}
}

// canWrite reports whether the verified token may use a tool that changes the
// inventory.
func canWrite(ti *sdkauth.TokenInfo) bool {
	return ti != nil && slices.Contains(ti.Scopes, string(dbtype.TokenReadWrite))
}
