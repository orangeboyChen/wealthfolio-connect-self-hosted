package hyperliquid_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/hyperliquid"
)

func TestHyperliquid(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Hyperliquid Client Suite")
}

func newServer(handler http.HandlerFunc) (*httptest.Server, *http.Client) {
	srv := httptest.NewServer(handler)
	return srv, srv.Client()
}

var _ = Describe("Hyperliquid Client", func() {
	It("returns slug hyperliquid", func() {
		Expect(hyperliquid.New("0xabc", "", nil).ID()).To(Equal("hyperliquid"))
	})

	It("fails when wallet address is empty", func() {
		_, err := hyperliquid.New("", "http://x", nil).Fetch(context.Background())
		Expect(err).To(HaveOccurred())
	})

	It("merges spot balances and perp account value into a single snapshot", func() {
		srv, hc := newServer(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Method).To(Equal(http.MethodPost))
			Expect(r.URL.Path).To(Equal("/info"))
			var body struct {
				Type string `json:"type"`
				User string `json:"user"`
			}
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			Expect(body.User).To(Equal("0xabc"))
			switch body.Type {
			case "spotClearinghouseState":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"balances": []any{
						map[string]any{"coin": "USDC", "total": "100", "entryNtl": "100"},
						map[string]any{"coin": "PURR", "total": "10", "entryNtl": "20"},
						map[string]any{"coin": "ZERO", "total": "0", "entryNtl": "0"},
					},
				})
			case "clearinghouseState":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"marginSummary": map[string]any{"accountValue": "500"},
				})
			default:
				http.NotFound(w, r)
			}
		})
		defer srv.Close()

		snap, err := hyperliquid.New("0xabc", srv.URL, hc).Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Connection.BrokerageSlug).To(Equal("hyperliquid"))
		// USDC spot ($100) + USDC perp synthetic ($500) → cash 600
		Expect(snap.Holdings[0].Balances[0].Cash).To(Equal(600.0))
		// PURR is the only non-stable position; price = 20/10 = 2, value = 20 → above $1 dust
		Expect(snap.Holdings[0].Positions).To(HaveLen(1))
		Expect(snap.Holdings[0].Positions[0].Symbol.Symbol).To(Equal("PURR"))
	})

	It("still returns spot data when the perp endpoint fails", func() {
		srv, hc := newServer(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Type string `json:"type"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Type == "clearinghouseState" {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"balances": []any{
					map[string]any{"coin": "USDC", "total": "42", "entryNtl": "42"},
				},
			})
		})
		defer srv.Close()

		snap, err := hyperliquid.New("0xabc", srv.URL, hc).Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Holdings[0].Balances[0].Cash).To(Equal(42.0))
	})

	It("fails when both spot and perp calls fail", func() {
		srv, hc := newServer(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		})
		defer srv.Close()

		_, err := hyperliquid.New("0xabc", srv.URL, hc).Fetch(context.Background())
		Expect(err).To(HaveOccurred())
	})
})
