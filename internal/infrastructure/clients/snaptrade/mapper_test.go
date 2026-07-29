package snaptrade

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
)

func activityOfType(activityType string) rawActivity {
	tradeDate := "2024-03-22T16:27:55Z"
	settlement := "2024-03-26"
	return rawActivity{
		ID: "activity-id", Type: activityType, TradeDate: &tradeDate, SettlementDate: &settlement,
		Price: decimal{Value: 10, Valid: true}, Units: decimal{Value: 2, Valid: true},
		Amount: decimal{Value: -20, Valid: true}, Fee: decimal{Value: 1, Valid: true},
		Currency: &rawCurrency{Code: "USD", Name: "US Dollar"}, Institution: "Interactive Brokers LLC",
	}
}

var _ = Describe("SnapTrade mapping", func() {
	DescribeTable("maps common activity types",
		func(rawType string, expected brokerage.ActivityType, review bool) {
			activity, err := mapActivity("snaptrade-account", "remote-account", activityOfType(rawType))
			Expect(err).NotTo(HaveOccurred())
			Expect(activity.Type).To(Equal(expected))
			Expect(activity.RawType).To(Equal(rawType))
			Expect(activity.NeedsReview).To(Equal(review))
			Expect(activity.SourceRecordID).To(Equal("snaptrade:activity-id"))
			Expect(activity.SourceFingerprint).To(HaveLen(64))
		},
		Entry("buy", "BUY", brokerage.ActivityBuy, true),
		Entry("sell", "SELL", brokerage.ActivitySell, true),
		Entry("dividend", "DIVIDEND", brokerage.ActivityDividend, false),
		Entry("substitute dividend", "SUBSTITUTE_DIVIDEND", brokerage.ActivityDividend, false),
		Entry("reinvestment", "REI", brokerage.ActivityBuy, true),
		Entry("interest", "INTEREST", brokerage.ActivityInterest, false),
		Entry("fee", "FEE", brokerage.ActivityFee, false),
		Entry("tax", "TAX", brokerage.ActivityTax, false),
		Entry("contribution", "CONTRIBUTION", brokerage.ActivityDeposit, false),
		Entry("withdrawal", "WITHDRAWAL", brokerage.ActivityWithdrawal, false),
		Entry("transfer in", "TRANSFER_IN", brokerage.ActivityTransferIn, false),
		Entry("transfer out", "TRANSFER_OUT", brokerage.ActivityTransferOut, false),
		Entry("conversion", "CONVERSION", brokerage.ActivityConversion, false),
		Entry("split", "SPLIT", brokerage.ActivitySplit, false),
		Entry("expiration", "OPTIONEXPIRATION", brokerage.ActivityOptionExpiry, false),
		Entry("assignment", "OPTIONASSIGNMENT", brokerage.ActivityOptionAssignment, false),
		Entry("exercise", "OPTIONEXERCISE", brokerage.ActivityOptionExercise, false),
		Entry("unknown", "MYSTERY_CASH", brokerage.ActivityUnknown, true),
	)

	It("preserves equity, currency-symbol, option, FX, grouping, and review metadata", func() {
		raw := activityOfType("BUY")
		raw.ExternalReferenceID = "broker-ref"
		raw.FXRate = decimal{Value: 1.25, Valid: true}
		raw.Symbol = &rawUniversalSymbol{
			Symbol: "AAPL", RawSymbol: "AAPL", Description: "Apple", Currency: rawCurrency{Code: "USD"},
			Exchange: rawExchange{Code: "NASDAQ", MICCode: "XNAS"}, Type: rawSecurityType{Code: "cs", Description: "Common Stock"},
		}
		raw.CurrencyUniversalSymbol = &rawUniversalSymbol{Symbol: "USDC", Type: rawSecurityType{Code: "crypto"}}
		raw.OptionSymbol = &rawOptionSymbol{
			Ticker: "AAPL  261218C00240000", OptionType: "CALL", StrikePrice: decimal{Value: 240, Valid: true},
			ExpirationDate: "2026-12-18", UnderlyingSymbol: raw.Symbol,
		}
		activity, err := mapActivity("snaptrade-account", "remote-account", raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(activity.Type).To(Equal(brokerage.ActivityOptionBuy))
		Expect(activity.Symbol.Exchange.MICCode).To(Equal("XNAS"))
		Expect(activity.CurrencySymbol.Symbol).To(Equal("USDC"))
		Expect(activity.OptionSymbol.Ticker).To(Equal("AAPL  261218C00240000"))
		Expect(activity.OptionSymbol.ExpirationDate).To(Equal(time.Date(2026, 12, 18, 0, 0, 0, 0, time.UTC)))
		Expect(*activity.FxRate).To(Equal(1.25))
		Expect(activity.SourceGroupID).To(Equal("snaptrade:external:broker-ref"))
		Expect(activity.NeedsReview).To(BeFalse())
	})

	It("uses a fingerprint fallback when SnapTrade omits an ID", func() {
		raw := activityOfType("DIVIDEND")
		raw.ID = ""
		one, err := mapActivity("snaptrade-a", "remote-a", raw)
		Expect(err).NotTo(HaveOccurred())
		two, err := mapActivity("snaptrade-a", "remote-a", raw)
		Expect(err).NotTo(HaveOccurred())
		otherAccount, err := mapActivity("snaptrade-b", "remote-b", raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(one.SourceRecordID).To(Equal(two.SourceRecordID))
		Expect(one.SourceFingerprint).NotTo(Equal(otherAccount.SourceFingerprint))
		Expect(one.NeedsReview).To(BeTrue())
	})

	It("rejects only the malformed activity with no trade date", func() {
		raw := activityOfType("DIVIDEND")
		raw.TradeDate = nil
		_, err := mapActivity("account", "remote", raw)
		Expect(err).To(MatchError(ContainSubstring("trade_date")))
	})

	It("maps multiple currencies and discriminated position kinds", func() {
		balances := mapBalances([]rawBalance{
			{Currency: rawCurrency{Code: "USD"}, Cash: decimal{Value: 1, Valid: true}},
			{Currency: rawCurrency{Code: "EUR"}, Cash: decimal{Value: 2, Valid: true}},
		})
		Expect(balances).To(HaveLen(2))
		positions, options := mapPositions([]rawPosition{
			{Instrument: rawInstrument{Kind: "etf", Symbol: "SPY", Currency: "USD"}, Units: decimal{Value: 1, Valid: true}},
			{Instrument: rawInstrument{Kind: "option", Ticker: "SPY-C", OptionType: "CALL", ExpirationDate: "2026-01-01"}, Units: decimal{Value: 2, Valid: true}},
		})
		Expect(positions).To(HaveLen(1))
		Expect(positions[0].Symbol.Type.Code).To(Equal("ETF"))
		Expect(options).To(HaveLen(1))
		Expect(options[0].OptionSymbol.OptionType).To(Equal(brokerage.OptionCall))
	})

	It("uses structured brokerage matching before display fallback", func() {
		Expect(isInteractiveBrokers(rawBrokerage{Slug: "INTERACTIVE_BROKERS"})).To(BeTrue())
		Expect(isInteractiveBrokers(rawBrokerage{Name: "Interactive Brokers LLC"})).To(BeTrue())
		Expect(isInteractiveBrokers(rawBrokerage{Slug: "FIDELITY", DisplayName: "Not IBKR"})).To(BeFalse())
	})

	DescribeTable("maps account categories",
		func(rawType, category string, expected brokerage.AccountType) {
			Expect(mapAccountType(rawType, category)).To(Equal(expected))
		},
		Entry("margin", "MARGIN", "", brokerage.AccountTypeMargin),
		Entry("cash", "", "cash", brokerage.AccountTypeCash),
		Entry("deposit", "", "deposit", brokerage.AccountTypeCash),
		Entry("securities fallback", "TFSA", "registered", brokerage.AccountTypeSecurities),
	)

	DescribeTable("maps instrument kinds",
		func(kind, expected string, supported bool) {
			code, _, actualSupported := mapInstrumentKind(kind)
			Expect(code).To(Equal(expected))
			Expect(actualSupported).To(Equal(supported))
		},
		Entry("stock", "stock", "EQUITY", true),
		Entry("ETF", "etf", "ETF", true),
		Entry("mutual fund", "mutualfund", "MUTUAL_FUND", true),
		Entry("closed end fund", "cef", "CLOSED_END_FUND", true),
		Entry("ADR", "adr", "ADR", true),
		Entry("crypto", "crypto", "CRYPTO", true),
		Entry("future", "future", "FUTURE", true),
		Entry("bond", "bond", "BOND", true),
		Entry("cash", "cash", "CASH", true),
		Entry("forex", "forex", "FOREX", true),
		Entry("CFD requires review", "cfd", "CFD", false),
		Entry("unknown", "structured_note", "STRUCTURED_NOTE", false),
	)

	DescribeTable("maps universal security types",
		func(input, expected string, supported bool) {
			code, _, actualSupported := mapSecurityType(input, "description")
			Expect(code).To(Equal(expected))
			Expect(actualSupported).To(Equal(supported))
		},
		Entry("common stock", "cs", "EQUITY", true),
		Entry("ETF", "et", "ETF", true),
		Entry("open-end fund", "oef", "FUND", true),
		Entry("bond", "bnd", "BOND", true),
		Entry("crypto", "crypto", "CRYPTO", true),
		Entry("cash", "cash", "CASH", true),
		Entry("unknown", "other", "OTHER", false),
	)

	It("maps disconnected connections and incomplete account metadata safely", func() {
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		connection := mapConnection(rawConnection{
			ID: "auth", Disabled: true, UpdatedDate: "invalid",
			Brokerage: rawBrokerage{Name: "Interactive Brokers"},
		}, now)
		Expect(connection.Status).To(Equal(brokerage.ConnectionDisconnected))
		Expect(connection.UpdatedAt).To(Equal(now))
		account := mapAccount(rawAccount{ID: "account", RawType: "CASH"}, rawConnection{
			Disabled: true, Brokerage: rawBrokerage{Name: "Interactive Brokers"},
		}, now)
		Expect(account.Name).To(Equal("Interactive Brokers"))
		Expect(account.Status).To(Equal("disconnected"))
		Expect(account.CreatedDate).To(Equal(now))
	})

	It("marks malformed optional metadata for review without dropping the activity", func() {
		raw := activityOfType("TRANSFER")
		raw.Institution = ""
		raw.Currency = nil
		raw.SettlementDate = stringPointer("not-a-date")
		raw.Amount = decimal{Value: -10, Valid: true}
		raw.Units = decimal{}
		activity, err := mapActivity("account", "remote", raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(activity.Type).To(Equal(brokerage.ActivityTransferOut))
		Expect(activity.Institution).To(Equal("Interactive Brokers"))
		Expect(activity.NeedsReview).To(BeTrue())
		Expect(activity.SettlementDate).To(BeNil())
		Expect(mapActivityError("a-very-long-account", 3, errors.New("bad row")).Error()).To(ContainSubstring("a-ve…ount"))
	})
})

func stringPointer(value string) *string { return &value }
