package handlers_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	appauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/auth"
	domainauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth"
	authmocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth/mocks"
	repomocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository/mocks"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/handlers"
	mw "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

var _ = Describe("AuthHandler.Token", func() {
	var (
		ctrl   *gomock.Controller
		signer *authmocks.MockSigner
		refr   *authmocks.MockRefreshTokens
		repo   *repomocks.MockTokenRepository
		svc    *appauth.Service
		h      *handlers.AuthHandler
		router chi.Router
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		signer = authmocks.NewMockSigner(ctrl)
		refr = authmocks.NewMockRefreshTokens(ctrl)
		repo = repomocks.NewMockTokenRepository(ctrl)
		svc = appauth.NewService(signer, refr, repo, &config.Config{TokenTTL: time.Hour, ConnectAuthPublishableKey: "pk"})
		h = handlers.NewAuthHandler(svc)
		router = chi.NewRouter()
		h.RegisterAuthRoutes(router)
	})

	AfterEach(func() { ctrl.Finish() })

	doRequest := func(query, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/token"+query, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	It("rejects unsupported grant types", func() {
		rec := doRequest("?grant_type=password", `{"refresh_token":"x"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("unsupported_grant_type"))
	})

	It("rejects bodies without refresh_token", func() {
		rec := doRequest("?grant_type=refresh_token", `{}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
	})

	It("rejects malformed JSON", func() {
		rec := doRequest("", "not-json")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("returns 401 on invalid refresh token", func() {
		refr.EXPECT().Validate(gomock.Any(), "bad").Return("", domainauth.ErrInvalidRefreshToken)
		rec := doRequest("?grant_type=refresh_token", `{"refresh_token":"bad"}`)
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_grant"))
	})

	It("returns 500 on unexpected refresh errors", func() {
		refr.EXPECT().Validate(gomock.Any(), "x").Return("u", nil)
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("", errors.New("kaput"))
		rec := doRequest("", `{"refresh_token":"x"}`)
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})

	It("returns 200 with token pair on success", func() {
		refr.EXPECT().Validate(gomock.Any(), "ok").Return("u", nil)
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("jwt", nil)
		refr.EXPECT().Issue(gomock.Any(), "u").Return("rt2", nil)
		repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
		rec := doRequest("?grant_type=refresh_token", `{"refresh_token":"ok"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"access_token":"jwt"`))
		Expect(rec.Body.String()).To(ContainSubstring(`"refresh_token":"rt2"`))
	})
})

var _ = Describe("UserHandler.Me", func() {
	It("emits a Pro/active team", func() {
		h := handlers.NewUserHandler(&config.Config{})
		router := chi.NewRouter()
		h.RegisterAPIRoutes(router)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user/me", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"plan":"pro"`))
		Expect(rec.Body.String()).To(ContainSubstring(`"subscriptionStatus":"active"`))
	})

	It("uses the JWT subject when present", func() {
		h := handlers.NewUserHandler(&config.Config{})
		req := httptest.NewRequest(http.MethodGet, "/user/me", nil)
		req = req.WithContext(injectClaims(req.Context(), domainauth.Claims{Subject: "alice"}))
		rec := httptest.NewRecorder()
		h.Me(rec, req)
		Expect(rec.Body.String()).To(ContainSubstring(`"id":"alice"`))
	})
})

var _ = Describe("SubscriptionHandler.Plans", func() {
	It("returns the static plan list", func() {
		h := handlers.NewSubscriptionHandler()
		router := chi.NewRouter()
		h.RegisterPublicAPIRoutes(router)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/subscription/plans", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"id":"pro"`))
		Expect(rec.Body.String()).To(ContainSubstring("broker_sync"))
	})
})

// injectClaims smuggles claims into the context exactly the way the Bearer
// middleware does, without depending on its private context key.
func injectClaims(ctx context.Context, claims domainauth.Claims) context.Context {
	// Use a real Bearer middleware run to inject claims.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", bytes.NewBufferString("")).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer t")

	ctrl := gomock.NewController(GinkgoT())
	signer := authmocks.NewMockSigner(ctrl)
	signer.EXPECT().Verify(gomock.Any(), "t").Return(claims, nil)

	repo := repomocks.NewMockTokenRepository(ctrl)
	refr := authmocks.NewMockRefreshTokens(ctrl)
	svc := appauth.NewService(signer, refr, repo, &config.Config{TokenTTL: time.Hour})

	var captured context.Context
	mw.Bearer(svc)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = r.Context()
	})).ServeHTTP(rec, req)
	return captured
}
