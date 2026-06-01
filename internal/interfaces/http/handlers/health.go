// Package handlers contains the HTTP delivery for every API endpoint.
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/database"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

// HealthHandler answers /healthz and /readyz.
type HealthHandler struct {
	pool  database.Pinger
	ready *database.Readiness
}

// NewHealthHandler wires the dependencies.
func NewHealthHandler(pool database.Pinger, ready *database.Readiness) *HealthHandler {
	return &HealthHandler{pool: pool, ready: ready}
}

// Live answers /healthz: 200 if the database is reachable. The DB ping uses
// a fresh context derived from context.Background() so an aborted client
// connection (kubelet timing out, etc.) does not propagate cancellation
// into the ping and produce a spurious 503 that kills the pod.
func (h *HealthHandler) Live(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.pool.Ping(ctx); err != nil { //nolint:contextcheck // intentional: see godoc above
		middleware.WriteError(w, http.StatusServiceUnavailable,
			"unhealthy", "DB_UNAVAILABLE", err.Error())
		return
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Ready answers /readyz: 200 only after migrations finished. Note: this
// intentionally does *not* re-ping the DB — that is /healthz's job. Keeping
// the two probes orthogonal avoids the situation where a transient DB
// hiccup flaps both probes simultaneously and makes the pod unschedulable.
func (h *HealthHandler) Ready(w http.ResponseWriter, _ *http.Request) {
	if !h.ready.Ready() {
		middleware.WriteError(w, http.StatusServiceUnavailable,
			"not_ready", "NOT_READY", "Migrations have not completed yet")
		return
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
