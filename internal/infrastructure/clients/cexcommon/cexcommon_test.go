package cexcommon_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/cexcommon"
)

func TestCEXCommon(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cexcommon Suite")
}

var _ = Describe("Translate", func() {
	It("collapses stablecoins into the cash balance", func() {
		snap := cexcommon.Translate("test", "Test", cexcommon.Snapshot{
			Balances: []cexcommon.Balance{
				{Asset: "USDT", Quantity: 100, PriceUSD: 1, USDValue: 100},
				{Asset: "USDC", Quantity: 50, PriceUSD: 1, USDValue: 50},
			},
		})
		Expect(snap.Holdings).To(HaveLen(1))
		Expect(snap.Holdings[0].Balances[0].Cash).To(Equal(150.0))
		Expect(snap.Holdings[0].Positions).To(BeEmpty())
		Expect(snap.Accounts[0].BalanceTotal).To(Equal(150.0))
	})

	It("creates a position per non-stable asset and skips dust under $1", func() {
		snap := cexcommon.Translate("test", "Test", cexcommon.Snapshot{
			Balances: []cexcommon.Balance{
				{Asset: "BTC", Quantity: 0.5, PriceUSD: 60000, USDValue: 30000},
				{Asset: "DUST", Quantity: 1000, PriceUSD: 0.0001, USDValue: 0.1},
			},
		})
		Expect(snap.Holdings[0].Positions).To(HaveLen(1))
		Expect(snap.Holdings[0].Positions[0].Symbol.Symbol).To(Equal("BTC"))
		Expect(snap.Holdings[0].Positions[0].Units).To(Equal(0.5))
	})

	It("translates trades into BUY/SELL activities", func() {
		snap := cexcommon.Translate("test", "Test", cexcommon.Snapshot{
			Trades: []cexcommon.Trade{
				{ID: "t1", Symbol: "BTC-USDT", Side: "buy", Price: 60000, Quantity: 0.1, Timestamp: time.Now()},
				{ID: "t2", Symbol: "BTC-USDT", Side: "SELL", Price: 65000, Quantity: 0.05, Timestamp: time.Now()},
			},
		})
		acts := snap.Activities["test-spot"]
		Expect(acts).To(HaveLen(2))
		Expect(string(acts[0].Type)).To(Equal("BUY"))
		Expect(string(acts[1].Type)).To(Equal("SELL"))
	})

	It("uses stable connection IDs derived from the slug", func() {
		snap := cexcommon.Translate("okx", "OKX", cexcommon.Snapshot{})
		Expect(snap.Connection.ID).To(Equal("okx-conn"))
		Expect(snap.Accounts[0].ID).To(Equal("okx-spot"))
	})
})

var _ = Describe("IsStablecoin", func() {
	It("recognizes common USD-pegged coins regardless of case", func() {
		Expect(cexcommon.IsStablecoin("USDT")).To(BeTrue())
		Expect(cexcommon.IsStablecoin("usdc")).To(BeTrue())
		Expect(cexcommon.IsStablecoin("DAI")).To(BeTrue())
		Expect(cexcommon.IsStablecoin("BTC")).To(BeFalse())
	})
})
