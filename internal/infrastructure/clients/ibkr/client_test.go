package ibkr_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/ibkr"
)

func TestIBKRClient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "IBKR Client Suite")
}

// fakeConnector returns a deterministic RawSnapshot, bypassing the real
// IB Gateway TCP+protobuf transport.
type fakeConnector struct {
	snap ibkr.RawSnapshot
	err  error
}

func (f fakeConnector) Fetch(_ context.Context, _ string, _ int, _ int64, _ string) (ibkr.RawSnapshot, error) {
	return f.snap, f.err
}

var _ = Describe("Client.ID", func() {
	It("returns ibkr", func() {
		Expect(ibkr.New("h", 4001, 1, "", fakeConnector{}).ID()).To(Equal("ibkr"))
	})
})

var _ = Describe("Client.Fetch", func() {
	It("fails fast when host/port is empty", func() {
		_, err := ibkr.New("", 0, 1, "", fakeConnector{}).Fetch(context.Background())
		Expect(err).To(HaveOccurred())
	})

	It("propagates connector failures", func() {
		c := ibkr.New("h", 4001, 1, "", fakeConnector{err: errors.New("nope")})
		_, err := c.Fetch(context.Background())
		Expect(err).To(MatchError(ContainSubstring("nope")))
	})

	It("translates a single live account with one position", func() {
		raw := ibkr.RawSnapshot{
			Accounts: map[string]*ibkr.RawAccount{
				"U1234567": {
					AccountID:      "U1234567",
					NetLiquidation: 100000,
					TotalCash:      25000,
					BuyingPower:    50000,
					Currency:       "USD",
				},
			},
			Positions: []ibkr.RawPosition{
				{Account: "U1234567", Symbol: "AAPL", SecType: "STK", Exchange: "NASDAQ",
					Currency: "USD", Quantity: 10, AvgCost: 150},
			},
		}
		c := ibkr.New("h", 4001, 1, "", fakeConnector{snap: raw})
		snap, err := c.Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Connection.BrokerageSlug).To(Equal("ibkr"))
		Expect(snap.Accounts).To(HaveLen(1))
		Expect(snap.Accounts[0].ID).To(Equal("ibkr-U1234567"))
		Expect(snap.Accounts[0].IsPaper).To(BeFalse())
		Expect(snap.Accounts[0].BalanceTotal).To(Equal(100000.0))
		Expect(snap.Accounts[0].Currency).To(Equal("USD"))
		Expect(snap.Holdings).To(HaveLen(1))
		Expect(snap.Holdings[0].Positions).To(HaveLen(1))
		Expect(snap.Holdings[0].Positions[0].Symbol.Symbol).To(Equal("AAPL"))
		Expect(snap.Holdings[0].Balances[0].Cash).To(Equal(25000.0))
	})

	It("flags paper accounts (DU prefix)", func() {
		raw := ibkr.RawSnapshot{Accounts: map[string]*ibkr.RawAccount{
			"DU111": {AccountID: "DU111", NetLiquidation: 1, Currency: "USD"},
		}}
		snap, err := ibkr.New("h", 4001, 1, "", fakeConnector{snap: raw}).Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Accounts[0].IsPaper).To(BeTrue())
	})

	It("maps option/futures/forex sec types to canonical position types", func() {
		raw := ibkr.RawSnapshot{
			Accounts: map[string]*ibkr.RawAccount{"U1": {AccountID: "U1", Currency: "USD"}},
			Positions: []ibkr.RawPosition{
				{Account: "U1", Symbol: "AAPL", SecType: "OPT", Currency: "USD", Quantity: 1},
				{Account: "U1", Symbol: "ES", SecType: "FUT", Currency: "USD", Quantity: 1},
				{Account: "U1", Symbol: "EUR.USD", SecType: "CASH", Currency: "USD", Quantity: 1},
			},
		}
		snap, err := ibkr.New("h", 4001, 1, "", fakeConnector{snap: raw}).Fetch(context.Background())
		Expect(err).NotTo(HaveOccurred())
		types := make([]string, 0, len(snap.Holdings[0].Positions))
		for _, p := range snap.Holdings[0].Positions {
			types = append(types, p.Symbol.Type.Code)
		}
		Expect(types).To(ConsistOf("OPTION", "FUTURE", "FOREX"))
	})

	It("Translate handles an empty snapshot", func() {
		snap := ibkr.Translate(ibkr.RawSnapshot{})
		Expect(snap.Connection.BrokerageSlug).To(Equal("ibkr"))
		Expect(snap.Accounts).To(BeEmpty())
		Expect(snap.Holdings).To(BeEmpty())
	})
})
