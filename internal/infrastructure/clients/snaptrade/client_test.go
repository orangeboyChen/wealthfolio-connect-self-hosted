package snaptrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"

	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

func testConfig(baseURL string) config.SnapTradeConfig {
	return config.SnapTradeConfig{
		Enabled: true, AuthMode: "personal", Package: "personal", ClientID: "client", ConsumerKey: "consumer",
		BaseURL: baseURL, HistoryStartDate: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
		SyncInterval: time.Hour, RequestInterval: 0, SafetyPercent: 80, PageSize: 2,
		MaxRetries: 0, RetryBaseDelay: time.Second, RetryMaxDelay: time.Second,
		IncrementalOverlap: 7 * 24 * time.Hour, RequestTimeout: time.Second, ManualRefreshCooldown: 24 * time.Hour,
	}
}

func testClient(cfg config.SnapTradeConfig, doer HTTPDoer) (*Client, *fakeClock, *[]time.Duration) {
	client, err := New(cfg, zerolog.Nop(), doer)
	Expect(err).NotTo(HaveOccurred())
	clk := &fakeClock{now: time.Unix(1700000000, 0)}
	waits := &[]time.Duration{}
	var mu sync.Mutex
	sleep := advancingSleeper(clk, waits, &mu)
	client.api.signer.clock = clk
	client.api.limiter.clock = clk
	client.api.limiter.sleep = sleep
	client.api.sleep = sleep
	client.api.jitter = func(time.Duration) time.Duration { return 0 }
	return client, clk, waits
}

func respondJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

