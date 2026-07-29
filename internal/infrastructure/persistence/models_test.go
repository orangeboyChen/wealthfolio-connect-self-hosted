package persistence

import (
	"testing"
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
)

func TestNewMigratorAndModels(t *testing.T) {
	m := NewMigrator()
	models := m.Models()
	if len(models) == 0 {
		t.Fatal("Models() returned no entries; AutoMigrate would skip every aggregate")
	}
	for i, mdl := range models {
		if mdl == nil {
			t.Errorf("models[%d] is nil", i)
		}
	}
}

func TestNonNilJSON(t *testing.T) {
	if string(nonNilJSON(nil)) != "[]" {
		t.Error("nil should map to []")
	}
	if string(nonNilJSON([]byte{})) != "[]" {
		t.Error("empty slice should map to []")
	}
	in := []byte(`{"x":1}`)
	if string(nonNilJSON(in)) != `{"x":1}` {
		t.Error("non-empty input should pass through")
	}
}

func TestOrEmpty(t *testing.T) {
	if got := orEmpty[int](nil); got == nil || len(got) != 0 {
		t.Errorf("nil → empty slice expected, got %v", got)
	}
	in := []string{"a"}
	if got := orEmpty(in); len(got) != 1 || got[0] != "a" {
		t.Errorf("non-nil should pass through, got %v", got)
	}
}

func TestActivityOptionMetadataRoundTrip(t *testing.T) {
	expiration := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	activity := brokerage.Activity{
		ID: "id", SourceRecordID: "snaptrade:id", SourceFingerprint: "fingerprint",
		Type: brokerage.ActivityOptionBuy, TradeDate: time.Now().UTC(),
		CurrencySymbol: &brokerage.Symbol{
			Symbol: "USDC", RawSymbol: "USDC", Name: "USD Coin",
			Type:     brokerage.SymbolType{Code: "CRYPTO", Description: "Cryptocurrency", IsSupported: true},
			Exchange: brokerage.Exchange{Code: "CRYPTO", MICCode: "XXXX", Name: "Crypto", Suffix: "-USD"},
			Currency: brokerage.Currency{Code: "USD", Name: "US Dollar"}, FIGICode: "FIGI-CURRENCY",
		},
		OptionSymbol: &brokerage.OptionSymbol{
			Ticker: "AAPL-C", OptionType: brokerage.OptionCall, StrikePrice: 240,
			ExpirationDate: expiration, IsMiniOption: true,
			Underlying: brokerage.Symbol{
				Symbol: "AAPL", RawSymbol: "AAPL", Description: "Apple",
				Type:     brokerage.SymbolType{Code: "EQUITY", Description: "Equity", IsSupported: true},
				Exchange: brokerage.Exchange{Code: "NASDAQ", MICCode: "XNAS", Name: "Nasdaq"},
				Currency: brokerage.Currency{Code: "USD", Name: "US Dollar"}, FIGICode: "FIGI-AAPL",
			},
		},
	}
	roundTrip := activityFromDomain("account", activity).ToDomain()
	if roundTrip.OptionSymbol == nil || roundTrip.OptionSymbol.Ticker != "AAPL-C" {
		t.Fatalf("option metadata not restored: %#v", roundTrip.OptionSymbol)
	}
	if !roundTrip.OptionSymbol.ExpirationDate.Equal(expiration) || !roundTrip.OptionSymbol.IsMiniOption {
		t.Fatalf("option contract fields not restored: %#v", roundTrip.OptionSymbol)
	}
	if roundTrip.OptionSymbol.Underlying.Exchange.MICCode != "XNAS" || !roundTrip.OptionSymbol.Underlying.Type.IsSupported {
		t.Fatalf("option underlying metadata not restored: %#v", roundTrip.OptionSymbol.Underlying)
	}
	if roundTrip.CurrencySymbol == nil || roundTrip.CurrencySymbol.FIGICode != "FIGI-CURRENCY" {
		t.Fatalf("currency universal symbol not restored: %#v", roundTrip.CurrencySymbol)
	}
	if roundTrip.SourceFingerprint != "fingerprint" {
		t.Fatalf("fingerprint not restored: %q", roundTrip.SourceFingerprint)
	}
}
