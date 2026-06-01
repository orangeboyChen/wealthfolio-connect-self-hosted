package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"go.uber.org/mock/gomock"

	appauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/auth"
	domainauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth"
	authmocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth/mocks"
	repomocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository/mocks"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
	mw "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

func TestMiddleware(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Middleware Suite")
}

var _ = Describe("RequestID", func() {
	It("sets X-Request-Id when missing and exposes it via context", func() {
		var seen string
		next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen = mw.RequestIDFrom(r.Context())
		})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		mw.RequestID(next).ServeHTTP(rec, req)
		Expect(rec.Header().Get("X-Request-Id")).NotTo(BeEmpty())
		Expect(seen).To(Equal(rec.Header().Get("X-Request-Id")))
	})

	It("preserves an existing X-Request-Id", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Request-Id", "client-id")
		rec := httptest.NewRecorder()
		mw.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})).ServeHTTP(rec, req)
		Expect(rec.Header().Get("X-Request-Id")).To(Equal("client-id"))
	})

	It("returns empty string when no id is set", func() {
		Expect(mw.RequestIDFrom(context.Background())).To(BeEmpty())
	})
})

var _ = Describe("Recover", func() {
	It("turns panics into 500 JSON errors", func() {
		log := zerolog.Nop()
		next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			panic("boom")
		})
		rec := httptest.NewRecorder()
		mw.Recover(log)(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).To(ContainSubstring("INTERNAL_ERROR"))
	})
})

var _ = Describe("Logger", func() {
	It("calls the next handler and logs", func() {
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusTeapot)
		})
		rec := httptest.NewRecorder()
		mw.Logger(zerolog.Nop())(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		Expect(called).To(BeTrue())
		Expect(rec.Code).To(Equal(http.StatusTeapot))
	})
})

var _ = Describe("CORS", func() {
	It("answers OPTIONS preflight successfully", func() {
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Origin", "https://example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		mw.CORS(nil)(next).ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically("<", 400))
	})

	It("respects explicitly configured origins", func() {
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://app.example")
		mw.CORS([]string{"https://app.example"})(next).ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically("<", 400))
	})
})

var _ = Describe("APIKey middleware", func() {
	var (
		ctrl   *gomock.Controller
		signer *authmocks.MockSigner
		refr   *authmocks.MockRefreshTokens
		repo   *repomocks.MockTokenRepository
		svc    *appauth.Service
	)
	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		signer = authmocks.NewMockSigner(ctrl)
		refr = authmocks.NewMockRefreshTokens(ctrl)
		repo = repomocks.NewMockTokenRepository(ctrl)
		svc = appauth.NewService(signer, refr, repo, &config.Config{ConnectAuthPublishableKey: "pk", TokenTTL: time.Hour})
	})
	AfterEach(func() { ctrl.Finish() })

	It("rejects missing apikey", func() {
		next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
		rec := httptest.NewRecorder()
		mw.APIKey(svc)(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("error_description"))
	})

	It("allows a matching apikey", func() {
		called := false
		next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("apikey", "pk")
		mw.APIKey(svc)(next).ServeHTTP(rec, req)
		Expect(called).To(BeTrue())
	})
})

var _ = Describe("Bearer middleware", func() {
	var (
		ctrl   *gomock.Controller
		signer *authmocks.MockSigner
		refr   *authmocks.MockRefreshTokens
		repo   *repomocks.MockTokenRepository
		svc    *appauth.Service
	)
	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		signer = authmocks.NewMockSigner(ctrl)
		refr = authmocks.NewMockRefreshTokens(ctrl)
		repo = repomocks.NewMockTokenRepository(ctrl)
		svc = appauth.NewService(signer, refr, repo, &config.Config{TokenTTL: time.Hour})
	})
	AfterEach(func() { ctrl.Finish() })

	It("rejects missing Authorization header", func() {
		rec := httptest.NewRecorder()
		mw.Bearer(svc)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})).
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("Missing bearer"))
	})

	It("rejects invalid tokens with 401", func() {
		signer.EXPECT().Verify(gomock.Any(), "bad").Return(domainauth.Claims{}, domainauth.ErrInvalidToken)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer bad")
		mw.Bearer(svc)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})).
			ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("Invalid"))
	})

	It("returns generic 401 on unexpected verification errors", func() {
		signer.EXPECT().Verify(gomock.Any(), "x").Return(domainauth.Claims{}, errors.New("other"))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer x")
		mw.Bearer(svc)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})).
			ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("Authentication"))
	})

	It("attaches claims to context for valid tokens", func() {
		signer.EXPECT().Verify(gomock.Any(), "ok").Return(domainauth.Claims{
			Subject: "u", ExpiresAt: time.Now().Add(time.Hour),
		}, nil)
		var got domainauth.Claims
		next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			c, ok := mw.ClaimsFrom(r.Context())
			Expect(ok).To(BeTrue())
			got = c
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer ok")
		mw.Bearer(svc)(next).ServeHTTP(rec, req)
		Expect(got.Subject).To(Equal("u"))
	})

	It("returns false when no claims in context", func() {
		_, ok := mw.ClaimsFrom(context.Background())
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("WriteJSON / WriteError / WriteAuthError", func() {
	It("writes a JSON body with the given status", func() {
		rec := httptest.NewRecorder()
		mw.WriteJSON(rec, http.StatusTeapot, map[string]string{"k": "v"})
		Expect(rec.Code).To(Equal(http.StatusTeapot))
		Expect(rec.Header().Get("Content-Type")).To(Equal("application/json; charset=utf-8"))
		Expect(rec.Body.String()).To(ContainSubstring(`"k":"v"`))
	})

	It("formats API errors", func() {
		rec := httptest.NewRecorder()
		mw.WriteError(rec, http.StatusBadRequest, "bad", "BAD", "boom")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring(`"code":"BAD"`))
	})

	It("formats auth errors", func() {
		rec := httptest.NewRecorder()
		mw.WriteAuthError(rec, http.StatusUnauthorized, "invalid_grant", "no")
		Expect(rec.Body.String()).To(ContainSubstring("invalid_grant"))
	})
})
