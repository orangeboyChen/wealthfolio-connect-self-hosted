package handlers_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

var _ = Describe("ActivityHandler.List", func() {
	var (
		ctrl    *gomock.Controller
		actRepo *repomocks.MockActivityRepository
		accRepo *repomocks.MockAccountRepository
		router  chi.Router
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		actRepo = repomocks.NewMockActivityRepository(ctrl)
		accRepo = repomocks.NewMockAccountRepository(ctrl)
		svc := appbrokerage.NewActivityService(actRepo, accRepo)
		h := handlers.NewActivityHandler(svc)
		router = chi.NewRouter()
		h.RegisterAPIRoutes(router)
	})
	AfterEach(func() { ctrl.Finish() })

	doGet := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	It("returns 404 when the account is missing", func() {
		accRepo.EXPECT().Get(gomock.Any(), "missing").Return(brokerage.Account{}, repository.ErrNotFound)
		rec := doGet("/sync/brokerage/accounts/missing/activities")
		Expect(rec.Code).To(Equal(http.StatusNotFound))
		Expect(rec.Body.String()).To(ContainSubstring("ACCOUNT_NOT_FOUND"))
	})

	It("returns 400 on invalid offset", func() {
		rec := doGet("/sync/brokerage/accounts/a/activities?offset=oops")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("INVALID_OFFSET"))
	})

	It("returns 400 on invalid limit", func() {
		rec := doGet("/sync/brokerage/accounts/a/activities?limit=nope")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("INVALID_LIMIT"))
	})

	It("returns 400 on invalid start_date", func() {
		rec := doGet("/sync/brokerage/accounts/a/activities?start_date=2026/01/01")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("INVALID_START_DATE"))
	})

	It("returns 400 on invalid end_date", func() {
		rec := doGet("/sync/brokerage/accounts/a/activities?end_date=foo")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("INVALID_END_DATE"))
	})

	It("forwards filters to the repository and maps DTOs", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a"}, nil)
		actRepo.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ interface{}, f repository.ActivityFilter) ([]brokerage.Activity, int, error) {
				Expect(f.AccountID).To(Equal("a"))
				Expect(f.Offset).To(Equal(10))
				Expect(f.Limit).To(Equal(50))
				Expect(f.StartDate).NotTo(BeNil())
				Expect(f.EndDate).NotTo(BeNil())
				return []brokerage.Activity{
					{
						ID:        "t1",
						Type:      brokerage.ActivityBuy,
						TradeDate: time.Date(2026, 5, 30, 9, 30, 0, 0, time.UTC),
						Price:     380,
						Units:     100,
						Amount:    38000,
						Currency:  brokerage.Currency{Code: "HKD", Name: "HKD"},
						Symbol: &brokerage.Symbol{
							Symbol:    "00700",
							RawSymbol: "00700.HK",
							Type:      brokerage.SymbolType{Code: "EQUITY", IsSupported: true},
							Exchange:  brokerage.Exchange{Code: "HKEX"},
							Currency:  brokerage.Currency{Code: "HKD"},
						},
						OptionSymbol: &brokerage.OptionSymbol{
							Ticker: "AAPL", OptionType: brokerage.OptionCall,
							ExpirationDate: time.Date(2026, 12, 19, 0, 0, 0, 0, time.UTC),
						},
						Subtype:       "MARKET",
						OptionType:    "CALL",
						SourceGroupID: "g1",
						RawType:       "BUY_MARKET",
						Fee:           50,
					},
				}, 1, nil
			})
		rec := doGet("/sync/brokerage/accounts/a/activities?offset=10&limit=50&start_date=2026-01-01&end_date=2026-12-31")
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(`"id":"t1"`))
		Expect(body).To(ContainSubstring(`"type":"BUY"`))
		Expect(body).To(ContainSubstring(`"symbol":"00700"`))
		Expect(body).To(ContainSubstring(`"option_type":"CALL"`))
		Expect(body).To(ContainSubstring(`"subtype":"MARKET"`))
		Expect(body).To(ContainSubstring(`"source_group_id":"g1"`))
		Expect(body).To(ContainSubstring(`"offset":10`))
		Expect(body).To(ContainSubstring(`"limit":50`))
		Expect(body).To(ContainSubstring(`"total":1`))
		Expect(body).To(ContainSubstring(`"has_more":false`))
	})

	It("returns 500 on repository failure", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a"}, nil)
		actRepo.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, 0, errors.New("db"))
		rec := doGet("/sync/brokerage/accounts/a/activities")
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})
})

