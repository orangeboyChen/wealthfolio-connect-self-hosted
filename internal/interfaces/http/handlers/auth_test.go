package handlers_test

import (
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
)

var _ = Describe("AuthHandler.OTP", func() {
	var (
		ctrl   *gomock.Controller
		signer *authmocks.MockSigner
		refr   *authmocks.MockRefreshTokens
		repo   *repomocks.MockTokenRepository
		router chi.Router
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		signer = authmocks.NewMockSigner(ctrl)
		refr = authmocks.NewMockRefreshTokens(ctrl)
		repo = repomocks.NewMockTokenRepository(ctrl)
		svc := appauth.NewService(signer, refr, repo, &config.Config{
			TokenTTL:             time.Hour,
			SelfHostedUserEmails: []string{"alice@example.com"},
		})
		h := handlers.NewAuthHandler(svc)
		router = chi.NewRouter()
		h.RegisterAuthRoutes(router)
	})
	AfterEach(func() { ctrl.Finish() })

	doPost := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	It("rejects malformed JSON", func() {
		rec := doPost("/otp", "not-json")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
	})

	It("rejects missing email", func() {
		rec := doPost("/otp", `{}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
	})

	It("rejects emails not on the allow-list with 403", func() {
		rec := doPost("/otp", `{"email":"eve@example.com"}`)
		Expect(rec.Code).To(Equal(http.StatusForbidden))
		Expect(rec.Body.String()).To(ContainSubstring("validation_failed"))
	})

	It("returns 200 with a Supabase-shaped body for allow-listed emails", func() {
		rec := doPost("/otp", `{"email":"alice@example.com"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"message_id":null`))
	})
})

var _ = Describe("AuthHandler.Verify", func() {
	var (
		ctrl   *gomock.Controller
		signer *authmocks.MockSigner
		refr   *authmocks.MockRefreshTokens
		repo   *repomocks.MockTokenRepository
		router chi.Router
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		signer = authmocks.NewMockSigner(ctrl)
		refr = authmocks.NewMockRefreshTokens(ctrl)
		repo = repomocks.NewMockTokenRepository(ctrl)
		svc := appauth.NewService(signer, refr, repo, &config.Config{
			TokenTTL:             time.Hour,
			SelfHostedUserEmails: []string{"alice@example.com"},
		})
		h := handlers.NewAuthHandler(svc)
		router = chi.NewRouter()
		h.RegisterAuthRoutes(router)
	})
	AfterEach(func() { ctrl.Finish() })

	doPost := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	It("rejects malformed JSON", func() {
		rec := doPost("not-json")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
	})

	It("rejects missing email", func() {
		rec := doPost(`{"token":"123456"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
	})

	It("rejects invalid OTPs with 400 + otp_invalid", func() {
		rec := doPost(`{"email":"alice@example.com","token":"abc"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("otp_invalid"))
	})

	It("rejects emails not on the allow-list with 403", func() {
		rec := doPost(`{"email":"eve@example.com","token":"123456"}`)
		Expect(rec.Code).To(Equal(http.StatusForbidden))
		Expect(rec.Body.String()).To(ContainSubstring("validation_failed"))
	})

	It("returns 500 when token signing fails", func() {
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("", errors.New("kaput"))
		rec := doPost(`{"email":"alice@example.com","token":"123456"}`)
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).To(ContainSubstring("server_error"))
	})

	It("returns 200 with a Supabase-shaped Session on success", func() {
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("jwt", nil)
		refr.EXPECT().Issue(gomock.Any(), gomock.Any()).Return("rt", nil)
		repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
		rec := doPost(`{"email":"alice@example.com","token":"123456","type":"email"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(`"access_token":"jwt"`))
		Expect(body).To(ContainSubstring(`"refresh_token":"rt"`))
		Expect(body).To(ContainSubstring(`"token_type":"bearer"`))
		Expect(body).To(ContainSubstring(`"email":"alice@example.com"`))
		Expect(body).To(ContainSubstring(`"role":"authenticated"`))
		Expect(body).To(ContainSubstring(`"provider":"email"`))
	})
})

var _ = Describe("AuthHandler.Logout", func() {
	It("returns 204 No Content", func() {
		svc := appauth.NewService(nil, nil, nil, &config.Config{})
		h := handlers.NewAuthHandler(svc)
		router := chi.NewRouter()
		h.RegisterAuthRoutes(router)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/logout", nil))
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(rec.Body.Len()).To(Equal(0))
	})
})

var _ = Describe("AuthHandler.Token", func() {
	var (
		ctrl   *gomock.Controller
		signer *authmocks.MockSigner
		refr   *authmocks.MockRefreshTokens
		repo   *repomocks.MockTokenRepository
		router chi.Router
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		signer = authmocks.NewMockSigner(ctrl)
		refr = authmocks.NewMockRefreshTokens(ctrl)
		repo = repomocks.NewMockTokenRepository(ctrl)
		svc := appauth.NewService(signer, refr, repo, &config.Config{
			TokenTTL:             time.Hour,
			SelfHostedUserEmails: []string{"alice@example.com"},
		})
		h := handlers.NewAuthHandler(svc)
		router = chi.NewRouter()
		h.RegisterAuthRoutes(router)
	})
	AfterEach(func() { ctrl.Finish() })

	doPost := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	It("rejects unsupported grant_type", func() {
		rec := doPost("/token?grant_type=authorization_code", `{"refresh_token":"rt"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("unsupported_grant_type"))
	})

	It("rejects malformed JSON", func() {
		rec := doPost("/token?grant_type=refresh_token", "not-json")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
	})

	It("rejects missing refresh_token", func() {
		rec := doPost("/token?grant_type=refresh_token", `{}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
	})

	It("returns 401 for invalid refresh token", func() {
		refr.EXPECT().Validate(gomock.Any(), "bad-rt").Return("", domainauth.ErrInvalidRefreshToken)
		rec := doPost("/token?grant_type=refresh_token", `{"refresh_token":"bad-rt"}`)
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_grant"))
	})

	It("returns 500 when signing fails", func() {
		refr.EXPECT().Validate(gomock.Any(), "good-rt").Return("user-id", nil)
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("", errors.New("sign error"))
		rec := doPost("/token?grant_type=refresh_token", `{"refresh_token":"good-rt"}`)
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).To(ContainSubstring("server_error"))
	})

	It("returns 200 with new token pair on success", func() {
		refr.EXPECT().Validate(gomock.Any(), "good-rt").Return("user-id", nil)
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("new-jwt", nil)
		refr.EXPECT().Issue(gomock.Any(), "user-id").Return("new-rt", nil)
		repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
		rec := doPost("/token?grant_type=refresh_token", `{"refresh_token":"good-rt"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(`"access_token":"new-jwt"`))
		Expect(body).To(ContainSubstring(`"refresh_token":"new-rt"`))
		Expect(body).To(ContainSubstring(`"expires_in"`))
	})
})

var _ = Describe("AuthHandler.GetUser", func() {
	var (
		ctrl   *gomock.Controller
		signer *authmocks.MockSigner
		refr   *authmocks.MockRefreshTokens
		repo   *repomocks.MockTokenRepository
		router chi.Router
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		signer = authmocks.NewMockSigner(ctrl)
		refr = authmocks.NewMockRefreshTokens(ctrl)
		repo = repomocks.NewMockTokenRepository(ctrl)
		svc := appauth.NewService(signer, refr, repo, &config.Config{
			TokenTTL:             time.Hour,
			SelfHostedUserEmails: []string{"alice@example.com"},
		})
		h := handlers.NewAuthHandler(svc)
		router = chi.NewRouter()
		h.RegisterAuthRoutes(router)
	})
	AfterEach(func() { ctrl.Finish() })

	doGet := func(authHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/user", nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	It("returns 401 when no Authorization header", func() {
		rec := doGet("")
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("unauthorized"))
	})

	It("returns 401 when Authorization header is not Bearer", func() {
		rec := doGet("Basic abc123")
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("unauthorized"))
	})

	It("returns 401 when token is expired", func() {
		signer.EXPECT().Verify(gomock.Any(), "expired-jwt").Return(
			domainauth.Claims{}, domainauth.ErrTokenExpired)
		rec := doGet("Bearer expired-jwt")
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("token_expired"))
	})

	It("returns 401 when token is invalid", func() {
		signer.EXPECT().Verify(gomock.Any(), "bad-jwt").Return(
			domainauth.Claims{}, domainauth.ErrInvalidToken)
		rec := doGet("Bearer bad-jwt")
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("unauthorized"))
	})

	It("returns 200 with Supabase-shaped user on valid token", func() {
		signer.EXPECT().Verify(gomock.Any(), "good-jwt").Return(
			domainauth.Claims{
				Subject:   "user-uuid",
				IssuedAt:  time.Now(),
				ExpiresAt: time.Now().Add(time.Hour),
			}, nil)
		rec := doGet("Bearer good-jwt")
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(`"id":"user-uuid"`))
		Expect(body).To(ContainSubstring(`"email":"alice@example.com"`))
		Expect(body).To(ContainSubstring(`"role":"authenticated"`))
		Expect(body).To(ContainSubstring(`"aud":"authenticated"`))
		Expect(body).To(ContainSubstring(`"provider"`))
	})
})
