// Package middleware contains the HTTP middleware stack (request id, recovery,
// CORS, structured logging, auth) shared by every route group.
package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	appauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/auth"
	domainauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth"
)

type contextKey string

const (
	requestIDKey contextKey = "request-id"
	claimsKey    contextKey = "claims"
)

// RequestID injects a per-request UUID into the context and the
// "X-Request-Id" response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if id == "" {
				id = uuid.NewString()
			}
			w.Header().Set("X-Request-Id", id)
			ctx := context.WithValue(r.Context(), requestIDKey, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		},
	)
}

// RequestIDFrom extracts the request id from the context (empty string if
// unset).
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// Logger logs every request as a structured JSON line. Health-probe paths
// (/healthz, /readyz) are skipped because they are hit every few seconds by
// kubelet and would otherwise drown out real traffic.
func Logger(log zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
					next.ServeHTTP(w, r)
					return
				}
				start := time.Now()
				ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
				next.ServeHTTP(ww, r)
				log.Info().
					Str("request_id", RequestIDFrom(r.Context())).
					Str("method", r.Method).
					Str("path", r.URL.Path).
					Int("status", ww.Status()).
					Int64("duration_ms", time.Since(start).Milliseconds()).
					Msg("http request")
			},
		)
	}
}

// Recover converts a panic into a structured 500 response and logs the stack
// trace. It is the first middleware in the chain.
func Recover(log zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				defer func() { //nolint:contextcheck // recovery path runs synchronously in request goroutine; r.Context() is already used for logging
					if rec := recover(); rec != nil {
						log.Error().
							Str("request_id", RequestIDFrom(r.Context())).
							Interface("panic", rec).
							Bytes("stack", debug.Stack()).
							Msg("panic recovered")
						WriteError(
							w, http.StatusInternalServerError,
							"internal_error", "INTERNAL_ERROR", "Internal server error",
						)
					}
				}()
				next.ServeHTTP(w, r)
			},
		)
	}
}

// CORS configures the global CORS handler from the supplied origins. When
// origins is empty no CORS headers are emitted (browsers will reject
// cross-origin requests). Operators must explicitly set CORS_ORIGINS to
// opt in.
func CORS(origins []string) func(http.Handler) http.Handler {
	if len(origins) == 0 {
		// No-op middleware: the desktop client talks to the server via
		// localhost / a CLI HTTP client, so cross-origin browser access is
		// not required by default.
		return func(next http.Handler) http.Handler { return next }
	}
	opts := cors.Options{
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: false,
		MaxAge:           300,
	}
	if len(origins) == 1 && origins[0] == "*" {
		opts.AllowOriginFunc = func(_ *http.Request, _ string) bool { return true }
	} else {
		opts.AllowedOrigins = origins
	}
	return cors.Handler(opts)
}

// APIKey validates the apikey header against the AuthService. Used for
// /auth/v1/* routes. When StaticTokenMode is enabled, validation is skipped.
func APIKey(svc *appauth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodOptions {
					next.ServeHTTP(w, r)
					return
				}
				if svc.StaticTokenMode() {
					next.ServeHTTP(w, r)
					return
				}
				if err := svc.VerifyAPIKey(r.Header.Get("apikey")); err != nil {
					WriteAuthError(
						w, http.StatusUnauthorized,
						"unauthorized", "Invalid apikey",
					)
					return
				}
				next.ServeHTTP(w, r)
			},
		)
	}
}

// Bearer validates the Authorization: Bearer <jwt> header against the
// AuthService. Used for /api/v1/* routes. Expired tokens surface a distinct
// "token_expired" code so desktop clients know to refresh instead of
// prompting the user to log in again. When StaticTokenMode is enabled,
// validation is skipped entirely.
func Bearer(svc *appauth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodOptions {
					next.ServeHTTP(w, r)
					return
				}
				if svc.StaticTokenMode() {
					next.ServeHTTP(w, r)
					return
				}
				raw := r.Header.Get("Authorization")
				if !strings.HasPrefix(raw, "Bearer ") {
					WriteError(
						w, http.StatusUnauthorized,
						"unauthorized", "UNAUTHORIZED", "Missing bearer token",
					)
					return
				}
				claims, err := svc.VerifyAccessToken(r.Context(), strings.TrimPrefix(raw, "Bearer "))
				if err != nil {
					switch {
					case errors.Is(err, domainauth.ErrTokenExpired):
						WriteError(
							w, http.StatusUnauthorized,
							"token_expired", "TOKEN_EXPIRED", "Access token has expired",
						)
					case errors.Is(err, domainauth.ErrInvalidToken):
						WriteError(
							w, http.StatusUnauthorized,
							"unauthorized", "UNAUTHORIZED", "Invalid token",
						)
					default:
						WriteError(
							w, http.StatusUnauthorized,
							"unauthorized", "UNAUTHORIZED", "Authentication failed",
						)
					}
					return
				}
				ctx := context.WithValue(r.Context(), claimsKey, claims)
				next.ServeHTTP(w, r.WithContext(ctx))
			},
		)
	}
}

// ClaimsFrom returns the JWT claims attached by Bearer middleware.
func ClaimsFrom(ctx context.Context) (domainauth.Claims, bool) {
	c, ok := ctx.Value(claimsKey).(domainauth.Claims)
	return c, ok
}

// ─── Error responses ─────────────────────────────────────────────────────

// APIError is the canonical error envelope.
type APIError struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AuthErrorBody matches the OAuth2-style error returned at /auth/v1/token.
type AuthErrorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// WriteJSON serializes v as JSON and writes it with the supplied status. The
// payload is fully marshaled before any header is written so an encode
// failure cannot result in a half-flushed response with a misleading 2xx
// status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal_error","code":"INTERNAL_ERROR","message":"failed to encode response"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

// WriteError writes a standard ApiError JSON envelope.
func WriteError(w http.ResponseWriter, status int, errKey, code, message string) {
	WriteJSON(w, status, APIError{Error: errKey, Code: code, Message: message})
}

// WriteAuthError writes the OAuth2-style error body used by /auth/v1/token.
func WriteAuthError(w http.ResponseWriter, status int, err, desc string) {
	WriteJSON(w, status, AuthErrorBody{Error: err, ErrorDescription: desc})
}
