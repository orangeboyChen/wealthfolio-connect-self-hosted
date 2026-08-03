// Package auth contains JWT signing and refresh-token implementations of the
// ports declared in domain/auth.
package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/fx"

	domainauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

// Module exposes the JWT signer and refresh-token store as domain ports.
var Module = fx.Module("auth",
	fx.Provide(
		fx.Annotate(NewJWTSigner, fx.As(new(domainauth.Signer))),
		fx.Annotate(NewRefreshTokens, fx.As(new(domainauth.RefreshTokens))),
	),
)

// ─── JWT signer ──────────────────────────────────────────────────────────

// JWTSigner produces and verifies HS256 JWTs.
type JWTSigner struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewJWTSigner builds a signer from the application config.
func NewJWTSigner(cfg *config.Config) *JWTSigner {
	ttl := cfg.TokenTTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &JWTSigner{secret: []byte(cfg.JWTSecret), ttl: ttl, now: time.Now}
}

// Sign returns a compact JWT bearing the supplied claims. Missing IssuedAt /
// ExpiresAt values are filled in based on the signer's clock.
func (s *JWTSigner) Sign(_ context.Context, claims domainauth.Claims) (string, error) {
	now := s.now().UTC()
	if claims.IssuedAt.IsZero() {
		claims.IssuedAt = now
	}
	if claims.ExpiresAt.IsZero() {
		claims.ExpiresAt = now.Add(s.ttl)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": claims.Subject,
		"iat": claims.IssuedAt.Unix(),
		"exp": claims.ExpiresAt.Unix(),
		"jti": claims.TokenID,
	})
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("auth: signing JWT: %w", err)
	}
	return signed, nil
}

// Verify parses and validates a token, returning its claims. Expired tokens
// surface as domainauth.ErrTokenExpired so callers can distinguish a refresh
// flow from a fresh re-authentication.
func (s *JWTSigner) Verify(_ context.Context, raw string) (domainauth.Claims, error) {
	keyFunc := func(_ *jwt.Token) (any, error) { return s.secret, nil }
	parsed, err := jwt.Parse(raw, keyFunc, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return domainauth.Claims{}, domainauth.ErrTokenExpired
		}
		return domainauth.Claims{}, domainauth.ErrInvalidToken
	}
	if !parsed.Valid {
		return domainauth.Claims{}, domainauth.ErrInvalidToken
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return domainauth.Claims{}, domainauth.ErrInvalidToken
	}
	c := domainauth.Claims{}
	if v, ok := mc["sub"].(string); ok {
		c.Subject = v
	}
	if v, ok := mc["jti"].(string); ok {
		c.TokenID = v
	}
	if v, ok := mc["iat"].(float64); ok {
		c.IssuedAt = time.Unix(int64(v), 0).UTC()
	}
	if v, ok := mc["exp"].(float64); ok {
		c.ExpiresAt = time.Unix(int64(v), 0).UTC()
	}
	return c, nil
}

// ─── Refresh tokens ──────────────────────────────────────────────────────

// refreshTTL bounds how long a signed refresh token remains valid.
const refreshTTL = 30 * 24 * time.Hour

// RefreshStore issues self-contained HS256 refresh tokens. Using the same
// configured JWT secret makes sessions survive process and container
// restarts without storing bearer credentials in plaintext. In static-token
// mode every non-empty refresh token resolves to the same subject.
type RefreshStore struct {
	staticMode bool
	subject    string
	ttl        time.Duration
	secret     []byte
	now        func() time.Time
}

// NewRefreshTokens constructs the refresh-token store.
func NewRefreshTokens(cfg *config.Config) *RefreshStore {
	refreshSecret := sha256.Sum256(append(
		[]byte("wealthfolio-connect-refresh-v1\x00"),
		[]byte(cfg.JWTSecret)...,
	))
	return &RefreshStore{
		staticMode: cfg.StaticTokenMode,
		subject:    "self-hosted-user",
		ttl:        refreshTTL,
		secret:     refreshSecret[:],
		now:        time.Now,
	}
}

// Validate verifies the signed refresh token and returns its subject.
func (s *RefreshStore) Validate(_ context.Context, token string) (string, error) {
	if token == "" {
		return "", domainauth.ErrInvalidRefreshToken
	}
	if s.staticMode {
		return s.subject, nil
	}
	parsed, err := jwt.Parse(
		token,
		func(_ *jwt.Token) (any, error) { return s.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || !parsed.Valid {
		return "", domainauth.ErrInvalidRefreshToken
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || claims["token_use"] != "refresh" {
		return "", domainauth.ErrInvalidRefreshToken
	}
	subject, ok := claims["sub"].(string)
	if !ok || strings.TrimSpace(subject) == "" {
		return "", domainauth.ErrInvalidRefreshToken
	}
	return subject, nil
}

// Issue signs a refresh token bound to subject. In static-token mode it
// always returns the same fixed value.
func (s *RefreshStore) Issue(_ context.Context, subject string) (string, error) {
	if s.staticMode {
		return "static-refresh-token", nil
	}
	if strings.TrimSpace(subject) == "" {
		return "", errors.New("auth: refresh token subject is required")
	}
	now := s.now().UTC()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       subject,
		"iat":       now.Unix(),
		"exp":       now.Add(s.ttl).Unix(),
		"token_use": "refresh",
	})
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("auth: signing refresh token: %w", err)
	}
	return signed, nil
}
