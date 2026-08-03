package snaptrade

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
)

var splitRatioPattern = regexp.MustCompile(`(?i)\b([0-9]+(?:\.[0-9]+)?)\s*(?:FOR|:)\s*([0-9]+(?:\.[0-9]+)?)\b`)

func isInteractiveBrokers(broker rawBrokerage) bool {
	for _, value := range []string{broker.Slug, broker.Name, broker.DisplayName} {
		normalized := normalizeInstitution(value)
		if normalized == "ibkr" || normalized == "interactivebrokers" ||
			normalized == "interactivebrokersllc" || strings.HasPrefix(normalized, "interactivebrokers") {
			return true
		}
	}
	return false
}

func isInteractiveBrokersName(value string) bool {
	normalized := normalizeInstitution(value)
	return normalized == "ibkr" || normalized == "interactivebrokers" ||
		normalized == "interactivebrokersllc" || strings.HasPrefix(normalized, "interactivebrokers")
}

func normalizeInstitution(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func mapConnection(raw rawConnection, now time.Time) brokerage.Connection {
	status := brokerage.ConnectionActive
	if raw.Disabled {
		status = brokerage.ConnectionDisconnected
	}
	updated := parseOptionalTime(raw.UpdatedDate)
	if updated.IsZero() {
		updated = now
	}
	name := raw.Brokerage.DisplayName
	if name == "" {
		name = raw.Brokerage.Name
	}
	return brokerage.Connection{
		ID: "snaptrade-conn-" + raw.ID, AuthorizationID: "snaptrade-auth-" + raw.ID,
		BrokerageName: raw.Brokerage.Name, BrokerageSlug: "snaptrade-" + strings.ToLower(raw.Brokerage.Slug),
		DisplayName: name, LogoURL: raw.Brokerage.LogoURL, SquareLogoURL: raw.Brokerage.SquareLogoURL,
		Disabled: raw.Disabled, Name: raw.Name, Status: status, UpdatedAt: updated,
	}
}

func mapAccount(raw rawAccount, connection rawConnection, now time.Time) brokerage.Account {
	institution := raw.InstitutionName
	if institution == "" {
		institution = connection.Brokerage.Name
	}
	name := raw.Name
	if name == "" {
		name = institution
	}
	currency := ""
	balance := 0.0
	if raw.Balance.Total != nil {
		currency = strings.ToUpper(raw.Balance.Total.Currency)
		balance = raw.Balance.Total.Amount.Value
	}
	created := parseOptionalTime(raw.CreatedDate)
	if created.IsZero() {
		created = now
	}
	status := raw.Status
	if status == "" {
		status = "open"
	}
	if connection.Disabled {
		status = "disconnected"
	}
	return brokerage.Account{
		ID: "snaptrade-" + raw.ID, Name: name, AccountNumber: raw.Number,
		Type: mapAccountType(raw.RawType, raw.AccountCategory), RawType: raw.RawType,
		Currency: currency, BalanceTotal: balance, BalanceCurrency: currency,
		BrokerageAuthorization: "snaptrade-auth-" + raw.BrokerageAuthorization,
		InstitutionName:        institution, SyncEnabled: true, IsPaper: raw.IsPaper,
		Status: status, CreatedDate: created,
	}
}

func mapAccountType(rawType, category string) brokerage.AccountType {
	value := strings.ToUpper(rawType + " " + category)
	switch {
	case strings.Contains(value, "MARGIN"):
		return brokerage.AccountTypeMargin
	case strings.Contains(value, "CASH") || strings.Contains(value, "DEPOSIT"):
		return brokerage.AccountTypeCash
	default:
		return brokerage.AccountTypeSecurities
	}
}

func mapBalances(raw []rawBalance) []brokerage.Balance {
	out := make([]brokerage.Balance, 0, len(raw))
	for _, balance := range raw {
		out = append(out, brokerage.Balance{
			Currency: brokerage.Currency{Code: strings.ToUpper(balance.Currency.Code), Name: balance.Currency.Name},
			Cash:     balance.Cash.Value, BuyingPower: balance.BuyingPower.Value,
		})
	}
	return out
}

func mapPositions(raw []rawPosition) ([]brokerage.Position, []brokerage.OptionPosition) {
	positions := make([]brokerage.Position, 0, len(raw))
	options := make([]brokerage.OptionPosition, 0)
	for _, position := range raw {
		if !position.Units.Valid || position.Units.Value == 0 {
			continue
		}
		currency := position.Currency
		if currency == "" {
			currency = position.Instrument.Currency
		}
		if strings.EqualFold(position.Instrument.Kind, "option") {
			options = append(options, brokerage.OptionPosition{
				OptionSymbol: mapPositionOption(position.Instrument), Units: position.Units.Value,
				Price: position.Price.Value, AveragePurchasePrice: position.CostBasis.Value,
				Currency: brokerage.Currency{Code: strings.ToUpper(currency)},
			})
			continue
		}
		positions = append(positions, brokerage.Position{
			Symbol: mapInstrument(position.Instrument), Units: position.Units.Value,
			Price: position.Price.Value, AveragePurchasePrice: position.CostBasis.Value,
			Currency:       brokerage.Currency{Code: strings.ToUpper(currency)},
			CashEquivalent: position.CashEquivalent,
		})
	}
	return positions, options
}

func mapInstrument(raw rawInstrument) brokerage.Symbol {
	code, description, supported := mapInstrumentKind(raw.Kind)
	figi := ""
	if raw.FIGIInstrument != nil {
		figi = raw.FIGIInstrument.FIGICode
	}
	return brokerage.Symbol{
		Symbol: raw.Symbol, RawSymbol: raw.RawSymbol, Name: raw.Symbol, Description: raw.Description,
		Type:     brokerage.SymbolType{Code: code, Description: description, IsSupported: supported},
		Exchange: brokerage.Exchange{Code: raw.Exchange, MICCode: raw.Exchange},
		Currency: brokerage.Currency{Code: strings.ToUpper(raw.Currency)}, FIGICode: figi,
	}
}

func mapInstrumentKind(kind string) (code, description string, supported bool) {
	switch strings.ToLower(kind) {
	case "stock":
		return "EQUITY", "Equity", true
	case "etf":
		return "ETF", "Exchange-traded fund", true
	case "mutualfund":
		return "MUTUAL_FUND", "Mutual fund", true
	case "cef":
		return "CLOSED_END_FUND", "Closed-end fund", true
	case "adr":
		return "ADR", "American depositary receipt", true
	case "crypto":
		return "CRYPTO", "Cryptocurrency", true
	case "future":
		return "FUTURE", "Future", true
	case "bond":
		return "BOND", "Bond", true
	case instrumentKindCash, "forex":
		return strings.ToUpper(kind), strings.ToUpper(kind), true
	case "cfd":
		return "CFD", "Contract for difference", false
	default:
		return strings.ToUpper(kind), kind, false
	}
}

func mapPositionOption(raw rawInstrument) brokerage.OptionSymbol {
	ticker := raw.Ticker
	if ticker == "" {
		ticker = raw.Symbol
	}
	underlying := brokerage.Symbol{}
	if raw.UnderlyingSymbol != nil {
		underlying = mapSymbol(*raw.UnderlyingSymbol)
	}
	return brokerage.OptionSymbol{
		Ticker: ticker, OptionType: brokerage.OptionSide(strings.ToUpper(raw.OptionType)),
		StrikePrice: raw.StrikePrice.Value, ExpirationDate: parseOptionalTime(raw.ExpirationDate),
		IsMiniOption: raw.IsMiniOption, Underlying: underlying,
	}
}

func mapSymbol(raw rawUniversalSymbol) brokerage.Symbol {
	code, description, supported := mapSecurityType(raw.Type.Code, raw.Type.Description)
	figi := raw.FIGICode
	if figi == "" && raw.FIGIInstrument != nil {
		figi = raw.FIGIInstrument.FIGICode
	}
	return brokerage.Symbol{
		Symbol: raw.Symbol, RawSymbol: raw.RawSymbol, Description: raw.Description, Name: raw.Description,
		Type:     brokerage.SymbolType{Code: code, Description: description, IsSupported: supported},
		Exchange: brokerage.Exchange{Code: raw.Exchange.Code, MICCode: raw.Exchange.MICCode, Name: raw.Exchange.Name, Suffix: raw.Exchange.Suffix},
		Currency: brokerage.Currency{Code: strings.ToUpper(raw.Currency.Code), Name: raw.Currency.Name}, FIGICode: figi,
	}
}

func mapSecurityType(code, description string) (mappedCode, mappedDescription string, supported bool) {
	switch strings.ToLower(code) {
	case "cs", "ps", "wi", "wt", "rt", "ut":
		return "EQUITY", description, true
	case "et":
		return "ETF", description, true
	case "oef", "cef":
		return "FUND", description, true
	case "bnd":
		return "BOND", description, true
	case "crypto":
		return "CRYPTO", description, true
	case instrumentKindCash, "forex", "future":
		return strings.ToUpper(code), description, true
	default:
		return strings.ToUpper(code), description, false
	}
}

func mapOption(raw rawOptionSymbol) brokerage.OptionSymbol {
	underlying := brokerage.Symbol{}
	if raw.UnderlyingSymbol != nil {
		underlying = mapSymbol(*raw.UnderlyingSymbol)
	}
	return brokerage.OptionSymbol{
		Ticker: raw.Ticker, OptionType: brokerage.OptionSide(strings.ToUpper(raw.OptionType)),
		StrikePrice: raw.StrikePrice.Value, ExpirationDate: parseOptionalTime(raw.ExpirationDate),
		IsMiniOption: raw.IsMiniOption, Underlying: underlying,
	}
}

func mapActivity(accountID, rawAccountID string, raw rawActivity) (brokerage.Activity, error) {
	if raw.TradeDate == nil || strings.TrimSpace(*raw.TradeDate) == "" {
		return brokerage.Activity{}, errors.New("activity has no trade_date")
	}
	tradeDate := parseOptionalTime(*raw.TradeDate)
	if tradeDate.IsZero() {
		return brokerage.Activity{}, errors.New("activity has an invalid trade_date")
	}
	activityType, subtype, needsReview := mapActivityType(raw.Type, raw.OptionSymbol != nil, raw.Amount.Value, raw.Units.Value)
	a := brokerage.Activity{
		AccountID: accountID, Price: raw.Price.Value, Units: raw.Units.Value, Amount: raw.Amount.Value,
		Type: activityType, Subtype: subtype, RawType: strings.ToUpper(raw.Type), OptionType: raw.OptionType,
		Description: raw.Description, TradeDate: tradeDate, Fee: raw.Fee.Value,
		Institution: raw.Institution, ExternalReferenceID: raw.ExternalReferenceID,
		ProviderType: "snaptrade", SourceSystem: "snaptrade", NeedsReview: needsReview,
	}
	if a.Institution == "" {
		a.Institution = ibkrInstitutionName
	}
	if raw.Currency != nil {
		a.Currency = brokerage.Currency{Code: strings.ToUpper(raw.Currency.Code), Name: raw.Currency.Name}
	} else if raw.Amount.Valid || raw.Price.Valid || raw.Fee.Valid {
		a.NeedsReview = true
	}
	if raw.Symbol != nil {
		symbol := mapSymbol(*raw.Symbol)
		a.Symbol = &symbol
		if !symbol.Type.IsSupported {
			a.NeedsReview = true
		}
	}
	if raw.CurrencyUniversalSymbol != nil {
		symbol := mapSymbol(*raw.CurrencyUniversalSymbol)
		a.CurrencySymbol = &symbol
	}
	if raw.OptionSymbol != nil {
		option := mapOption(*raw.OptionSymbol)
		a.OptionSymbol = &option
	}
	if raw.SettlementDate != nil && strings.TrimSpace(*raw.SettlementDate) != "" {
		if settlement := parseOptionalTime(*raw.SettlementDate); !settlement.IsZero() {
			a.SettlementDate = &settlement
		} else {
			a.NeedsReview = true
		}
	}
	if raw.FXRate.Valid {
		value := raw.FXRate.Value
		a.FxRate = &value
	}
	if a.Type == brokerage.ActivitySplit {
		if ratio, ok := mapSplitRatio(raw); ok {
			a.Amount = ratio
		} else {
			a.NeedsReview = true
		}
	}
	if (a.Type == brokerage.ActivityBuy || a.Type == brokerage.ActivitySell ||
		a.Type == brokerage.ActivityOptionBuy || a.Type == brokerage.ActivityOptionSell) &&
		a.Symbol == nil && a.OptionSymbol == nil {
		a.NeedsReview = true
	}

	a.SourceFingerprint = activityFingerprint(rawAccountID, raw)
	if raw.ID != "" {
		a.SourceRecordID = "snaptrade:" + raw.ID
		a.ID = "snaptrade-activity-" + stableHash(rawAccountID+"\x00"+raw.ID)
	} else {
		a.SourceRecordID = "snaptrade:fingerprint:" + a.SourceFingerprint
		a.ID = "snaptrade-activity-" + a.SourceFingerprint
		a.NeedsReview = true
	}
	if raw.ExternalReferenceID != "" {
		a.SourceGroupID = "snaptrade:external:" + raw.ExternalReferenceID
	}
	return a, nil
}

func mapSplitRatio(raw rawActivity) (float64, bool) {
	match := splitRatioPattern.FindStringSubmatch(raw.Description)
	if len(match) == 3 {
		numerator, numeratorErr := strconv.ParseFloat(match[1], 64)
		denominator, denominatorErr := strconv.ParseFloat(match[2], 64)
		if numeratorErr == nil && denominatorErr == nil && numerator > 0 && denominator > 0 {
			return numerator / denominator, true
		}
	}
	return 0, false
}

func mapActivityType(rawType string, option bool, amount, units float64) (brokerage.ActivityType, string, bool) {
	t := strings.ToUpper(strings.TrimSpace(rawType))
	switch t {
	case "BUY":
		if option {
			return brokerage.ActivityOptionBuy, "", false
		}
		return brokerage.ActivityBuy, "", false
	case "SELL":
		if option {
			return brokerage.ActivityOptionSell, "", false
		}
		return brokerage.ActivitySell, "", false
	case activityTypeDividend:
		return brokerage.ActivityDividend, "", false
	case "SUBSTITUTE_DIVIDEND", "STOCK_DIVIDEND":
		return brokerage.ActivityDividend, t, false
	case "REI":
		return brokerage.ActivityBuy, "DIVIDEND_REINVESTMENT", false
	case "CONTRIBUTION", "DEPOSIT":
		return brokerage.ActivityDeposit, t, false
	case "WITHDRAWAL":
		return brokerage.ActivityWithdrawal, "", false
	case "INTEREST":
		return brokerage.ActivityInterest, "", false
	case "FEE":
		return brokerage.ActivityFee, "", false
	case "TAX":
		return brokerage.ActivityTax, "", false
	case "SPLIT":
		return brokerage.ActivitySplit, "", false
	case "CONVERSION":
		return brokerage.ActivityConversion, "", false
	case "TRANSFER_IN", "EXTERNAL_ASSET_TRANSFER_IN":
		return brokerage.ActivityTransferIn, t, false
	case "TRANSFER_OUT", "EXTERNAL_ASSET_TRANSFER_OUT":
		return brokerage.ActivityTransferOut, t, false
	case "TRANSFER":
		if units < 0 || (units == 0 && amount < 0) {
			return brokerage.ActivityTransferOut, t, true
		}
		return brokerage.ActivityTransferIn, t, true
	case "OPTIONEXPIRATION":
		return brokerage.ActivityOptionExpiry, "", false
	case "OPTIONASSIGNMENT":
		return brokerage.ActivityOptionAssignment, "", false
	case "OPTIONEXERCISE":
		return brokerage.ActivityOptionExercise, "", false
	default:
		return brokerage.ActivityUnknown, t, true
	}
}

func activityFingerprint(accountID string, raw rawActivity) string {
	instrument := ""
	if raw.OptionSymbol != nil {
		instrument = raw.OptionSymbol.Ticker
	} else if raw.Symbol != nil {
		instrument = raw.Symbol.Symbol
	}
	tradeDate, settlementDate, currency := "", "", ""
	if raw.TradeDate != nil {
		tradeDate = *raw.TradeDate
	}
	if raw.SettlementDate != nil {
		settlementDate = *raw.SettlementDate
	}
	if raw.Currency != nil {
		currency = raw.Currency.Code
	}
	payload := struct {
		Account, External, Type, Trade, Settlement, Instrument, Currency string
		Units, Price, Amount, Fee                                        float64
	}{
		Account: accountID, External: raw.ExternalReferenceID, Type: strings.ToUpper(raw.Type),
		Trade: tradeDate, Settlement: settlementDate, Instrument: instrument, Currency: strings.ToUpper(currency),
		Units: raw.Units.Value, Price: raw.Price.Value, Amount: raw.Amount.Value, Fee: raw.Fee.Value,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return stableHash(fmt.Sprintf("%#v", payload))
	}
	return stableHash(string(encoded))
}

func stableHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func parseOptionalTime(value string) time.Time {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339, "2006-01-02", "2006-01-02 15:04:05.999999-07:00",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func mapActivityError(accountID string, index int, err error) error {
	return fmt.Errorf("map SnapTrade activity for %s at page index %d: %w", maskIdentifier(accountID), index, err)
}
