package bitget_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/bitget"
)

func TestBitget(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bitget Client Suite")
}

func newServer(handler http.HandlerFunc) (*httptest.Server, *http.Client) {
	srv := httptest.NewServer(handler)
	return srv, srv.Client()
}

var _ = Describe("Bitget Client", func() {
	It("returns slug bitget", func() {
		Expect(bitget.New("", "", "", "", nil).ID()).To(Equal("bitget"))
	})

	It("fails when credentials are missing", func() {
		_, err := bitget.New("", "", "", "http://x", nil).Fetch(context.Background())
		Expect(err).To(HaveOccurred())
	})

	It("signs the assets request, fuses tickers, and translates", func() {
		var assetsHeaders http.Header
		var sawTickers bool
		srv, hc := newServer(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/spot/account/assets":
				assetsHeaders = r.Header
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": "00000",
					"data": []any{
						map[string]any{"coin": "BTC", "available": "0.5", "frozen": "0", "locked": "0"},
						map[string]any{"coin": "USDT", "available": "200", "frozen": "0", "locked": "0"},
						map[string]any{"coin": "DUST", "available": "100", "frozen": "0", "locked": "0"},
					},
				})
			case "/api/v2/spot/market/tickers":
				sawTickers = true
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": "00000",
					"data": []any{
						map[string]any{"symbol": "BTCUSDT", "lastPr": "60000"},
						map[string]any{"symbol": "DUSTUSDT", "lastPr": "0.001"}, // 100 * 0.001 = $0.1 → dust
					},
				})
			default:
				http.NotFound(w, r)
			}
		})
		defer srv.Close()

		c := bitget.New("k", "s", "p", srv.URL, hc)
		snap, err := c.Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(sawTickers).To(BeTrue())
		Expect(assetsHeaders.Get("ACCESS-KEY")).To(Equal("k"))
		Expect(assetsHeaders.Get("ACCESS-PASSPHRASE")).To(Equal("p"))
		Expect(assetsHeaders.Get("ACCESS-SIGN")).NotTo(BeEmpty())
		Expect(assetsHeaders.Get("ACCESS-TIMESTAMP")).NotTo(BeEmpty())
		Expect(assetsHeaders.Get("locale")).To(Equal("en-US"))

		Expect(snap.Connection.BrokerageSlug).To(Equal("bitget"))
		Expect(snap.Holdings).To(HaveLen(1))
		Expect(snap.Holdings[0].Balances[0].Cash).To(Equal(200.0)) // USDT stable cash
		Expect(snap.Holdings[0].Positions).To(HaveLen(1))          // BTC only; DUST filtered
		Expect(snap.Holdings[0].Positions[0].Symbol.Symbol).To(Equal("BTC"))
	})

	It("surfaces API error codes other than 00000", func() {
		srv, hc := newServer(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Path).To(Equal("/api/v2/spot/account/assets"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": "40001", "msg": "bad signature",
			})
		})
		defer srv.Close()
		_, err := bitget.New("k", "s", "p", srv.URL, hc).Fetch(context.Background())
		Expect(err).To(MatchError(ContainSubstring("40001")))
	})

	It("still returns a snapshot when the public tickers call fails (best-effort)", func() {
		srv, hc := newServer(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/spot/account/assets":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": "00000",
					"data": []any{
						map[string]any{"coin": "USDT", "available": "50", "frozen": "0", "locked": "0"},
					},
				})
			case "/api/v2/spot/market/tickers":
				http.Error(w, "boom", http.StatusInternalServerError)
			}
		})
		defer srv.Close()

		snap, err := bitget.New("k", "s", "p", srv.URL, hc).Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Holdings[0].Balances[0].Cash).To(Equal(50.0))
	})
})
