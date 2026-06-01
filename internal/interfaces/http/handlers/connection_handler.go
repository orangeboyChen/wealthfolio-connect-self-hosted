package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	appbrokerage "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/brokerage"
	appsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

// ConnectionHandler answers GET /api/v1/sync/brokerage/connections.
type ConnectionHandler struct {
	svc    *appbrokerage.ConnectionService
	syncer *appsync.Service
}

// NewConnectionHandler wires the handler.
func NewConnectionHandler(svc *appbrokerage.ConnectionService, syncer *appsync.Service) *ConnectionHandler {
	return &ConnectionHandler{svc: svc, syncer: syncer}
}

// RegisterAPIRoutes mounts /sync/brokerage/connections.
func (h *ConnectionHandler) RegisterAPIRoutes(r chi.Router) {
	r.Get("/sync/brokerage/connections", h.List)
}

type brokerageDTO struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	DisplayName        string `json:"display_name"`
	Slug               string `json:"slug"`
	AwsS3LogoURL       string `json:"aws_s3_logo_url,omitempty"`
	AwsS3SquareLogoURL string `json:"aws_s3_square_logo_url,omitempty"`
}

type connectionDTO struct {
	ID              string       `json:"id"`
	AuthorizationID string       `json:"authorization_id"`
	BrokerageName   string       `json:"brokerage_name"`
	BrokerageSlug   string       `json:"brokerage_slug"`
	Brokerage       brokerageDTO `json:"brokerage"`
	Disabled        bool         `json:"disabled"`
	UpdatedAt       time.Time    `json:"updated_at"`
	Name            string       `json:"name"`
	Status          string       `json:"status"`
}

type connectionsResponse struct {
	Connections []connectionDTO `json:"connections"`
}

func toConnectionDTO(c brokerage.Connection) connectionDTO {
	return connectionDTO{
		ID:              c.ID,
		AuthorizationID: c.AuthorizationID,
		BrokerageName:   c.BrokerageName,
		BrokerageSlug:   c.BrokerageSlug,
		Brokerage: brokerageDTO{
			ID:                 "brokerage-" + c.BrokerageSlug,
			Name:               c.BrokerageName,
			DisplayName:        c.DisplayName,
			Slug:               c.BrokerageSlug,
			AwsS3LogoURL:       c.LogoURL,
			AwsS3SquareLogoURL: c.SquareLogoURL,
		},
		Disabled:  c.Disabled,
		UpdatedAt: c.UpdatedAt,
		Name:      c.Name,
		Status:    string(c.Status),
	}
}

// List returns every brokerage connection and triggers a background sync.
func (h *ConnectionHandler) List(w http.ResponseWriter, r *http.Request) {
	// Fire-and-forget: trigger a sync in the background.
	if h.syncer != nil {
		go h.syncer.RunOnce(context.Background()) //nolint:errcheck,gosec,contextcheck // fire-and-forget background sync intentionally detached from request ctx
	}

	conns, err := h.svc.List(r.Context())
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "internal_error", "internal_error", err.Error())
		return
	}
	out := connectionsResponse{Connections: make([]connectionDTO, 0, len(conns))}
	for _, c := range conns {
		out.Connections = append(out.Connections, toConnectionDTO(c))
	}
	middleware.WriteJSON(w, http.StatusOK, out)
}
