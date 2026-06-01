package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	appauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/auth"
	domainauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

// AuthHandler answers /auth/v1/token (apikey-gated).
type AuthHandler struct {
	svc *appauth.Service
}

// NewAuthHandler wires the dependencies.
func NewAuthHandler(svc *appauth.Service) *AuthHandler {
	return &AuthHandler{svc: svc}
}

// RegisterAuthRoutes mounts the /auth/v1 endpoints.
func (h *AuthHandler) RegisterAuthRoutes(r chi.Router) {
	r.Post("/token", h.Token)
	// Synthetic Supabase-compatible OTP endpoints. The supabase-js client
	// hits these from the Wealthfolio frontend during magic-link login;
	// since this is a single-tenant self-hosted deployment we skip OTP
	// validation entirely and gate access on apikey + email allow-list.
	r.Post("/otp", h.OTP)
	r.Post("/verify", h.Verify)
	// supabase-js calls /logout on signOut. We have no session-side state
	// to invalidate (refresh tokens expire naturally) so it is a no-op.
	r.Post("/logout", h.Logout)
	// supabase-js calls GET /auth/v1/user from setSession/getUser to
	// retrieve the current user object. We verify the Bearer token and
	// return a Supabase-shaped user response.
	r.Get("/user", h.GetUser)
}

type tokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// Token exchanges a refresh token for a fresh JWT pair.
func (h *AuthHandler) Token(w http.ResponseWriter, r *http.Request) {
	if grant := r.URL.Query().Get("grant_type"); grant != "" && grant != "refresh_token" {
		middleware.WriteAuthError(w, http.StatusBadRequest,
			"unsupported_grant_type", "Only refresh_token is supported")
		return
	}
	var body tokenRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RefreshToken == "" {
		middleware.WriteAuthError(w, http.StatusBadRequest,
			"invalid_request", "refresh_token is required")
		return
	}
	pair, err := h.svc.Refresh(r.Context(), body.RefreshToken)
	if err != nil {
		if errors.Is(err, domainauth.ErrInvalidRefreshToken) {
			middleware.WriteAuthError(w, http.StatusUnauthorized,
				"invalid_grant", "Invalid refresh token")
			return
		}
		middleware.WriteAuthError(w, http.StatusInternalServerError,
			"server_error", err.Error())
		return
	}
	middleware.WriteJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
	})
}

// otpRequest mirrors the Supabase /auth/v1/otp body. Only `email` is
// inspected; every other field (create_user, gotrue_meta_security,
// code_challenge, ...) is accepted and ignored so supabase-js works
// regardless of flow type.
type otpRequest struct {
	Email string `json:"email"`
}

// OTP responds 200 with an empty body, mimicking Supabase's "magic link
// sent" response. We do not actually send any email; the user is expected
// to type any 6-digit value into the verify form because /verify accepts
// anything for an allow-listed email.
func (h *AuthHandler) OTP(w http.ResponseWriter, r *http.Request) {
	var body otpRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		middleware.WriteAuthError(w, http.StatusBadRequest,
			"invalid_request", "email is required")
		return
	}
	if !h.svc.IsAllowedEmail(body.Email) {
		// Use 400 + the Supabase-style "validation_failed" code so
		// supabase-js surfaces a user-visible "email not allowed"
		// message instead of forcing a sign-out flow.
		middleware.WriteAuthError(w, http.StatusForbidden,
			"validation_failed", "Email not allowed for this self-hosted instance")
		return
	}
	// Supabase returns {"message_id": null} on success. supabase-js does
	// not look at the body, but we keep the shape close to the real one.
	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"message_id": nil,
	})
}

// verifyRequest mirrors the Supabase /auth/v1/verify body. We deliberately
// ignore Token: any string is accepted as long as the email matches the
// allow-list and the apikey middleware has let the request through.
type verifyRequest struct {
	Email string `json:"email"`
	Token string `json:"token"`
	Type  string `json:"type"`
}

// supabaseUser is the minimum subset of the GoTrue User object that
// supabase-js needs to produce a working Session. Field names mirror the
// upstream wire format.
type supabaseUser struct {
	ID               string         `json:"id"`
	Aud              string         `json:"aud"`
	Role             string         `json:"role"`
	Email            string         `json:"email"`
	EmailConfirmedAt string         `json:"email_confirmed_at"`
	Phone            string         `json:"phone"`
	ConfirmedAt      string         `json:"confirmed_at"`
	LastSignInAt     string         `json:"last_sign_in_at"`
	AppMetadata      map[string]any `json:"app_metadata"`
	UserMetadata     map[string]any `json:"user_metadata"`
	CreatedAt        string         `json:"created_at"`
	UpdatedAt        string         `json:"updated_at"`
}

