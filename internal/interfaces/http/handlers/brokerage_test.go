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

	appbrokerage "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
	repomocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository/mocks"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/handlers"
)

var _ = Describe("ConnectionHandler.List", func() {
	var (
		ctrl   *gomock.Controller
		repo   *repomocks.MockConnectionRepository
		router chi.Router
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		repo = repomocks.NewMockConnectionRepository(ctrl)
		svc := appbrokerage.NewConnectionService(repo)
		h := handlers.NewConnectionHandler(svc, nil)
		router = chi.NewRouter()
		h.RegisterAPIRoutes(router)
	})
	AfterEach(func() { ctrl.Finish() })

	It("returns 200 with empty array when no connections", func() {
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sync/brokerage/connections", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"connections":[]`))
	})

	It("maps connections into the API DTO shape", func() {
		repo.EXPECT().List(gomock.Any()).Return([]brokerage.Connection{
			{
				ID:              "conn-001",
				AuthorizationID: "auth-futu-001",
				BrokerageName:   "Futu Securities",
				BrokerageSlug:   "futu",
				DisplayName:     "Futu",
				LogoURL:         "https://example.com/futu.png",
				SquareLogoURL:   "https://example.com/futu-sq.png",
				Disabled:        false,
				Name:            "My Futu",
				Status:          brokerage.ConnectionActive,
				UpdatedAt:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			},
		}, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sync/brokerage/connections", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(`"id":"conn-001"`))
		Expect(body).To(ContainSubstring(`"authorization_id":"auth-futu-001"`))
		Expect(body).To(ContainSubstring(`"brokerage_slug":"futu"`))
		Expect(body).To(ContainSubstring(`"slug":"futu"`))
		Expect(body).To(ContainSubstring(`"aws_s3_logo_url":"https://example.com/futu.png"`))
		Expect(body).To(ContainSubstring(`"status":"active"`))
	})

	It("returns 500 on repository error", func() {
		repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db"))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sync/brokerage/connections", nil))
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})
})

var _ = Describe("AccountHandler.List", func() {
	var (
		ctrl   *gomock.Controller
		repo   *repomocks.MockAccountRepository
		router chi.Router
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		repo = repomocks.NewMockAccountRepository(ctrl)
		svc := appbrokerage.NewAccountService(repo)
		h := handlers.NewAccountHandler(svc)
		router = chi.NewRouter()
		h.RegisterAPIRoutes(router)
	})
	AfterEach(func() { ctrl.Finish() })

	It("returns empty array when no accounts", func() {
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sync/brokerage/accounts", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"accounts":[]`))
	})

	It("maps account fields with sync status and owner", func() {
		txTime := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
		holdingsTime := time.Date(2026, 5, 31, 13, 0, 0, 0, time.UTC)
		firstTx := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
		repo.EXPECT().List(gomock.Any()).Return([]brokerage.Account{
			{
				ID:                     "acct-1",
				Name:                   "港股账户",
				AccountNumber:          "****1234",
				Type:                   brokerage.AccountTypeMargin,
				RawType:                "MARGIN",
				Currency:               "HKD",
				BalanceTotal:           150000,
				BalanceCurrency:        "HKD",
				BrokerageAuthorization: "auth-futu-001",
				InstitutionName:        "Futu",
				SyncEnabled:            true,
				IsPaper:                false,
				Status:                 "open",
				CreatedDate:            time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				LastTxSync:             &txTime,
				LastHoldingsSync:       &holdingsTime,
				FirstTxDate:            &firstTx,
				InitialTxSyncDone:      true,
				InitialHoldingsDone:    true,
				OwnerUserID:            "user-001",
				OwnerFullName:          "Me",
				OwnerEmail:             "me@example.com",
			},
		}, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sync/brokerage/accounts", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(`"id":"acct-1"`))
		Expect(body).To(ContainSubstring(`"type":"MARGIN"`))
		Expect(body).To(ContainSubstring(`"amount":150000`))
		Expect(body).To(ContainSubstring(`"currency":"HKD"`))
		Expect(body).To(ContainSubstring(`"first_transaction_date":"2024-01-15"`))
		Expect(body).To(ContainSubstring(`"is_own_account":true`))
		Expect(body).To(ContainSubstring(`"meta":null`))
	})

	It("falls back to account currency when balance currency missing", func() {
		repo.EXPECT().List(gomock.Any()).Return([]brokerage.Account{
			{ID: "a", Currency: "USD", BalanceTotal: 10},
		}, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sync/brokerage/accounts", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"currency":"USD"`))
	})

	It("returns 500 when repository fails", func() {
		repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db"))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sync/brokerage/accounts", nil))
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})
})

// silence unused import for repository in this file when ErrNotFound isn't used.
var _ = repository.ErrNotFound

var _ = Describe("AccountHandler.Patch", func() {
	var (
		ctrl   *gomock.Controller
		repo   *repomocks.MockAccountRepository
		router chi.Router
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		repo = repomocks.NewMockAccountRepository(ctrl)
		svc := appbrokerage.NewAccountService(repo)
		h := handlers.NewAccountHandler(svc)
		router = chi.NewRouter()
		h.RegisterAPIRoutes(router)
	})
	AfterEach(func() { ctrl.Finish() })

	doPatch := func(id, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/sync/brokerage/accounts/"+id, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	It("rejects malformed JSON", func() {
		rec := doPatch("acc-1", "not-json")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid JSON body"))
	})

	It("rejects bodies without any mutable fields", func() {
		rec := doPatch("acc-1", `{}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("no mutable fields"))
	})

	It("returns 404 when the account is missing", func() {
		repo.EXPECT().SetSyncEnabled(gomock.Any(), "missing", true).Return(repository.ErrNotFound)
		rec := doPatch("missing", `{"sync_enabled":true}`)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
		Expect(rec.Body.String()).To(ContainSubstring("not_found"))
	})

	It("returns 500 on unexpected repository errors", func() {
		repo.EXPECT().SetSyncEnabled(gomock.Any(), "acc-1", false).Return(errors.New("db"))
		rec := doPatch("acc-1", `{"sync_enabled":false}`)
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})

	It("returns 200 with the updated account on success", func() {
		repo.EXPECT().SetSyncEnabled(gomock.Any(), "acc-1", false).Return(nil)
		repo.EXPECT().Get(gomock.Any(), "acc-1").Return(brokerage.Account{
			ID:          "acc-1",
			Currency:    "USD",
			SyncEnabled: false,
		}, nil)
		rec := doPatch("acc-1", `{"sync_enabled":false}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(`"id":"acc-1"`))
		Expect(body).To(ContainSubstring(`"sync_enabled":false`))
	})
})
