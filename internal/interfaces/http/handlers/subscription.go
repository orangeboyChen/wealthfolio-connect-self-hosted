package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

// SubscriptionHandler answers /api/v1/subscription/plans.
type SubscriptionHandler struct{}

// NewSubscriptionHandler wires the handler.
func NewSubscriptionHandler() *SubscriptionHandler { return &SubscriptionHandler{} }

// RegisterPublicAPIRoutes mounts /subscription/plans into the unauthenticated
// /api/v1 sub-group so the Wealthfolio UI can fetch the plan list before the
// user signs in (matches fetch_subscription_plans_public on the client side).
func (h *SubscriptionHandler) RegisterPublicAPIRoutes(r chi.Router) {
	r.Get("/subscription/plans", h.Plans)
}

type plansResponse struct {
	Plans []plan `json:"plans"`
}

type plan struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Tagline      string   `json:"tagline"`
	Description  string   `json:"description"`
	Pricing      pricing  `json:"pricing"`
	Limits       limits   `json:"limits"`
	Features     []string `json:"features"`
	IsAvailable  bool     `json:"isAvailable"`
	IsComingSoon bool     `json:"isComingSoon"`
}

type pricing struct {
	Monthly        float64 `json:"monthly"`
	Yearly         float64 `json:"yearly"`
	YearlyPerMonth float64 `json:"yearlyPerMonth"`
}

type limits struct {
	HouseholdSize          int    `json:"householdSize"`
	InstitutionConnections string `json:"institutionConnections"`
	Devices                int    `json:"devices"`
}

// Plans returns the static plan list expected by Wealthfolio.
func (h *SubscriptionHandler) Plans(w http.ResponseWriter, _ *http.Request) {
	middleware.WriteJSON(w, http.StatusOK, plansResponse{
		Plans: []plan{{
			ID:          "pro",
			Name:        "Pro",
			Tagline:     "Full broker sync",
			Description: "All features",
			Pricing:     pricing{},
			Limits: limits{
				HouseholdSize:          10,
				InstitutionConnections: "unlimited",
				Devices:                10,
			},
			Features:     []string{"broker_sync", "device_sync"},
			IsAvailable:  true,
			IsComingSoon: false,
		}},
	})
}
