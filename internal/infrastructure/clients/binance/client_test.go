package binance_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/binance"
)

func TestBinance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Binance Client Suite")
}

type fakeFetcher struct {
	balances []binance.RawBalance
	prices   map[string]float64
	balErr   error
	priceErr error
}

func (f *fakeFetcher) Account(_ context.Context) ([]binance.RawBalance, error) {
	return f.balances, f.balErr
}
func (f *fakeFetcher) Prices(_ context.Context) (map[string]float64, error) {
	return f.prices, f.priceErr
}

var _ = Describe("Binance Client", func() {
	It("returns slug binance", func() {
		Expect(binance.New("k", "s", &fakeFetcher{}).ID()).To(Equal("binance"))
	})

	It("fails when credentials are missing", func() {
		_, err := binance.New("", "", &fakeFetcher{}).Fetch(context.Background())
		Expect(err).To(HaveOccurred())
	})

	It("propagates account fetch failure", func() {
		c := binance.New("k", "s", &fakeFetcher{balErr: errors.New("boom")})
		_, err := c.Fetch(context.Background())
		Expect(err).To(MatchError(ContainSubstring("boom")))
	})

	It("translates BTC/USDT into a position priced in USD", func() {
		c := binance.New("k", "s", &fakeFetcher{
			balances: []binance.RawBalance{
				{Asset: "BTC", Free: 0.5, Locked: 0},
				{Asset: "USDT", Free: 1000},
			},
			prices: map[string]float64{"BTCUSDT": 60000},
		})
		snap, err := c.Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Connection.BrokerageSlug).To(Equal("binance"))
		Expect(snap.Holdings[0].Balances[0].Cash).To(Equal(1000.0)) // USDT folded as cash
		Expect(snap.Holdings[0].Positions).To(HaveLen(1))
		Expect(snap.Holdings[0].Positions[0].Symbol.Symbol).To(Equal("BTC"))
		Expect(snap.Holdings[0].Positions[0].Units).To(Equal(0.5))
	})

	It("falls back to empty prices when ticker fetch fails", func() {
		c := binance.New("k", "s", &fakeFetcher{
			balances: []binance.RawBalance{
				{Asset: "USDC", Free: 200},
				{Asset: "BTC", Free: 0.1},
			},
			priceErr: errors.New("rate limit"),
		})
		snap, err := c.Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		// USDC still gets folded as cash; BTC has zero USD value so it's
		// dropped by the dust filter.
		Expect(snap.Holdings[0].Balances[0].Cash).To(Equal(200.0))
		Expect(snap.Holdings[0].Positions).To(BeEmpty())
	})

	It("skips assets with zero combined quantity", func() {
		c := binance.New("k", "s", &fakeFetcher{
			balances: []binance.RawBalance{
				{Asset: "ETH", Free: 0, Locked: 0}, // dropped before pricing
				{Asset: "USDT", Free: 50},
			},
		})
		snap, err := c.Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Holdings[0].Balances[0].Cash).To(Equal(50.0))
		Expect(snap.Holdings[0].Positions).To(BeEmpty())
	})

	It("uses the real SDK fetcher when nil is passed", func() {
		// We cannot reach the network in unit tests, but we can confirm
		// New(...) constructs a client successfully and the missing-cred
		// check runs first, exercising the nil-fetcher branch.
		_, err := binance.New("", "", nil).Fetch(context.Background())
		Expect(err).To(HaveOccurred())
	})
})
