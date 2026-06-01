package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"go.uber.org/fx/fxtest"
	"go.uber.org/mock/gomock"

	appauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/auth"
	domainauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth"
	authmocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth/mocks"
	repomocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository/mocks"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/database"
	httpmod "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/handlers"
)

func TestHTTP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HTTP Server Suite")
}

type stubPinger struct{}

func (stubPinger) Ping(_ context.Context) error { return nil }

type fakeAuthRoute struct{ called *bool }

func (f fakeAuthRoute) RegisterAuthRoutes(r chi.Router) {
	r.Get("/probe", func(w http.ResponseWriter, _ *http.Request) {
		*f.called = true
		w.WriteHeader(http.StatusOK)
	})
}

type fakeAPIRoute struct{ called *bool }

func (f fakeAPIRoute) RegisterAPIRoutes(r chi.Router) {
	r.Get("/probe", func(w http.ResponseWriter, _ *http.Request) {
		*f.called = true
		w.WriteHeader(http.StatusOK)
	})
}

func makeRouter(authReg []httpmod.AuthRouteRegistrar, apiReg []httpmod.APIRouteRegistrar) http.Handler {
	ctrl := gomock.NewController(GinkgoT())
	signer := authmocks.NewMockSigner(ctrl)
	refr := authmocks.NewMockRefreshTokens(ctrl)
	repo := repomocks.NewMockTokenRepository(ctrl)
	cfg := &config.Config{ConnectAuthPublishableKey: "pk", TokenTTL: time.Hour}
	svc := appauth.NewService(signer, refr, repo, cfg)
	ready := database.NewReadiness()
	ready.MarkReady()
	return httpmod.NewRouter(httpmod.RouterParams{
		Config:         cfg,
		Logger:         zerolog.Nop(),
		Auth:           svc,
		Health:         handlers.NewHealthHandler(stubPinger{}, ready),
		AuthRegistrars: authReg,
		APIRegistrars:  apiReg,
	})
}

var _ = Describe("Router", func() {
	It("serves /healthz without auth", func() {
		r := makeRouter(nil, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("rejects /api/v1 without bearer", func() {
		called := false
		r := makeRouter(nil, []httpmod.APIRouteRegistrar{fakeAPIRoute{called: &called}})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil))
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(called).To(BeFalse())
	})

	It("rejects /auth/v1 without apikey", func() {
		called := false
		r := makeRouter([]httpmod.AuthRouteRegistrar{fakeAuthRoute{called: &called}}, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/v1/probe", nil))
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(called).To(BeFalse())
	})

	It("registers auth route registrars", func() {
		called := false
		r := makeRouter([]httpmod.AuthRouteRegistrar{fakeAuthRoute{called: &called}}, nil)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/v1/probe", nil)
		req.Header.Set("apikey", "pk")
		r.ServeHTTP(rec, req)
		Expect(called).To(BeTrue())
		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("registers api route registrars when authenticated", func() {
		ctrl := gomock.NewController(GinkgoT())
		signer := authmocks.NewMockSigner(ctrl)
		refr := authmocks.NewMockRefreshTokens(ctrl)
		repo := repomocks.NewMockTokenRepository(ctrl)
		cfg := &config.Config{ConnectAuthPublishableKey: "pk", TokenTTL: time.Hour}
		svc := appauth.NewService(signer, refr, repo, cfg)
		ready := database.NewReadiness()
		ready.MarkReady()
		signer.EXPECT().Verify(gomock.Any(), "ok").Return(domainauth.Claims{Subject: "u", ExpiresAt: time.Now().Add(time.Hour)}, nil)

		called := false
		r := httpmod.NewRouter(httpmod.RouterParams{
			Config: cfg, Logger: zerolog.Nop(), Auth: svc,
			Health:        handlers.NewHealthHandler(stubPinger{}, ready),
			APIRegistrars: []httpmod.APIRouteRegistrar{fakeAPIRoute{called: &called}},
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil)
		req.Header.Set("Authorization", "Bearer ok")
		r.ServeHTTP(rec, req)
		Expect(called).To(BeTrue())
		Expect(rec.Code).To(Equal(http.StatusOK))
	})
})

var _ = Describe("StartServer", func() {
	It("registers OnStart/OnStop hooks and shuts down cleanly", func() {
		lc := fxtest.NewLifecycle(GinkgoT())
		httpmod.StartServer(lc, &config.Config{ServerPort: 0}, http.NotFoundHandler(), zerolog.Nop())
		Expect(lc.Start(context.Background())).To(Succeed())
		Expect(lc.Stop(context.Background())).To(Succeed())
	})

	It("fails OnStart when the port cannot be bound", func() {
		lc := fxtest.NewLifecycle(GinkgoT())
		httpmod.StartServer(lc, &config.Config{ServerPort: -1}, http.NotFoundHandler(), zerolog.Nop())
		Expect(lc.Start(context.Background())).To(HaveOccurred())
	})
})

var _ = Describe("AsAuthRoute / AsAPIRoute helpers", func() {
	It("return non-nil annotations", func() {
		Expect(httpmod.AsAuthRoute(func() httpmod.AuthRouteRegistrar { return fakeAuthRoute{} })).NotTo(BeNil())
		Expect(httpmod.AsAPIRoute(func() httpmod.APIRouteRegistrar { return fakeAPIRoute{} })).NotTo(BeNil())
	})
})
