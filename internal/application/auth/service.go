// Package auth contains the AuthService application use case responsible for
// orchestrating refresh-token validation, JWT issuance and audit persistence.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/fx"

	domainauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

// Module provides the AuthService.
var Module = fx.Module("application.auth",
	fx.Provide(NewService),
)

// Service orchestrates token refresh.
type Service struct {
	signer    domainauth.Signer
	refreshes domainauth.RefreshTokens
	tokens    repository.TokenRepository
	cfg       *config.Config
	now       func() time.Time
}

// NewService wires the application service.
func NewService(
	signer domainauth.Signer,
	refreshes domainauth.RefreshTokens,
	tokens repository.TokenRepository,
	cfg *config.Config,
) *Service {
	return &Service{signer: signer, refreshes: refreshes, tokens: tokens, cfg: cfg, now: time.Now}
}

// StaticTokenMode reports whether the server is running in static-token
// mode (STATIC_TOKEN_MODE=true), which skips all auth validation.
func (s *Service) StaticTokenMode() bool {
	return s.cfg.StaticTokenMode
}

// Refresh exchanges a refresh token for a fresh JWT pair.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (domainauth.TokenPair, error) {
	subject, err := s.refreshes.Validate(ctx, refreshToken)
	if err != nil {
		return domainauth.TokenPair{}, err
	}

	now := s.now().UTC()
	ttl := s.cfg.TokenTTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	claims := domainauth.Claims{
		Subject:   subject,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
		TokenID:   uuid.NewString(),
	}
	access, err := s.signer.Sign(ctx, claims)
	if err != nil {
		return domainauth.TokenPair{}, fmt.Errorf("refresh: sign: %w", err)
	}
	newRefresh, err := s.refreshes.Issue(ctx, subject)
	if err != nil {
		return domainauth.TokenPair{}, fmt.Errorf("refresh: issue: %w", err)
	}
	if err := s.tokens.Insert(ctx, repository.TokenMetadata{
		TokenID:   claims.TokenID,
		Subject:   subject,
		IssuedAt:  claims.IssuedAt,
		ExpiresAt: claims.ExpiresAt,
	}); err != nil {
		return domainauth.TokenPair{}, fmt.Errorf("refresh: persist: %w", err)
	}
	return domainauth.TokenPair{
		AccessToken:  access,
		RefreshToken: newRefresh,
		ExpiresIn:    int(ttl / time.Second),
	}, nil
}

// VerifyAccessToken validates an incoming Bearer token. The signer parser
// already enforces exp; we keep a thin re-check using s.now so unit tests
// can drive the clock independently. Expired tokens surface as
// domainauth.ErrTokenExpired so the HTTP layer can return a distinct
// "token_expired" body that desktop clients use to trigger a refresh.
func (s *Service) VerifyAccessToken(ctx context.Context, raw string) (domainauth.Claims, error) {
	if raw == "" {
		return domainauth.Claims{}, domainauth.ErrInvalidToken
	}
	c, err := s.signer.Verify(ctx, raw)
	if err != nil {
		return domainauth.Claims{}, err
	}
	if !c.ExpiresAt.IsZero() && c.ExpiresAt.Before(s.now()) {
		return domainauth.Claims{}, domainauth.ErrTokenExpired
	}
	return c, nil
}

// VerifyAPIKey checks the apikey header used by /auth/v1/token. The
// comparison is constant-time so a network attacker cannot mount a timing
// side-channel against the publishable key.
func (s *Service) VerifyAPIKey(value string) error {
	expected := s.cfg.ConnectAuthPublishableKey
	if value == "" || expected == "" {
		return errors.New("auth: invalid apikey")
	}
	if subtle.ConstantTimeCompare([]byte(value), []byte(expected)) != 1 {
		return errors.New("auth: invalid apikey")
	}
	return nil
}

// ErrEmailNotAllowed is returned when the OTP endpoints receive an email
// outside the configured allow-list. Surfaces as 403 to the client.
var ErrEmailNotAllowed = errors.New("auth: email not allowed")

