package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/database"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/handlers"
)

func TestHandlers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Handlers Suite")
}

type stubPinger struct{ err error }

func (s stubPinger) Ping(_ context.Context) error { return s.err }

var _ = Describe("HealthHandler", func() {
	It("returns 200 when DB is reachable", func() {
		ready := database.NewReadiness()
		ready.MarkReady()
		h := handlers.NewHealthHandler(stubPinger{}, ready)
		rec := httptest.NewRecorder()
		h.Live(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("ok"))
	})

	It("returns 503 when DB ping fails", func() {
		h := handlers.NewHealthHandler(stubPinger{err: errors.New("down")}, database.NewReadiness())
		rec := httptest.NewRecorder()
		h.Live(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(rec.Body.String()).To(ContainSubstring("DB_UNAVAILABLE"))
	})

	It("returns 503 from /readyz before migrations finish", func() {
		h := handlers.NewHealthHandler(stubPinger{}, database.NewReadiness())
		rec := httptest.NewRecorder()
		h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
	})

	It("returns 200 from /readyz once readiness is set", func() {
		ready := database.NewReadiness()
		ready.MarkReady()
		h := handlers.NewHealthHandler(stubPinger{}, ready)
		rec := httptest.NewRecorder()
		h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
	})
})