var _ = Describe("SnapTrade client", func() {
	It("validates base URLs and creates a default reusable HTTP client", func() {
		cfg := testConfig("://invalid")
		_, err := New(cfg, zerolog.Nop(), nil)
		Expect(err).To(MatchError(ContainSubstring("invalid base URL")))

		cfg = testConfig("https://example.test")
		cfg.Package = "commercial"
		client, err := New(cfg, zerolog.Nop(), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(client.api.doer).To(BeAssignableToTypeOf(&http.Client{}))
	})

	It("exposes its scheduler identity and supports fallback IBKR discovery through Fetch", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			switch r.URL.Path {
			case "/api/v1/authorizations":
				respondJSON(w, []any{})
			case "/api/v1/accounts":
				respondJSON(w, []any{map[string]any{
					"id": "fallback", "brokerage_authorization": "legacy-auth",
					"institution_name": "Interactive Brokers (U.K.) Limited",
				}})
			case "/api/v1/accounts/fallback":
				respondJSON(w, map[string]any{"id": "fallback", "brokerage_authorization": "legacy-auth", "institution_name": "Interactive Brokers"})
			case "/api/v1/accounts/fallback/balances":
				respondJSON(w, []any{})
			case "/api/v1/accounts/fallback/positions/all":
				respondJSON(w, map[string]any{"results": []any{}})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		client, _, _ := testClient(testConfig(server.URL), server.Client())
		Expect(client.ID()).To(Equal("snaptrade"))
		Expect(client.SyncInterval()).To(Equal(time.Hour))
		snapshot, err := client.Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Accounts).To(HaveLen(1))
		Expect(snapshot.Holdings).To(HaveLen(1))
	})

	It("discovers only IBKR accounts and imports details, multi-currency balances, and unified positions", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.URL.Query().Get("clientId")).To(Equal("client"))
			Expect(r.URL.Query().Get("timestamp")).To(Equal("1700000000"))
			Expect(r.URL.Query().Has("userId")).To(BeFalse())
			Expect(r.Header.Get("Signature")).NotTo(BeEmpty())
			switch r.URL.Path {
			case "/api/v1/authorizations":
				respondJSON(w, []any{
					map[string]any{"id": "auth-ibkr", "brokerage": map[string]any{"slug": "INTERACTIVE_BROKERS", "name": "Interactive Brokers", "display_name": "IBKR", "enabled": true}},
					map[string]any{"id": "auth-other", "brokerage": map[string]any{"slug": "FIDELITY", "name": "Fidelity", "enabled": true}},
				})
			case "/api/v1/accounts":
				respondJSON(w, []any{
					map[string]any{"id": "account-ibkr", "brokerage_authorization": "auth-ibkr", "name": "Margin", "institution_name": "Interactive Brokers", "created_date": "2024-01-01T00:00:00Z", "balance": map[string]any{"total": map[string]any{"amount": 1000, "currency": "EUR"}}},
					map[string]any{"id": "account-other", "brokerage_authorization": "auth-other", "institution_name": "Interactive Brokers"},
				})
			case "/api/v1/accounts/account-ibkr":
				respondJSON(w, map[string]any{"id": "account-ibkr", "brokerage_authorization": "auth-ibkr", "name": "Margin", "raw_type": "MARGIN", "institution_name": "Interactive Brokers LLC", "created_date": "2024-01-01T00:00:00Z", "balance": map[string]any{"total": map[string]any{"amount": 1200, "currency": "EUR"}}})
			case "/api/v1/accounts/account-ibkr/balances":
				respondJSON(w, []any{
					map[string]any{"currency": map[string]any{"code": "EUR", "name": "Euro"}, "cash": 100, "buying_power": 200},
					map[string]any{"currency": map[string]any{"code": "USD", "name": "US Dollar"}, "cash": 50, "buying_power": 50},
				})
			case "/api/v1/accounts/account-ibkr/positions/all":
				respondJSON(w, map[string]any{"results": []any{
					map[string]any{"instrument": map[string]any{"kind": "stock", "symbol": "AAPL", "raw_symbol": "AAPL", "currency": "USD", "exchange": "XNAS"}, "units": "2", "price": "200", "cost_basis": "150", "currency": "USD"},
					map[string]any{"instrument": map[string]any{"kind": "option", "ticker": "AAPL-C", "option_type": "CALL", "strike_price": "240", "expiration_date": "2026-12-18"}, "units": "1", "price": "4", "cost_basis": "3", "currency": "USD"},
				}})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		client, _, _ := testClient(testConfig(server.URL), server.Client())
		snapshot, err := client.FetchAccountSnapshot(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Connections).To(HaveLen(1))
		Expect(snapshot.Accounts).To(HaveLen(1))
		Expect(snapshot.Accounts[0].ID).To(Equal("snaptrade-account-ibkr"))
		Expect(snapshot.Accounts[0].BalanceCurrency).To(Equal("EUR"))
		Expect(snapshot.Holdings).To(HaveLen(1))
		Expect(snapshot.Holdings[0].Balances).To(HaveLen(2))
		Expect(snapshot.Holdings[0].Positions).To(HaveLen(1))
		Expect(snapshot.Holdings[0].OptionPositions).To(HaveLen(1))
	})

	It("streams reverse-chronological pages and resumes from a persisted offset", func() {
		var offsets []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			offsets = append(offsets, r.URL.Query().Get("offset"))
			Expect(r.URL.Query().Get("startDate")).To(Equal("2022-01-01"))
			offset := r.URL.Query().Get("offset")
			if offset == "0" {
				respondJSON(w, map[string]any{"data": []any{
					map[string]any{"id": "3", "type": "DIVIDEND", "trade_date": "2024-03-03", "amount": 3, "currency": map[string]any{"code": "USD"}},
					map[string]any{"id": "2", "type": "DIVIDEND", "trade_date": "2024-03-02", "amount": 2, "currency": map[string]any{"code": "USD"}},
				}, "pagination": map[string]any{"offset": 0, "limit": 2, "total": 3}})
				return
			}
			respondJSON(w, map[string]any{"data": []any{
				map[string]any{"id": "1", "type": "DIVIDEND", "trade_date": "2024-03-01", "amount": 1, "currency": map[string]any{"code": "USD"}},
			}, "pagination": map[string]any{"offset": 2, "limit": 2, "total": 3}})
		}))
		defer server.Close()
		client, _, _ := testClient(testConfig(server.URL), server.Client())
		client.selected["snaptrade-account"] = selectedAccount{account: rawAccount{ID: "account"}}
		var pages []domainsync.ActivityPage
		err := client.StreamActivities(context.Background(), []domainsync.ActivitySyncState{{AccountID: "snaptrade-account"}}, func(_ context.Context, page domainsync.ActivityPage) error {
			pages = append(pages, page)
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(offsets).To(Equal([]string{"0", "2"}))
		Expect(pages).To(HaveLen(2))
		Expect(pages[0].Complete).To(BeFalse())
		Expect(pages[1].Complete).To(BeTrue())
		Expect(pages[1].FirstTransactionDate).NotTo(BeNil())

		offsets = nil
		err = client.StreamActivities(context.Background(), []domainsync.ActivitySyncState{{AccountID: "snaptrade-account", NextOffset: 2}}, func(_ context.Context, page domainsync.ActivityPage) error { return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(offsets).To(Equal([]string{"2"}))
	})

	It("uses the incremental overlap after initial completion", func() {
		var startDate string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			startDate = r.URL.Query().Get("startDate")
			respondJSON(w, map[string]any{"data": []any{}, "pagination": map[string]any{"offset": 0, "limit": 2, "total": 0}})
		}))
		defer server.Close()
		client, _, _ := testClient(testConfig(server.URL), server.Client())
		client.selected["snaptrade-account"] = selectedAccount{account: rawAccount{ID: "account"}}
		last := time.Date(2024, 3, 10, 12, 0, 0, 0, time.UTC)
		err := client.StreamActivities(context.Background(), []domainsync.ActivitySyncState{{
			AccountID: "snaptrade-account", InitialSyncCompleted: true, LastSuccessfulSync: &last,
		}}, func(_ context.Context, page domainsync.ActivityPage) error { return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(startDate).To(Equal("2024-03-03"))
	})

	It("honors Retry-After on 429 and bounds retries", func() {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			if attempts.Add(1) == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprint(w, `{"detail":"Request was throttled. Expected available in 3 seconds."}`)
				return
			}
			respondJSON(w, []any{})
		}))
		defer server.Close()
		cfg := testConfig(server.URL)
		cfg.MaxRetries = 1
		client, _, waits := testClient(cfg, server.Client())
		var out []rawAccount
		Expect(client.api.get(context.Background(), "/api/v1/accounts", "accounts", "", nil, &out)).To(Succeed())
		Expect(attempts.Load()).To(Equal(int32(2)))
		Expect(*waits).To(ContainElement(5 * time.Second))
	})

	It("does not retry permanent authentication errors or expose credentials", func() {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			attempts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, `{"detail":"bad https://example.test?clientId=client&userSecret=user-secret"}`)
		}))
		defer server.Close()
		cfg := testConfig(server.URL)
		cfg.AuthMode, cfg.UserID, cfg.UserSecret, cfg.MaxRetries = "commercial", "user", "user-secret", 3
		client, _, _ := testClient(cfg, server.Client())
		var out []rawAccount
		err := client.api.get(context.Background(), "/api/v1/accounts", "accounts", "", nil, &out)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("user-secret"))
		Expect(err.Error()).NotTo(ContainSubstring("clientId=client"))
		Expect(attempts.Load()).To(Equal(int32(1)))
	})

	It("throttles explicitly enabled manual refresh operations per authorization", func() {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.Method).To(Equal(http.MethodPost))
			Expect(r.URL.Path).To(Or(
				Equal("/api/v1/authorizations/auth-id/refresh"),
				Equal("/api/v1/authorizations/auth-id/transactions/sync"),
			))
			calls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		cfg := testConfig(server.URL)
		cfg.AllowManualRefresh = true
		cfg.AllowTransactionSync = true
		client, clk, _ := testClient(cfg, server.Client())
		Expect(client.maybeRefresh(context.Background(), "auth-id")).To(Succeed())
		Expect(client.maybeRefresh(context.Background(), "auth-id")).To(Succeed())
		Expect(calls.Load()).To(Equal(int32(2)))
		clk.advance(cfg.ManualRefreshCooldown)
		Expect(client.maybeRefresh(context.Background(), "auth-id")).To(Succeed())
		Expect(calls.Load()).To(Equal(int32(4)))
	})

	It("returns a partial account snapshot without replacing holdings after an endpoint failure", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			switch r.URL.Path {
			case "/api/v1/authorizations":
				respondJSON(w, []any{map[string]any{"id": "auth", "brokerage": map[string]any{"slug": "IBKR", "name": "Interactive Brokers"}}})
			case "/api/v1/accounts":
				respondJSON(w, []any{map[string]any{"id": "account", "brokerage_authorization": "auth", "institution_name": "Interactive Brokers"}})
			case "/api/v1/accounts/account":
				respondJSON(w, map[string]any{"id": "account", "brokerage_authorization": "auth", "institution_name": "Interactive Brokers"})
			case "/api/v1/accounts/account/balances":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = fmt.Fprint(w, `{"detail":"upstream unavailable"}`)
			case "/api/v1/accounts/account/positions/all":
				respondJSON(w, map[string]any{"results": []any{}})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		client, _, _ := testClient(testConfig(server.URL), server.Client())
		snapshot, err := client.FetchAccountSnapshot(context.Background())
		Expect(err).To(MatchError(ContainSubstring("balances")))
		Expect(snapshot.Accounts).To(HaveLen(1))
		Expect(snapshot.Holdings).To(BeEmpty())
	})

	It("persists disabled accounts without calling account-scoped endpoints", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			switch r.URL.Path {
			case "/api/v1/authorizations":
				respondJSON(w, []any{map[string]any{
					"id": "disabled-auth", "disabled": true,
					"brokerage": map[string]any{"slug": "IBKR", "name": "Interactive Brokers"},
				}})
			case "/api/v1/accounts":
				respondJSON(w, []any{map[string]any{
					"id": "disabled-account", "brokerage_authorization": "disabled-auth",
					"institution_name": "Interactive Brokers",
				}})
			default:
				Fail("disabled connection must not call account endpoints: " + r.URL.Path)
			}
		}))
		defer server.Close()
		client, _, _ := testClient(testConfig(server.URL), server.Client())
		snapshot, err := client.FetchAccountSnapshot(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Accounts).To(HaveLen(1))
		Expect(snapshot.Accounts[0].Status).To(Equal("disconnected"))
		Expect(snapshot.Holdings).To(BeEmpty())
	})

	It("validates activity sinks and detects pagination that cannot advance", func() {
		client, _, _ := testClient(testConfig("https://example.test"), http.DefaultClient)
		Expect(client.StreamActivities(context.Background(), nil, nil)).To(MatchError(ContainSubstring("sink is nil")))

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			defer GinkgoRecover()
			total := 2
			respondJSON(w, rawActivityPage{Pagination: rawPagination{Total: &total}})
		}))
		defer server.Close()
		client, _, _ = testClient(testConfig(server.URL), server.Client())
		client.selected["snaptrade-account"] = selectedAccount{account: rawAccount{ID: "account"}}
		err := client.StreamActivities(context.Background(), []domainsync.ActivitySyncState{{AccountID: "snaptrade-account"}}, func(context.Context, domainsync.ActivityPage) error {
			return nil
		})
		Expect(err).To(MatchError(ContainSubstring("made no progress")))
	})
})