var _ = Describe("HoldingHandler.Get", func() {
	var (
		ctrl    *gomock.Controller
		hldRepo *repomocks.MockHoldingRepository
		accRepo *repomocks.MockAccountRepository
		router  chi.Router
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		hldRepo = repomocks.NewMockHoldingRepository(ctrl)
		accRepo = repomocks.NewMockAccountRepository(ctrl)
		svc := appbrokerage.NewHoldingService(hldRepo, accRepo)
		h := handlers.NewHoldingHandler(svc)
		router = chi.NewRouter()
		h.RegisterAPIRoutes(router)
	})
	AfterEach(func() { ctrl.Finish() })

	doGet := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	It("returns 404 when account is missing", func() {
		accRepo.EXPECT().Get(gomock.Any(), "x").Return(brokerage.Account{}, repository.ErrNotFound)
		rec := doGet("/sync/brokerage/accounts/x/holdings")
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("returns 200 with empty snapshot when no record yet", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{
			ID: "a", Name: "n", AccountNumber: "***1", RawType: "MARGIN",
		}, nil)
		hldRepo.EXPECT().GetLatest(gomock.Any(), "a").Return(brokerage.Holdings{}, repository.ErrNotFound)
		rec := doGet("/sync/brokerage/accounts/a/holdings")
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(`"id":"a"`))
		Expect(body).To(ContainSubstring(`"balances":[]`))
		Expect(body).To(ContainSubstring(`"positions":[]`))
		Expect(body).To(ContainSubstring(`"option_positions":[]`))
	})

	It("maps snapshot positions, balances and option positions", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a", Name: "n", AccountNumber: "x", RawType: "MARGIN"}, nil)
		hldRepo.EXPECT().GetLatest(gomock.Any(), "a").Return(brokerage.Holdings{
			AccountID: "a",
			Balances:  []brokerage.Balance{{Currency: brokerage.Currency{Code: "HKD"}, Cash: 100, BuyingPower: 200}},
			Positions: []brokerage.Position{
				{
					Symbol: brokerage.Symbol{
						Symbol: "00700", RawSymbol: "00700.HK", Description: "Tencent",
						Type:     brokerage.SymbolType{Code: "EQUITY", IsSupported: true},
						Exchange: brokerage.Exchange{Code: "HKEX"},
						Currency: brokerage.Currency{Code: "HKD"},
					},
					Units: 10, Price: 380, OpenPnL: 50, AveragePurchasePrice: 375,
					Currency: brokerage.Currency{Code: "HKD"}, CashEquivalent: false,
				},
			},
			OptionPositions: []brokerage.OptionPosition{
				{
					OptionSymbol: brokerage.OptionSymbol{
						Ticker: "AAPL", OptionType: brokerage.OptionCall,
						StrikePrice: 150, ExpirationDate: time.Date(2026, 12, 19, 0, 0, 0, 0, time.UTC),
						Underlying: brokerage.Symbol{Symbol: "AAPL"},
					},
					Units: 2, Price: 15.5, AveragePurchasePrice: 12,
					Currency: brokerage.Currency{Code: "USD"},
				},
			},
		}, nil)
		rec := doGet("/sync/brokerage/accounts/a/holdings")
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(`"cash":100`))
		Expect(body).To(ContainSubstring(`"buying_power":200`))
		Expect(body).To(ContainSubstring(`"symbol":"00700"`))
		Expect(body).To(ContainSubstring(`"holding-00700"`))
		Expect(body).To(ContainSubstring(`"open_pnl":50`))
		Expect(body).To(ContainSubstring(`"option_type":"CALL"`))
		Expect(body).To(ContainSubstring(`"strike_price":150`))
	})

	It("returns 500 when repository fails unexpectedly", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a"}, nil)
		hldRepo.EXPECT().GetLatest(gomock.Any(), "a").Return(brokerage.Holdings{}, errors.New("db"))
		rec := doGet("/sync/brokerage/accounts/a/holdings")
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})
})
