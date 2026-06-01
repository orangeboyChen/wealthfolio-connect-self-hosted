// Package auth contains the authentication domain model: token claims,
// signing/verification ports and refresh-token semantics.
package auth

//go:generate go run go.uber.org/mock/mockgen -source=auth.go -destination=mocks/mock_auth.go -package=mocks

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidToken signals that a JWT failed verification (bad signature,
// malformed, ...). Use ErrTokenExpired to distinguish the "still valid but
// past exp" case (so clients can refresh instead of re-authenticating).
var ErrInvalidToken = errors.New("auth: invalid token")

// ErrTokenExpired signals that a JWT was correctly signed but has expired.
// Wrapped errors satisfy errors.Is(err, ErrInvalidToken) too, so existing
// callers that only branch on invalid-vs-valid keep working.
var ErrTokenExpired = errors.New("auth: token expired")

// ErrInvalidRefreshToken signals that the refresh token presented during a
// /auth/v1/token exchange is unknown or expired.
var ErrInvalidRefreshToken = errors.New("auth: invalid refresh token")

// Claims is the minimal JWT payload exchanged between the client and us.
type Claims struct {
	Subject   string
	IssuedAt  time.Time
	ExpiresAt time.Time
	TokenID   string
}

// TokenPair groups an access token (JWT) with its refresh counterpart.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // seconds
}

// Signer issues signed JWTs and verifies the ones it receives back. Real
// implementations live in infrastructure/auth.
type Signer interface {
	Sign(ctx context.Context, claims Claims) (string, error)
	Verify(ctx context.Context, token string) (Claims, error)
}

// RefreshTokens validates and issues refresh tokens. In static-token mode it
// always returns the same value; in regular mode it should be backed by a
// store with rotation semantics.
type RefreshTokens interface {
	// Validate returns the subject the refresh token belongs to, or
	// ErrInvalidRefreshToken if it is unknown/expired.
	Validate(ctx context.Context, token string) (subject string, err error)
	// Issue creates (or reuses) a refresh token bound to subject.
	Issue(ctx context.Context, subject string) (string, error)
}
