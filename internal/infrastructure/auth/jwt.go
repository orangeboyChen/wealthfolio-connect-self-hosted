// Package auth contains JWT signing and refresh-token implementations of the
// ports declared in domain/auth.
package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

// refreshTTL bounds how long an issued refresh token remains valid in
// memory. Combined with maxRefreshTokens it gives a defense-in-depth cap on
// memory growth even when a malicious caller floods /auth/v1/token.
const (
	refreshTTL       = 30 * 24 * time.Hour // 30 days
	maxRefreshTokens = 10000
)

type refreshEntry struct {
	subject   string
	expiresAt time.Time
}

// RefreshStore is an in-memory refresh-token registry with TTL eviction. It
// is the simplest thing that satisfies domainauth.RefreshTokens for
// self-hosted deployments without external session stores. In static-token
// mode every refresh token is accepted and resolves to the same subject.
type RefreshStore struct {
	staticMode bool
	subject    string
	ttl        time.Duration
	cap        int
	now        func() time.Time

	mu     sync.RWMutex
	tokens map[string]refreshEntry // token -> entry
}

// NewRefreshTokens constructs the refresh-token store.
func NewRefreshTokens(cfg *config.Config) *RefreshStore {
	return &RefreshStore{
		staticMode: cfg.StaticTokenMode,
		subject:    "self-hosted-user",
		ttl:        refreshTTL,
		cap:        maxRefreshTokens,
		now:        time.Now,
		tokens:     map[string]refreshEntry{},
	}
}

// Validate returns the subject the token belongs to or ErrInvalidRefreshToken.
func (s *RefreshStore) Validate(_ context.Context, token string) (string, error) {
	if token == "" {
		return "", domainauth.ErrInvalidRefreshToken
	}
	if s.staticMode {
		return s.subject, nil
	}
	s.mu.RLock()
	entry, ok := s.tokens[token]
	s.mu.RUnlock()
	if !ok {
		return "", domainauth.ErrInvalidRefreshToken
	}
	if !entry.expiresAt.IsZero() && s.now().After(entry.expiresAt) {
		// Lazy eviction of expired entry.
		s.mu.Lock()
		delete(s.tokens, token)
		s.mu.Unlock()
		return "", domainauth.ErrInvalidRefreshToken
	}
	return entry.subject, nil
}

// Issue persists a refresh token bound to subject and returns its value. In
// static-token mode it always returns the same fixed value.
func (s *RefreshStore) Issue(_ context.Context, subject string) (string, error) {
	if s.staticMode {
		return "static-refresh-token", nil
	}
	now := s.now()
	tok := fmt.Sprintf("rt-%s-%d", subject, now.UnixNano())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked(now)
	if s.cap > 0 && len(s.tokens) >= s.cap {
		// Hard cap reached even after eviction: reject to avoid OOM.
		return "", fmt.Errorf("auth: refresh token store at capacity")
	}
	s.tokens[tok] = refreshEntry{subject: subject, expiresAt: now.Add(s.ttl)}
	return tok, nil
}

// evictExpiredLocked removes every expired entry. Caller must hold s.mu.
func (s *RefreshStore) evictExpiredLocked(now time.Time) {
	for k, v := range s.tokens {
		if !v.expiresAt.IsZero() && now.After(v.expiresAt) {
			delete(s.tokens, k)
		}
	}
}
