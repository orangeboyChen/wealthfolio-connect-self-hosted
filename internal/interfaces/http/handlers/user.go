package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

// UserHandler answers /api/v1/user/me.
type UserHandler struct {
	cfg *config.Config
}

// NewUserHandler wires the handler.
func NewUserHandler(cfg *config.Config) *UserHandler { return &UserHandler{cfg: cfg} }

// RegisterAPIRoutes mounts /user/me.
func (h *UserHandler) RegisterAPIRoutes(r chi.Router) {
	r.Get("/user/me", h.Me)
}

type team struct {
	ID                            string    `json:"id"`
	Name                          string    `json:"name"`
	LogoURL                       *string   `json:"logoUrl"`
	Plan                          string    `json:"plan"`
	SubscriptionStatus            string    `json:"subscriptionStatus"`
	SubscriptionCurrentPeriodEnd  string    `json:"subscriptionCurrentPeriodEnd"`
	SubscriptionCancelAtPeriodEnd bool      `json:"subscriptionCancelAtPeriodEnd"`
	CanceledAt                    *string   `json:"canceledAt"`
	CountryCode                   string    `json:"countryCode"`
	CreatedAt                     time.Time `json:"createdAt"`
}

type userMeResponse struct {
	ID                 string  `json:"id"`
	Email              string  `json:"email"`
	FullName           string  `json:"fullName"`
	AvatarURL          *string `json:"avatarUrl"`
	Locale             string  `json:"locale"`
	WeekStartsOnMonday bool    `json:"weekStartsOnMonday"`
	Timezone           string  `json:"timezone"`
	TimezoneAutoSync   bool    `json:"timezoneAutoSync"`
	TimeFormat         int     `json:"timeFormat"`
	DateFormat         string  `json:"dateFormat"`
	TeamID             string  `json:"teamId"`
	TeamRole           string  `json:"teamRole"`
	Team               team    `json:"team"`
}

// Me returns the synthetic single-user response required by Wealthfolio's
// has_broker_sync() check.
func (h *UserHandler) Me(w http.ResponseWriter, r *http.Request) {
	subject := "self-hosted-user"
	if c, ok := middleware.ClaimsFrom(r.Context()); ok && c.Subject != "" {
		subject = c.Subject
	}
	email := subject + "@self-hosted.local"
	if h.cfg != nil && len(h.cfg.SelfHostedUserEmails) > 0 {
		email = h.cfg.SelfHostedUserEmails[0]
	}
	middleware.WriteJSON(w, http.StatusOK, userMeResponse{
		ID:                 subject,
		Email:              email,
		FullName:           "Self-Hosted User",
		Locale:             "en",
		WeekStartsOnMonday: false,
		Timezone:           "UTC",
		TimezoneAutoSync:   true,
		TimeFormat:         24,
		DateFormat:         "YYYY-MM-DD",
		TeamID:             "team-self-hosted",
		TeamRole:           "owner",
		Team: team{
			ID:                            "team-self-hosted",
			Name:                          "Self-Hosted",
			Plan:                          "pro",
			SubscriptionStatus:            "active",
			SubscriptionCurrentPeriodEnd:  "2099-12-31T23:59:59Z",
			SubscriptionCancelAtPeriodEnd: false,
			CountryCode:                   "",
			CreatedAt:                     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	})
}