// ErrInvalidOTP is returned when the OTP code does not satisfy any of the
// accepted shapes (6 numeric digits, or an exact match against the
// configured STATIC_OTP). Surfaces as 400 + "otp_invalid" to the client.
var ErrInvalidOTP = errors.New("auth: invalid otp")

var otpNumericPattern = regexp.MustCompile(`^\d{6}$`)

// AllowedEmails returns the configured email allow-list.
func (s *Service) AllowedEmails() []string {
	return s.cfg.SelfHostedUserEmails
}

// IsAllowedEmail reports whether email matches any entry in the configured
// allow-list. The comparison is case-insensitive and whitespace-trimmed,
// mirroring how Supabase normalises addresses.
func (s *Service) IsAllowedEmail(email string) bool {
	if len(s.cfg.SelfHostedUserEmails) == 0 {
		return false
	}
	normalised := strings.ToLower(strings.TrimSpace(email))
	if normalised == "" {
		return false
	}
	for _, allowed := range s.cfg.SelfHostedUserEmails {
		if subtle.ConstantTimeCompare([]byte(normalised), []byte(allowed)) == 1 {
			return true
		}
	}
	return false
}

// VerifyOTP checks that token satisfies the synthetic OTP policy: any
// 6-digit numeric value is accepted, or — when STATIC_OTP is configured —
// the exact static value. The comparison is constant-time. The code is
// otherwise opaque: there is no per-email storage, no expiry, no
// rate-limiting; this is by design for a single-tenant self-hosted
// deployment that gates real access on apikey + email allow-list.
func (s *Service) VerifyOTP(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidOTP
	}
	if otpNumericPattern.MatchString(token) {
		return nil
	}
	if s.cfg.StaticOTP != "" &&
		subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.StaticOTP)) == 1 {
		return nil
	}
	return ErrInvalidOTP
}

// SubjectFromEmail derives a stable opaque subject identifier from an
// email address. We hash with sha256 so the JWT `sub` claim does not leak
// the raw email into logs / downstream services, while remaining stable
// across token refreshes for the same address. The 32-hex-char prefix is
// reformatted into UUID v4 layout because supabase-js validates that the
// session user id parses as a UUID.
func SubjectFromEmail(email string) string {
	normalised := strings.ToLower(strings.TrimSpace(email))
	sum := sha256.Sum256([]byte(normalised))
	hex32 := hex.EncodeToString(sum[:16]) // 32 hex chars = 16 bytes
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex32[0:8], hex32[8:12], hex32[12:16], hex32[16:20], hex32[20:32])
}

// IssueSession mints a fresh access/refresh token pair for the given email.
// It is the building block for the synthetic /auth/v1/verify endpoint that
// emulates Supabase's OTP success response. Email allow-listing is
// enforced; OTP validation is the caller's responsibility (see VerifyOTP).
func (s *Service) IssueSession(ctx context.Context, email string) (domainauth.TokenPair, error) {
	if !s.IsAllowedEmail(email) {
		return domainauth.TokenPair{}, ErrEmailNotAllowed
	}
	subject := SubjectFromEmail(email)

	now := s.now().UTC()
	ttl := s.cfg.TokenTTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	claims := domainauth.Claims{
		Subject:   subject,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
		TokenID:   uuid.NewString(),
	}
	access, err := s.signer.Sign(ctx, claims)
	if err != nil {
		return domainauth.TokenPair{}, fmt.Errorf("issue session: sign: %w", err)
	}
	refresh, err := s.refreshes.Issue(ctx, subject)
	if err != nil {
		return domainauth.TokenPair{}, fmt.Errorf("issue session: issue: %w", err)
	}
	if err := s.tokens.Insert(ctx, repository.TokenMetadata{
		TokenID:   claims.TokenID,
		Subject:   subject,
		IssuedAt:  claims.IssuedAt,
		ExpiresAt: claims.ExpiresAt,
	}); err != nil {
		return domainauth.TokenPair{}, fmt.Errorf("issue session: persist: %w", err)
	}
	return domainauth.TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(ttl / time.Second),
	}, nil
}