// verifyResponse matches the AuthResponse shape supabase-js expects when
// type == "email" \u2014 i.e. the OTP success path returns a fully populated
// Session, not a code that needs to be exchanged.
type verifyResponse struct {
	AccessToken  string       `json:"access_token"`
	TokenType    string       `json:"token_type"`
	ExpiresIn    int          `json:"expires_in"`
	ExpiresAt    int64        `json:"expires_at"`
	RefreshToken string       `json:"refresh_token"`
	User         supabaseUser `json:"user"`
}

// Verify is the synthetic counterpart of Supabase's /auth/v1/verify
// endpoint. It validates the OTP shape (any 6-digit numeric value, or
// STATIC_OTP when configured) and the email allow-list, then mints a real
// signed JWT plus refresh token. There is no per-email OTP storage; this
// is intentional for a single-tenant self-hosted deployment that gates
// real access on apikey + email allow-list.
func (h *AuthHandler) Verify(w http.ResponseWriter, r *http.Request) {
	var body verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		middleware.WriteAuthError(w, http.StatusBadRequest,
			"invalid_request", "email is required")
		return
	}
	if err := h.svc.VerifyOTP(body.Token); err != nil {
		middleware.WriteAuthError(w, http.StatusBadRequest,
			"otp_invalid", "Invalid or expired OTP code")
		return
	}

	pair, err := h.svc.IssueSession(r.Context(), body.Email)
	if err != nil {
		if errors.Is(err, appauth.ErrEmailNotAllowed) {
			middleware.WriteAuthError(w, http.StatusForbidden,
				"validation_failed", "Email not allowed for this self-hosted instance")
			return
		}
		middleware.WriteAuthError(w, http.StatusInternalServerError,
			"server_error", err.Error())
		return
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	email := body.Email
	userID := appauth.SubjectFromEmail(email)

	middleware.WriteJSON(w, http.StatusOK, verifyResponse{
		AccessToken:  pair.AccessToken,
		TokenType:    "bearer",
		ExpiresIn:    pair.ExpiresIn,
		ExpiresAt:    now.Add(time.Duration(pair.ExpiresIn) * time.Second).Unix(),
		RefreshToken: pair.RefreshToken,
		User: supabaseUser{
			ID:               userID,
			Aud:              "authenticated",
			Role:             "authenticated",
			Email:            email,
			EmailConfirmedAt: nowStr,
			ConfirmedAt:      nowStr,
			LastSignInAt:     nowStr,
			AppMetadata: map[string]any{
				"provider":  "email",
				"providers": []string{"email"},
			},
			UserMetadata: map[string]any{},
			CreatedAt:    nowStr,
			UpdatedAt:    nowStr,
		},
	})
}

// Logout is a no-op stub. supabase-js calls POST /auth/v1/logout during
// signOut; we have no server-side session state beyond the refresh token
// store (which expires naturally) so we simply acknowledge the request.
func (h *AuthHandler) Logout(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// GetUser implements GET /auth/v1/user. supabase-js calls this endpoint
// during setSession() and getUser() to hydrate the local User object.
// We verify the Bearer token from the Authorization header and return a
// Supabase-shaped user response.
func (h *AuthHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	raw := r.Header.Get("Authorization")
	if !strings.HasPrefix(raw, "Bearer ") {
		middleware.WriteAuthError(w, http.StatusUnauthorized,
			"unauthorized", "Missing bearer token")
		return
	}
	token := strings.TrimPrefix(raw, "Bearer ")
	claims, err := h.svc.VerifyAccessToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, domainauth.ErrTokenExpired) {
			middleware.WriteAuthError(w, http.StatusUnauthorized,
				"token_expired", "Access token has expired")
			return
		}
		middleware.WriteAuthError(w, http.StatusUnauthorized,
			"unauthorized", "Invalid token")
		return
	}

	// Derive email from subject (best-effort; subject is a UUID hash of
	// the email so we cannot reverse it). Use the configured allow-list
	// email if available; otherwise use a placeholder.
	email := claims.Subject + "@self-hosted.local"
	if emails := h.svc.AllowedEmails(); len(emails) > 0 {
		email = emails[0]
	}

	now := time.Now().UTC().Format(time.RFC3339)
	middleware.WriteJSON(w, http.StatusOK, supabaseUser{
		ID:               claims.Subject,
		Aud:              "authenticated",
		Role:             "authenticated",
		Email:            email,
		EmailConfirmedAt: now,
		Phone:            "",
		ConfirmedAt:      now,
		LastSignInAt:     now,
		AppMetadata: map[string]any{
			"provider":  "email",
			"providers": []string{"email"},
		},
		UserMetadata: map[string]any{},
		CreatedAt:    now,
		UpdatedAt:    now,
	})
}
