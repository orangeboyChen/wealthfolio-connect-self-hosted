package ibkr

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
)

const (
	flexInstitutionName = "Interactive Brokers"
	flexLogoURL         = "https://passiv-brokerage-logos.s3.ca-central-1.amazonaws.com/interactive-brokers-logo.png"
	flexSquareLogoURL   = "https://passiv-brokerage-logos.s3.ca-central-1.amazonaws.com/interactive-brokers-logo-square.png"
)

const (
	ibkrAssetClassCash         = "CASH"
	ibkrAssetClassBond         = "BOND"
	flexAssetClassOption       = "OPT"
	flexAssetClassFutureOption = "FOP"
	flexKindSecurityInfo       = "securityinfo"
	flexSubtypeFXTrade         = "FXEXCHANGE"
)

var flexSplitPattern = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*(?:FOR|:|/)\s*([0-9]+(?:\.[0-9]+)?)`)

var flexExchangeMICs = map[string]string{
	"BVB":    "XBSE",
	"IBIS":   "XETR",
	"IBIS2":  "XETR",
	"NASDAQ": "XNAS",
	"NYSE":   "XNYS",
	"XETRA":  "XETR",
}

type flexRateKey struct {
	Date     string
	Currency string
}

type flexMappingResult struct {
	Activities []brokerage.Activity
	Skipped    int
	Review     int
}

type flexCommissionPlan struct {
	Currency string
	Amount   float64
}

type flexCommissionCandidate struct {
	Fingerprint      string
	DeclaredCurrency string
	OutCurrency      string
	DeclaredAmount   float64
	OutAmount        float64
	UseOutCurrency   bool
}

func mapFlexReport(report flexReport, remoteAccountID, baseCurrency string) flexMappingResult {
	securities := make(map[string]map[string]string)
	rates := make(map[flexRateKey]float64)
	for _, record := range report.Records {
		switch strings.ToLower(record.Kind) {
		case flexKindSecurityInfo:
			if conid := flexAttr(record.Attrs, "conid", "conid1"); conid != "" {
				securities[conid] = record.Attrs
			}
		case "conversionrate":
			indexFlexRate(rates, record.Attrs, baseCurrency)
		}
	}
	commissionPlans := planFlexCommissions(report, rates, remoteAccountID, baseCurrency)

	localAccountID := "ibkr-" + remoteAccountID
	result := flexMappingResult{}
	for _, record := range report.Records {
		kind := strings.ToLower(record.Kind)
		if isFlexSnapshotRecord(kind) || kind == flexKindSecurityInfo || kind == "conversionrate" {
			continue
		}
		if accountID := flexAttr(record.Attrs, "accountid", "account"); accountID != "" && accountID != remoteAccountID {
			continue
		}
		activity, err := mapFlexRecord(localAccountID, remoteAccountID, record, securities)
		if err != nil {
			result.Skipped++
			continue
		}
		activities := []brokerage.Activity{activity}
		if activity.Type == brokerage.ActivityConversion {
			activities = expandFlexConversionWithCommission(activity, record, commissionPlans[activity.SourceFingerprint])
		}
		for _, mapped := range activities {
			switch {
			case isFlexNativeCurrencyTrade(mapped.Type):
				// IBKR's fxRateToBase and CurrencyConversionRates fields are
				// statement-translation rates. They do not mean that IBKR
				// converted the cash leg of a security trade into the account
				// base currency. Wealthfolio gives fx_rate that latter meaning,
				// so forwarding the reporting rate would debit non-base BUYs
				// from base-currency cash instead of their native cash balance.
				mapped.FxRate = nil
			case mapped.FxRate == nil && mapped.Currency.Code != "" && !strings.EqualFold(mapped.Currency.Code, baseCurrency):
				date := mapped.TradeDate.UTC().Format("2006-01-02")
				if rate, ok := rates[flexRateKey{Date: date, Currency: strings.ToUpper(mapped.Currency.Code)}]; ok {
					mapped.FxRate = &rate
				}
			case mapped.FxRate == nil && strings.EqualFold(mapped.Currency.Code, baseCurrency):
				rate := 1.0
				mapped.FxRate = &rate
			}
			if mapped.NeedsReview {
				result.Review++
			}
			result.Activities = append(result.Activities, mapped)
		}
	}
	sort.SliceStable(result.Activities, func(i, j int) bool {
		if result.Activities[i].TradeDate.Equal(result.Activities[j].TradeDate) {
			return result.Activities[i].SourceRecordID < result.Activities[j].SourceRecordID
		}
		return result.Activities[i].TradeDate.Before(result.Activities[j].TradeDate)
	})
	return result
}

func isFlexNativeCurrencyTrade(activityType brokerage.ActivityType) bool {
	switch activityType {
	case brokerage.ActivityBuy, brokerage.ActivitySell,
		brokerage.ActivityOptionBuy, brokerage.ActivityOptionSell:
		return true
	default:
		return false
	}
}

func expandFlexConversion(activity brokerage.Activity, record flexRecord) []brokerage.Activity {
	return expandFlexConversionWithCommission(activity, record, flexCommissionPlan{})
}

func expandFlexConversionWithCommission(
	activity brokerage.Activity,
	record flexRecord,
	commissionPlan flexCommissionPlan,
) []brokerage.Activity {
	baseCurrency, quoteCurrency, ok := flexCurrencyPair(record.Attrs)
	baseAmount := math.Abs(activity.Units)
	quoteAmount := math.Abs(activity.Amount)
	if quoteAmount == 0 && baseAmount > 0 && activity.Price != 0 {
		quoteAmount = baseAmount * math.Abs(activity.Price)
	}

	side := strings.ToUpper(flexAttr(record.Attrs, "buysell", "direction"))
	buyBase := strings.Contains(side, "BUY") || strings.Contains(side, "BOT")
	sellBase := strings.Contains(side, "SELL") || strings.Contains(side, "SLD")
	if !buyBase && !sellBase {
		quantity, quantityOK := flexFloat(record.Attrs, "quantity", "qty", "shares")
		proceeds, proceedsOK := flexFloat(record.Attrs, "proceeds", "trademoney", "netcash")
		buyBase = quantityOK && proceedsOK && quantity > 0 && proceeds < 0
		sellBase = quantityOK && proceedsOK && quantity < 0 && proceeds > 0
	}
	if !ok || baseAmount == 0 || quoteAmount == 0 || buyBase == sellBase {
		activity.Type = brokerage.ActivityUnknown
		activity.Subtype = flexSubtypeFXTrade
		activity.NeedsReview = true
		activity.CurrencySymbol = nil
		return []brokerage.Activity{activity}
	}

	outCurrency, outAmount := baseCurrency, baseAmount
	inCurrency, inAmount := quoteCurrency, quoteAmount
	if buyBase {
		outCurrency, outAmount = quoteCurrency, quoteAmount
		inCurrency, inAmount = baseCurrency, baseAmount
	}

	groupID := "ibkr-flex:fx:" + activity.SourceRecordID
	makeLeg := func(direction string, activityType brokerage.ActivityType, currency string, amount float64) brokerage.Activity {
		leg := activity
		if direction != "out" {
			leg.ID = "ibkr-flex-activity-" + flexHash(activity.ID+"\x00"+direction)
		}
		leg.Type = activityType
		leg.Subtype = flexSubtypeFXTrade
		leg.Price = 0
		leg.Units = 0
		leg.Amount = amount
		leg.Currency = brokerage.Currency{Code: currency}
		leg.Symbol = nil
		leg.CurrencySymbol = nil
		leg.OptionSymbol = nil
		leg.Fee = 0
		leg.FxRate = nil
		if direction != "out" {
			leg.SourceRecordID = activity.SourceRecordID + ":" + direction
		}
		leg.SourceGroupID = groupID
		if direction != "out" {
			leg.SourceFingerprint = flexHash(activity.SourceFingerprint + "\x00" + direction)
		}
		leg.NeedsReview = false
		return leg
	}

	outLeg := makeLeg("out", brokerage.ActivityTransferOut, outCurrency, outAmount)
	inLeg := makeLeg("in", brokerage.ActivityTransferIn, inCurrency, inAmount)
	commissionCurrency := strings.ToUpper(flexAttr(record.Attrs, "ibcommissioncurrency", "commissioncurrency"))
	if commissionCurrency == "" {
		commissionCurrency = quoteCurrency
	}
	if activity.Fee > 0 {
		commissionAmount := activity.Fee
		if commissionPlan.Currency != "" && commissionPlan.Amount > 0 {
			commissionCurrency = commissionPlan.Currency
			commissionAmount = commissionPlan.Amount
		}
		if commissionCurrency == outCurrency {
			outLeg.Fee = commissionAmount
		} else {
			// Keep a commission charged in another currency as its own cash
			// movement instead of attaching it to the outgoing FX leg.
			feeLeg := makeLeg("fee", brokerage.ActivityFee, commissionCurrency, commissionAmount)
			feeLeg.Subtype = "FX_COMMISSION"
			feeLeg.SourceGroupID = ""
			return []brokerage.Activity{outLeg, inLeg, feeLeg}
		}
	}
	return []brokerage.Activity{outLeg, inLeg}
}

func planFlexCommissions(
	report flexReport,
	rates map[flexRateKey]float64,
	accountID, baseCurrency string,
) map[string]flexCommissionPlan {
	// Historical Flex statements can express an FX commission in the current
	// base currency even when the cash ledger charged the currency sold. The
	// per-currency Cash Report is authoritative for both allocation and totals.
	targets := make(map[string]float64)
	targetAvailable := make(map[string]bool)
	for _, record := range report.Records {
		if !strings.EqualFold(record.Kind, "CashReportCurrency") {
			continue
		}
		currency := strings.ToUpper(flexAttr(record.Attrs, "currency"))
		if currency == "" || currency == flexBaseSummaryCurrency {
			continue
		}
		if commission, ok := flexFloat(record.Attrs, "commissions"); ok {
			targets[currency] = math.Abs(commission)
			targetAvailable[currency] = true
		}
	}

	fixed := make(map[string]float64)
	candidates := make([]flexCommissionCandidate, 0)
	for _, record := range report.Records {
		if !strings.EqualFold(record.Kind, "Trade") {
			continue
		}
		commission, ok := flexFloat(record.Attrs, "ibcommission", "commission", "fee", "commtax")
		commission = math.Abs(commission)
		if !ok || commission == 0 {
			continue
		}
		commissionCurrency := strings.ToUpper(flexAttr(record.Attrs, "ibcommissioncurrency", "commissioncurrency"))
		if commissionCurrency == "" {
			commissionCurrency = strings.ToUpper(flexAttr(record.Attrs, "currency", "currencyprimary"))
		}
		assetClass := strings.ToUpper(flexAttr(record.Attrs, "assetcategory", "assetclass", "sectype"))
		if assetClass != ibkrAssetClassCash {
			fixed[commissionCurrency] += commission
			continue
		}

		base, quote, validPair := flexCurrencyPair(record.Attrs)
		side := strings.ToUpper(flexAttr(record.Attrs, "buysell", "direction"))
		buyBase := strings.Contains(side, "BUY") || strings.Contains(side, "BOT")
		sellBase := strings.Contains(side, "SELL") || strings.Contains(side, "SLD")
		if !validPair || buyBase == sellBase {
			fixed[commissionCurrency] += commission
			continue
		}
		outCurrency := base
		if buyBase {
			outCurrency = quote
		}
		if outCurrency == commissionCurrency {
			fixed[outCurrency] += commission
			continue
		}

		outAmount, converted := convertFlexAmount(
			commission, commissionCurrency, outCurrency, flexDate(record.Attrs), rates, baseCurrency,
		)
		candidate := flexCommissionCandidate{
			Fingerprint:      flexFingerprint(accountID, record),
			DeclaredCurrency: commissionCurrency,
			OutCurrency:      outCurrency,
			DeclaredAmount:   commission,
			OutAmount:        outAmount,
			// A converted legacy commission carries sub-cent base-currency
			// precision. A fee actually charged in the declared currency keeps
			// ordinary currency precision (for example, exactly USD 2.00).
			UseOutCurrency: converted && !isFlexMinorUnitAmount(commission),
		}
		candidates = append(candidates, candidate)
	}

	selectedTotals := make(map[string]float64)
	for _, candidate := range candidates {
		currency := candidate.DeclaredCurrency
		amount := candidate.DeclaredAmount
		if candidate.UseOutCurrency {
			currency = candidate.OutCurrency
			amount = candidate.OutAmount
		}
		selectedTotals[currency] += amount
	}

	scales := make(map[string]float64)
	for currency, selected := range selectedTotals {
		if selected == 0 || !targetAvailable[currency] {
			continue
		}
		desired := targets[currency] - fixed[currency]
		if desired < 0 {
			continue
		}
		ratio := desired / selected
		// Small differences come from the daily translation rate used by the
		// statement. Refuse a large correction so missing query fields or an
		// unexpected report shape cannot silently rewrite trade fees.
		if ratio >= 0.9 && ratio <= 1.1 {
			scales[currency] = ratio
		}
	}

	plans := make(map[string]flexCommissionPlan, len(candidates))
	for _, candidate := range candidates {
		plan := flexCommissionPlan{Currency: candidate.DeclaredCurrency, Amount: candidate.DeclaredAmount}
		if candidate.UseOutCurrency {
			plan.Currency = candidate.OutCurrency
			plan.Amount = candidate.OutAmount
		}
		if scale, ok := scales[plan.Currency]; ok {
			plan.Amount *= scale
		}
		plans[candidate.Fingerprint] = plan
	}
	return plans
}

func convertFlexAmount(
	amount float64,
	fromCurrency, toCurrency string,
	date time.Time,
	rates map[flexRateKey]float64,
	baseCurrency string,
) (float64, bool) {
	fromRate, fromOK := flexRateForCurrency(rates, date, fromCurrency, baseCurrency)
	toRate, toOK := flexRateForCurrency(rates, date, toCurrency, baseCurrency)
	if !fromOK || !toOK || fromRate <= 0 || toRate <= 0 {
		return 0, false
	}
	return amount * fromRate / toRate, true
}

func flexRateForCurrency(
	rates map[flexRateKey]float64,
	date time.Time,
	currency, baseCurrency string,
) (float64, bool) {
	if strings.EqualFold(currency, baseCurrency) {
		return 1, true
	}
	rate, ok := rates[flexRateKey{Date: date.UTC().Format("2006-01-02"), Currency: strings.ToUpper(currency)}]
	return rate, ok
}

func isFlexMinorUnitAmount(amount float64) bool {
	return math.Abs(amount*100-math.Round(amount*100)) < 1e-7
}

func flexCurrencyPair(attrs map[string]string) (baseCurrency, quoteCurrency string, ok bool) {
	rawSymbol := strings.ToUpper(strings.TrimSpace(flexAttr(attrs, "symbol", "underlyingsymbol")))
	quoteCurrency = strings.ToUpper(flexAttr(attrs, "currency", "currencyprimary"))
	parts := strings.FieldsFunc(rawSymbol, func(r rune) bool {
		return r == '.' || r == '/' || r == '-' || r == '_'
	})
	if len(parts) == 1 && len(parts[0]) == 6 {
		parts = []string{parts[0][:3], parts[0][3:]}
	}
	if len(parts) != 2 || len(parts[0]) != 3 || len(parts[1]) != 3 {
		return "", "", false
	}
	baseCurrency = parts[0]
	if quoteCurrency == "" {
		quoteCurrency = parts[1]
	}
	if quoteCurrency != parts[1] || baseCurrency == quoteCurrency ||
		!isAlphaCurrency(baseCurrency) || !isAlphaCurrency(quoteCurrency) {
		return "", "", false
	}
	return baseCurrency, quoteCurrency, true
}

func isAlphaCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func isFlexSnapshotRecord(kind string) bool {
	switch strings.ToLower(kind) {
	case "accountinformation", "openposition", "cashreportcurrency", "netassetvaluebyreportdateinbase",
		"equitysummarybyreportdateinbase":
		return true
	default:
		return false
	}
}

func mapFlexRecord(localAccountID, remoteAccountID string, record flexRecord, securities map[string]map[string]string) (brokerage.Activity, error) {
	tradeDate := flexDate(record.Attrs)
	if tradeDate.IsZero() {
		return brokerage.Activity{}, errors.New("record has no valid activity date")
	}
	amount, _ := flexFloat(record.Attrs, "amount", "proceeds", "trademoney", "netcash", "taxamount", "positionamount")
	units, _ := flexFloat(record.Attrs, "quantity", "qty", "shares")
	price, _ := flexFloat(record.Attrs, "tradeprice", "transferprice", "price")
	fee, _ := flexFloat(record.Attrs, "ibcommission", "commission", "fee", "commtax")

	rawType := strings.ToUpper(flexAttr(record.Attrs, "type", "transactiontype", "buySell", "levelofdetail"))
	description := flexAttr(record.Attrs, "description", "activitydescription", "actiondescription", "taxdescription", "merchantname", "category")
	activityType, subtype, needsReview := flexActivityType(record.Kind, rawType, description, amount, units, record.Attrs)
	if strings.EqualFold(record.Kind, "Trade") {
		if proceeds, ok := flexFloat(record.Attrs, "proceeds", "trademoney"); ok {
			amount = math.Abs(proceeds)
		}
		units = math.Abs(units)
		fee = math.Abs(fee)
	} else if strings.EqualFold(record.Kind, "BrokerageFee") && fee == 0 {
		fee = math.Abs(amount)
	}
	fee = math.Abs(fee)

	activity := brokerage.Activity{
		AccountID: localAccountID, Type: activityType, Subtype: subtype,
		RawType: rawType, Description: description, TradeDate: tradeDate,
		Price: price, Units: units, Amount: amount, Fee: fee,
		Currency:    brokerage.Currency{Code: strings.ToUpper(flexAttr(record.Attrs, "currency", "currencyprimary"))},
		Institution: flexInstitutionName, ProviderType: "ibkr", SourceSystem: "ibkr-flex",
		ExternalReferenceID: flexAttr(record.Attrs, "transactionid", "ibexecid", "tradeid", "iborderid"),
		NeedsReview:         needsReview,
	}
	if settlement := parseFlexDate(flexAttr(record.Attrs, "settledatetarget", "settledate")); !settlement.IsZero() {
		activity.SettlementDate = &settlement
	}
	if rate, ok := flexFloat(record.Attrs, "fxratetobase", "fxrate"); ok && rate > 0 {
		activity.FxRate = &rate
	}

	securityAttrs := record.Attrs
	if info := securities[flexAttr(record.Attrs, "conid", "conid1")]; info != nil {
		securityAttrs = mergeFlexAttrs(info, record.Attrs)
	}
	assetClass := strings.ToUpper(flexAttr(securityAttrs, "assetcategory", "assetclass", "sectype"))
	if (activity.Type == brokerage.ActivityBuy || activity.Type == brokerage.ActivitySell) &&
		activity.Units > 0 && activity.Amount > 0 &&
		assetClass != flexAssetClassOption && assetClass != flexAssetClassFutureOption &&
		assetClass != ibkrAssetClassBond && assetClass != ibkrAssetClassCash {
		// Wealthfolio reconstructs transaction-mode cash as units * price.
		// Flex proceeds is the authoritative pre-commission cash movement and
		// can carry more precision than the displayed tradePrice.
		activity.Price = activity.Amount / activity.Units
	}
	if symbol := flexSymbol(securityAttrs); symbol != nil {
		if assetClass == ibkrAssetClassCash || activity.Type == brokerage.ActivityConversion {
			activity.CurrencySymbol = symbol
		} else {
			activity.Symbol = symbol
		}
	}
	if assetClass == flexAssetClassOption || assetClass == flexAssetClassFutureOption {
		if option := flexOptionSymbol(securityAttrs); option != nil {
			activity.OptionSymbol = option
			activity.Symbol = nil
		}
	}
	if activity.Type == brokerage.ActivitySplit {
		if ratio, ok := flexFloat(record.Attrs, "ratio", "splitratio"); ok && ratio > 0 {
			activity.Amount = ratio
		} else if ratio, ok := flexSplitRatio(description); ok {
			activity.Amount = ratio
		} else {
			activity.NeedsReview = true
		}
	}
	if requiresInstrument(activity.Type) && activity.Symbol == nil && activity.CurrencySymbol == nil && activity.OptionSymbol == nil {
		activity.NeedsReview = true
	}

	activity.SourceFingerprint = flexFingerprint(remoteAccountID, record)
	identity := flexRecordIdentity(record)
	if identity == "" {
		identity = "fingerprint:" + activity.SourceFingerprint
		activity.NeedsReview = true
	}
	kind := strings.ToLower(record.Kind)
	activity.SourceRecordID = "ibkr-flex:" + kind + ":" + identity
	activity.ID = "ibkr-flex-activity-" + flexHash(remoteAccountID+"\x00"+activity.SourceRecordID)
	if group := flexAttr(record.Attrs, "tradeid", "iborderid", "transactionid"); group != "" {
		activity.SourceGroupID = "ibkr-flex:" + group
	}
	return activity, nil
}

func flexSplitRatio(description string) (float64, bool) {
	match := flexSplitPattern.FindStringSubmatch(description)
	if len(match) != 3 {
		return 0, false
	}
	numerator, numeratorErr := strconv.ParseFloat(match[1], 64)
	denominator, denominatorErr := strconv.ParseFloat(match[2], 64)
	if numeratorErr != nil || denominatorErr != nil || numerator <= 0 || denominator <= 0 {
		return 0, false
	}
	return numerator / denominator, true
}

func flexActivityType(kind, rawType, description string, amount, units float64, attrs map[string]string) (brokerage.ActivityType, string, bool) {
	combined := strings.ToUpper(strings.Join([]string{
		kind, rawType, description, flexAttr(attrs, "buysell"), flexAttr(attrs, "code", "direction"),
	}, " "))
	assetClass := strings.ToUpper(flexAttr(attrs, "assetcategory", "assetclass", "sectype"))
	switch strings.ToLower(kind) {
	case "trade":
		if assetClass == ibkrAssetClassCash || strings.Contains(combined, "FOREX") || strings.Contains(combined, "FX TRADE") {
			return brokerage.ActivityConversion, "FX_TRADE", false
		}
		buy := strings.Contains(combined, "BUY") || strings.Contains(combined, "BOT")
		sell := strings.Contains(combined, "SELL") || strings.Contains(combined, "SLD")
		if assetClass == flexAssetClassOption || assetClass == flexAssetClassFutureOption {
			if buy {
				return brokerage.ActivityOptionBuy, "", false
			}
			if sell {
				return brokerage.ActivityOptionSell, "", false
			}
		}
		if buy {
			return brokerage.ActivityBuy, "", false
		}
		if sell {
			return brokerage.ActivitySell, "", false
		}
	case "cashtransaction":
		detailType := strings.ToUpper(rawType)
		detail := strings.ToUpper(description)
		switch {
		case strings.Contains(detailType, "DIVIDEND") || strings.Contains(detailType, "PAYMENT IN LIEU"):
			return brokerage.ActivityDividend, "", false
		case strings.Contains(detailType, "WITHHOLD") || strings.Contains(detailType, "TAX"):
			if amount > 0 {
				return brokerage.ActivityCredit, "REFUND", false
			}
			return brokerage.ActivityTax, "", false
		case strings.Contains(combined, "WITHHOLD") || strings.Contains(combined, "TAX"):
			if amount > 0 {
				return brokerage.ActivityCredit, "REFUND", false
			}
			return brokerage.ActivityTax, "", false
		case strings.Contains(combined, "DIVIDEND") || strings.Contains(combined, "PAYMENT IN LIEU"):
			return brokerage.ActivityDividend, "", false
		case strings.Contains(combined, "INTEREST"):
			return brokerage.ActivityInterest, "", false
		case (strings.Contains(combined, "FEE") || strings.Contains(combined, "COMMISSION")) && amount > 0:
			return brokerage.ActivityCredit, "FEE_REFUND", false
		case strings.Contains(combined, "FEE") || strings.Contains(combined, "COMMISSION"):
			return brokerage.ActivityFee, "", false
		case strings.Contains(detail, "DEPOSIT") || strings.Contains(detail, "RECEIPT"):
			return brokerage.ActivityDeposit, "", false
		case strings.Contains(detail, "WITHDRAW") || strings.Contains(detail, "DISBURSEMENT"):
			return brokerage.ActivityWithdrawal, "", false
		case strings.Contains(combined, "DEPOSIT") && !strings.Contains(combined, "WITHDRAW"):
			return brokerage.ActivityDeposit, "", false
		case strings.Contains(combined, "WITHDRAW") && !strings.Contains(combined, "DEPOSIT"):
			return brokerage.ActivityWithdrawal, "", false
		}
		if amount < 0 {
			return brokerage.ActivityWithdrawal, rawType, true
		}
		return brokerage.ActivityDeposit, rawType, true
	case "transfer", "tradetransfer":
		if strings.Contains(combined, "OUT") || strings.Contains(combined, "DELIVER") || units < 0 || (units == 0 && amount < 0) {
			return brokerage.ActivityTransferOut, rawType, false
		}
		return brokerage.ActivityTransferIn, rawType, false
	case "corporateaction":
		if strings.Contains(combined, "SPLIT") {
			return brokerage.ActivitySplit, "", false
		}
		if strings.Contains(combined, "DIVIDEND") {
			return brokerage.ActivityDividend, "CORPORATE_ACTION", false
		}
	case "optioneae":
		switch {
		case strings.Contains(combined, "EXPIR"):
			return brokerage.ActivityOptionExpiry, "", false
		case strings.Contains(combined, "ASSIGN"):
			return brokerage.ActivityOptionAssignment, "", false
		case strings.Contains(combined, "EXERC"):
			return brokerage.ActivityOptionExercise, "", false
		}
	case "transactiontax":
		return brokerage.ActivityTax, "", false
	case "brokeragefee", "clientfee", "transactionfee":
		return brokerage.ActivityFee, strings.ToUpper(kind), false
	case "debitcardactivity":
		if amount > 0 {
			return brokerage.ActivityDeposit, "DEBIT_CARD_REFUND", false
		}
		return brokerage.ActivityWithdrawal, "DEBIT_CARD", false
	case "slbactivity":
		return brokerage.ActivityUnknown, "SECURITIES_BORROWED_LENT", true
	}
	return brokerage.ActivityUnknown, rawType, true
}

func flexSymbol(attrs map[string]string) *brokerage.Symbol {
	rawSymbol := flexAttr(attrs, "symbol", "underlyingsymbol")
	if rawSymbol == "" {
		return nil
	}
	assetClass := strings.ToUpper(flexAttr(attrs, "assetcategory", "assetclass", "sectype"))
	exchange := flexAttr(attrs, "listingexchange", "exchange")
	currency := strings.ToUpper(flexAttr(attrs, "currency", "currencyprimary"))
	symbol := formatSymbol(rawSymbol, exchange, currency)
	return &brokerage.Symbol{
		Symbol: symbol, RawSymbol: rawSymbol,
		Description: flexAttr(attrs, "description", "securitydescription"), Name: symbol,
		Type:     brokerage.SymbolType{Code: ibSecTypeToCode(assetClass), Description: assetClass, IsSupported: true},
		Exchange: brokerage.Exchange{Code: exchange, MICCode: flexExchangeMIC(exchange)}, Currency: brokerage.Currency{Code: currency},
		FIGICode: flexFIGI(attrs),
	}
}

func flexExchangeMIC(exchange string) string {
	return flexExchangeMICs[strings.ToUpper(strings.TrimSpace(exchange))]
}

func flexOptionSymbol(attrs map[string]string) *brokerage.OptionSymbol {
	strike, strikeOK := flexFloat(attrs, "strike")
	expiry := parseFlexDate(flexAttr(attrs, "expiry", "expirationdate"))
	putCall := strings.ToUpper(flexAttr(attrs, "putcall", "optiontype"))
	if !strikeOK && expiry.IsZero() && putCall == "" {
		return nil
	}
	side := brokerage.OptionSide(putCall)
	switch putCall {
	case "C":
		side = brokerage.OptionCall
	case "P":
		side = brokerage.OptionPut
	}
	underlyingRaw := flexAttr(attrs, "underlyingsymbol", "symbol")
	exchange := flexAttr(attrs, "listingexchange", "exchange")
	return &brokerage.OptionSymbol{
		Ticker: flexAttr(attrs, "symbol", "description"), OptionType: side,
		StrikePrice: strike, ExpirationDate: expiry,
		Underlying: brokerage.Symbol{
			Symbol: underlyingRaw, RawSymbol: underlyingRaw,
			Type:     brokerage.SymbolType{Code: "EQUITY", IsSupported: true},
			Exchange: brokerage.Exchange{Code: exchange, MICCode: flexExchangeMIC(exchange)},
			Currency: brokerage.Currency{Code: strings.ToUpper(flexAttr(attrs, "currency", "currencyprimary"))},
		},
	}
}

func flexFIGI(attrs map[string]string) string {
	if figi := flexAttr(attrs, "figi"); figi != "" {
		return figi
	}
	if strings.EqualFold(flexAttr(attrs, "securityidtype"), "FIGI") {
		return flexAttr(attrs, "securityid")
	}
	return ""
}

func indexFlexRate(rates map[flexRateKey]float64, attrs map[string]string, baseCurrency string) {
	rate, ok := flexFloat(attrs, "rate")
	if !ok || rate <= 0 {
		return
	}
	date := parseFlexDate(flexAttr(attrs, "reportdate", "date", "datetime"))
	if date.IsZero() {
		return
	}
	from := strings.ToUpper(flexAttr(attrs, "fromcurrency", "currency"))
	to := strings.ToUpper(flexAttr(attrs, "tocurrency", "basecurrency"))
	base := strings.ToUpper(baseCurrency)
	if from != "" && to == base {
		rates[flexRateKey{Date: date.Format("2006-01-02"), Currency: from}] = rate
	} else if from == base && to != "" {
		rates[flexRateKey{Date: date.Format("2006-01-02"), Currency: to}] = 1 / rate
	}
}

func flexDate(attrs map[string]string) time.Time {
	if value := flexAttr(attrs, "datetime", "date", "transactiondatetime", "postingdate"); value != "" {
		if parsed := parseFlexDate(value); !parsed.IsZero() {
			return parsed
		}
	}
	date := flexAttr(attrs, "tradedate", "reportdate", "activitydate", "settledate")
	timeValue := flexAttr(attrs, "tradetime", "time")
	if timeValue != "" {
		date = date + " " + timeValue
	}
	return parseFlexDate(date)
}

func parseFlexDate(value string) time.Time {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, ";", " "), ",", " "))
	value = strings.Join(strings.Fields(value), " ")
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "20060102 15:04:05",
		"20060102 150405", "2006-01-02", "20060102", "01/02/2006 15:04:05", "01/02/2006",
	} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func flexRecordIdentity(record flexRecord) string {
	for _, key := range []string{"ibexecid", "transactionid", "tradeid", "transferid", "activityid", "actionid", "iborderid", "orderid"} {
		if value := record.Attrs[key]; value != "" {
			return key + ":" + value
		}
	}
	return ""
}

func flexFingerprint(accountID string, record flexRecord) string {
	keys := make([]string, 0, len(record.Attrs))
	for key := range record.Attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var payload strings.Builder
	payload.WriteString(accountID)
	payload.WriteByte(0)
	payload.WriteString(strings.ToLower(record.Kind))
	for _, key := range keys {
		payload.WriteByte(0)
		payload.WriteString(key)
		payload.WriteByte('=')
		payload.WriteString(record.Attrs[key])
	}
	return flexHash(payload.String())
}

func flexHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func flexAttr(attrs map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(attrs[strings.ToLower(key)]); value != "" {
			return value
		}
	}
	return ""
}

func flexFloat(attrs map[string]string, keys ...string) (float64, bool) {
	value := flexAttr(attrs, keys...)
	if value == "" {
		return 0, false
	}
	value = strings.ReplaceAll(value, ",", "")
	parsed, err := strconv.ParseFloat(value, 64)
	return parsed, err == nil
}

func mergeFlexAttrs(base, override map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		if value != "" {
			merged[key] = value
		}
	}
	return merged
}

func requiresInstrument(activityType brokerage.ActivityType) bool {
	switch activityType {
	case brokerage.ActivityBuy, brokerage.ActivitySell, brokerage.ActivityConversion,
		brokerage.ActivityOptionBuy, brokerage.ActivityOptionSell, brokerage.ActivityOptionExpiry,
		brokerage.ActivityOptionAssignment, brokerage.ActivityOptionExercise:
		return true
	default:
		return false
	}
}

func validateFlexAccount(report flexReport, expected string) error {
	if len(report.Accounts) == 0 {
		return errors.New("ibkr flex: statement did not identify an account")
	}
	if _, ok := report.Accounts[expected]; !ok {
		return fmt.Errorf("ibkr flex: statement does not contain configured account %s", maskFlexIdentifier(expected))
	}
	if len(report.Accounts) != 1 {
		return fmt.Errorf("ibkr flex: query must contain only configured account %s", maskFlexIdentifier(expected))
	}
	return nil
}

func maskFlexIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 6 {
		return "***"
	}
	return value[:2] + "…" + value[len(value)-3:]
}
