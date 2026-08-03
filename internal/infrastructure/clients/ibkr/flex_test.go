package ibkr

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

type flexErrorDoer struct{}

func (flexErrorDoer) Do(request *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("request failed for %s", request.URL.String())
}

func TestFlexClientMapsHistoryAndDailyFX(t *testing.T) {
	var server *httptest.Server
	polls := 0
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/SendRequest":
			from := request.URL.Query().Get("fd")
			to := request.URL.Query().Get("td")
			reference := "history"
			if from == "20240101" && to == "20240101" {
				reference = "snapshot"
			} else if from != "20230801" || to != "20240101" {
				t.Errorf("unexpected Flex window %s..%s", from, to)
			}
			fmt.Fprintf(writer, `<FlexStatementResponse><Status>Success</Status><ReferenceCode>%s</ReferenceCode><url>%s/GetStatement</url></FlexStatementResponse>`, reference, server.URL)
		case "/GetStatement":
			polls++
			if polls == 1 {
				fmt.Fprint(writer, `<FlexStatementResponse><Status>Warn</Status><ErrorCode>1019</ErrorCode><ErrorMessage>Statement generation in progress</ErrorMessage></FlexStatementResponse>`)
				return
			}
			if request.URL.Query().Get("q") == "snapshot" {
				fmt.Fprint(writer, `<FlexQueryResponse><FlexStatements count="1"><FlexStatement accountId="U1234567">
<AccountInformation accountId="U1234567" accountType="Margin" baseCurrency="USD"/>
<SecuritiesInfo><SecurityInfo conid="77" symbol="SAP" description="SAP SE" assetCategory="STK" currency="EUR" listingExchange="IBIS" figi="BBG000BBJQV0"/></SecuritiesInfo>
<NetAssetValue><NetAssetValueByReportDateInBase accountId="U1234567" reportDate="20240101" total="1234.50"/></NetAssetValue>
<CashReport><CashReportCurrency accountId="U1234567" currency="BASE_SUMMARY" endingCash="120"/><CashReportCurrency accountId="U1234567" currency="USD" endingCash="20"/><CashReportCurrency accountId="U1234567" currency="EUR" endingCash="90"/></CashReport>
<OpenPositions><OpenPosition accountId="U1234567" conid="77" currency="EUR" assetCategory="STK" reportDate="20240101" position="2" markPrice="110" costBasisPrice="100" fifoPnlUnrealized="20"/></OpenPositions>
</FlexStatement></FlexStatements></FlexQueryResponse>`)
				return
			}
			fmt.Fprint(writer, `<FlexQueryResponse><FlexStatements count="1"><FlexStatement accountId="U1234567">
<SecuritiesInfo><SecurityInfo conid="77" symbol="SAP" description="SAP SE" assetCategory="STK" currency="EUR" listingExchange="IBIS" figi="BBG000BBJQV0"/></SecuritiesInfo>
<CurrencyConversionRates><ConversionRate reportDate="2023-08-04" fromCurrency="EUR" toCurrency="USD" rate="1.10"/></CurrencyConversionRates>
<Trades><Trade accountId="U1234567" assetCategory="STK" conid="77" currency="EUR" tradeDate="20230804" tradeTime="12:30:00" transactionType="ExchTrade" buySell="BUY" quantity="2" tradePrice="100" proceeds="-200" ibCommission="-1" ibExecID="exec-1"/></Trades>
<CashTransactions><CashTransaction accountId="U1234567" currency="EUR" dateTime="2023-08-03 09:00:00" amount="500" type="Deposits/Withdrawals" description="Electronic deposit" transactionID="cash-1" fxRateToBase="1.09"/></CashTransactions>
<Trades><Trade accountId="U1234567" assetCategory="CASH" symbol="EUR.USD" currency="USD" tradeDate="20230805" tradeTime="10:00:00" buySell="BUY" quantity="100" tradePrice="1.08" proceeds="-108" ibCommission="-0.5" ibExecID="fx-1" fxRateToBase="1"/></Trades>
</FlexStatement></FlexStatements></FlexQueryResponse>`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := newFlexTestClient(t, server.URL, time.Date(2023, 8, 1, 0, 0, 0, 0, time.UTC))
	client.api.sendEvery = time.Millisecond
	client.now = func() time.Time { return time.Date(2024, 1, 2, 18, 0, 0, 0, time.UTC) }

	snapshot, err := client.FetchAccountSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Accounts) != 1 || snapshot.Accounts[0].BalanceTotal != 1234.50 || snapshot.Accounts[0].InitialTxSyncDone {
		t.Fatalf("Flex account snapshot = %#v", snapshot.Accounts)
	}
	if snapshot.Connection.LogoURL != flexLogoURL || snapshot.Connection.SquareLogoURL != flexSquareLogoURL {
		t.Fatalf("Flex connection logos = %#v", snapshot.Connection)
	}
	if len(snapshot.Holdings) != 1 || len(snapshot.Holdings[0].Balances) != 2 || len(snapshot.Holdings[0].Positions) != 1 {
		t.Fatalf("Flex holdings snapshot = %#v", snapshot.Holdings)
	}
	if got := snapshot.Holdings[0].Positions[0]; got.Units != 2 || got.Price != 110 || got.AveragePurchasePrice != 100 || got.OpenPnL != 20 {
		t.Fatalf("Flex open position = %#v", got)
	}

	var page domainsync.ActivityPage
	err = client.StreamActivities(context.Background(), []domainsync.ActivitySyncState{{
		AccountID: "ibkr-U1234567", InitialSyncCompleted: true,
	}}, func(_ context.Context, emitted domainsync.ActivityPage) error {
		page = emitted
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !page.InitialSync || !page.Complete || len(page.Items) != 4 {
		t.Fatalf("unexpected activity page: %#v", page)
	}
	byID := make(map[string]brokerage.Activity)
	for _, item := range page.Items {
		byID[item.SourceRecordID] = item
	}
	stock := byID["ibkr-flex:trade:ibexecid:exec-1"]
	if stock.Type != brokerage.ActivityBuy || stock.FxRate != nil {
		t.Fatalf("stock FX mapping = %#v", stock)
	}
	if stock.Symbol == nil || stock.Symbol.FIGICode != "BBG000BBJQV0" ||
		stock.Symbol.Exchange.MICCode != "XETR" || stock.Amount != 200 || stock.Fee != 1 {
		t.Fatalf("stock instrument mapping = %#v", stock)
	}
	deposit := byID["ibkr-flex:cashtransaction:transactionid:cash-1"]
	if deposit.Type != brokerage.ActivityDeposit || deposit.FxRate == nil || *deposit.FxRate != 1.09 || deposit.Fee != 0 {
		t.Fatalf("deposit mapping = %#v", deposit)
	}
	conversionOut := byID["ibkr-flex:trade:ibexecid:fx-1"]
	conversionIn := byID["ibkr-flex:trade:ibexecid:fx-1:in"]
	if conversionOut.Type != brokerage.ActivityTransferOut || conversionOut.Currency.Code != "USD" ||
		conversionOut.Amount != 108 || conversionOut.Fee != 0.5 {
		t.Fatalf("conversion out mapping = %#v", conversionOut)
	}
	if conversionIn.Type != brokerage.ActivityTransferIn || conversionIn.Currency.Code != "EUR" ||
		conversionIn.Amount != 100 || conversionIn.Fee != 0 {
		t.Fatalf("conversion in mapping = %#v", conversionIn)
	}
	if conversionOut.SourceGroupID == "" || conversionOut.SourceGroupID != conversionIn.SourceGroupID ||
		conversionOut.Subtype != "FXEXCHANGE" || conversionIn.Subtype != "FXEXCHANGE" {
		t.Fatalf("conversion pair is not linked: out=%#v in=%#v", conversionOut, conversionIn)
	}
	if conversionOut.Symbol != nil || conversionOut.CurrencySymbol != nil ||
		conversionIn.Symbol != nil || conversionIn.CurrencySymbol != nil {
		t.Fatalf("conversion legs must be cash-only: out=%#v in=%#v", conversionOut, conversionIn)
	}
}

func TestFlexClientFallsBackToPreviousAvailableBusinessDay(t *testing.T) {
	requestedDays := make([]string, 0, 2)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/SendRequest":
			day := request.URL.Query().Get("fd")
			requestedDays = append(requestedDays, day)
			if day == "20260803" {
				fmt.Fprint(writer, `<FlexStatementResponse><Status>Fail</Status><ErrorCode>1003</ErrorCode><ErrorMessage>Statement is not available.</ErrorMessage></FlexStatementResponse>`)
				return
			}
			if day != "20260731" || request.URL.Query().Get("td") != day {
				t.Errorf("unexpected snapshot day %s", day)
			}
			fmt.Fprintf(writer, `<FlexStatementResponse><Status>Success</Status><ReferenceCode>snapshot</ReferenceCode><url>%s/GetStatement</url></FlexStatementResponse>`, server.URL)
		case "/GetStatement":
			fmt.Fprint(writer, `<FlexQueryResponse><FlexStatements count="1"><FlexStatement accountId="U1234567">
<AccountInformation accountId="U1234567" accountType="Margin" baseCurrency="USD"/>
<NetAssetValue><NetAssetValueByReportDateInBase accountId="U1234567" reportDate="20260731" total="1234.50"/></NetAssetValue>
<CashReport><CashReportCurrency accountId="U1234567" currency="USD" endingCash="120"/></CashReport>
<OpenPositions/>
</FlexStatement></FlexStatements></FlexQueryResponse>`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := newFlexTestClient(t, server.URL, time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC))
	client.api.sendEvery = 0
	client.now = func() time.Time { return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC) }

	snapshot, err := client.FetchAccountSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Accounts) != 1 || snapshot.Accounts[0].BalanceTotal != 1234.50 {
		t.Fatalf("fallback snapshot = %#v", snapshot)
	}
	var activityPage domainsync.ActivityPage
	err = client.StreamActivities(context.Background(), []domainsync.ActivitySyncState{{
		AccountID: "ibkr-U1234567",
	}}, func(_ context.Context, page domainsync.ActivityPage) error {
		activityPage = page
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !activityPage.Complete || !activityPage.InitialSync {
		t.Fatalf("fallback activity page = %#v", activityPage)
	}

	want := []string{"20260803", "20260731", "20260731"}
	if len(requestedDays) != len(want) || requestedDays[0] != want[0] ||
		requestedDays[1] != want[1] || requestedDays[2] != want[2] {
		t.Fatalf("snapshot request days = %#v, want %#v", requestedDays, want)
	}
}

func TestFlexError1003IsUnavailableRatherThanPending(t *testing.T) {
	response := flexServiceResponse{ErrorCode: "1003", ErrorMessage: "Statement is not available."}
	err := flexResponseError(response)
	if !isFlexStatementUnavailable(err) {
		t.Fatalf("1003 should be recognized as unavailable: %v", err)
	}
	if isFlexPending(response) {
		t.Fatal("1003 is a terminal unavailable response, not a generation-in-progress response")
	}
}

func TestFlexExchangeMICMapsObservedIBKRVenues(t *testing.T) {
	tests := map[string]string{
		"NYSE": "XNYS", "NASDAQ": "XNAS", "IBIS2": "XETR", "BVB": "XBSE",
		"unknown": "",
	}
	for exchange, expected := range tests {
		t.Run(exchange, func(t *testing.T) {
			if actual := flexExchangeMIC(exchange); actual != expected {
				t.Fatalf("flexExchangeMIC(%q) = %q, want %q", exchange, actual, expected)
			}
		})
	}
}

func TestMapFlexReportDoesNotTranslateSecurityTradeCashToBaseCurrency(t *testing.T) {
	report := flexReport{
		Accounts: map[string]struct{}{"U1234567": {}},
		Records: []flexRecord{
			{Kind: "SecurityInfo", Attrs: map[string]string{
				"conid": "77", "symbol": "SAP", "assetcategory": "STK", "currency": "EUR",
			}},
			{Kind: "ConversionRate", Attrs: map[string]string{
				"reportdate": "2024-01-02", "fromcurrency": "EUR", "tocurrency": "USD", "rate": "1.10",
			}},
			{Kind: "Trade", Attrs: map[string]string{
				"accountid": "U1234567", "conid": "77", "assetcategory": "STK", "currency": "EUR",
				"tradedate": "20240102", "buysell": "BUY", "quantity": "2", "tradeprice": "100",
				"proceeds": "-200", "ibexecid": "buy", "fxratetobase": "1.09",
			}},
			{Kind: "Trade", Attrs: map[string]string{
				"accountid": "U1234567", "conid": "77", "assetcategory": "STK", "currency": "EUR",
				"tradedate": "20240102", "buysell": "SELL", "quantity": "1", "tradeprice": "110",
				"proceeds": "110", "ibexecid": "sell",
			}},
			{Kind: "CashTransaction", Attrs: map[string]string{
				"accountid": "U1234567", "currency": "EUR", "date": "20240102", "amount": "500",
				"type": "Deposits/Withdrawals", "description": "Electronic deposit", "transactionid": "deposit",
			}},
		},
	}

	mapped := mapFlexReport(report, "U1234567", "USD")
	bySourceID := make(map[string]brokerage.Activity, len(mapped.Activities))
	for _, activity := range mapped.Activities {
		bySourceID[activity.SourceRecordID] = activity
	}

	buy := bySourceID["ibkr-flex:trade:ibexecid:buy"]
	sell := bySourceID["ibkr-flex:trade:ibexecid:sell"]
	deposit := bySourceID["ibkr-flex:cashtransaction:transactionid:deposit"]
	if buy.FxRate != nil || sell.FxRate != nil {
		t.Fatalf("security trades must settle native cash: buy=%#v sell=%#v", buy, sell)
	}
	if deposit.FxRate == nil || *deposit.FxRate != 1.10 {
		t.Fatalf("non-trade reporting FX rate should be retained: %#v", deposit)
	}
}

func TestMapFlexReportUsesSecurityProceedsForEffectiveCashPrice(t *testing.T) {
	report := flexReport{
		Accounts: map[string]struct{}{"U1234567": {}},
		Records: []flexRecord{
			{Kind: "SecurityInfo", Attrs: map[string]string{
				"conid": "77", "symbol": "ABC", "assetcategory": "STK", "currency": "EUR",
			}},
			{Kind: "Trade", Attrs: map[string]string{
				"accountid": "U1234567", "conid": "77", "assetcategory": "STK", "currency": "EUR",
				"tradedate": "20240102", "buysell": "BUY", "quantity": "3", "tradeprice": "10",
				"proceeds": "-30.01", "ibexecid": "buy",
			}},
		},
	}

	mapped := mapFlexReport(report, "U1234567", "USD")
	if len(mapped.Activities) != 1 {
		t.Fatalf("mapped activities = %#v", mapped.Activities)
	}
	activity := mapped.Activities[0]
	if math.Abs(activity.Units*activity.Price-30.01) > 1e-12 {
		t.Fatalf("effective cash price = %#v", activity)
	}
}

func TestExpandFlexConversionMapsSellAndMalformedRecords(t *testing.T) {
	base := brokerage.Activity{
		ID: "activity", Type: brokerage.ActivityConversion, TradeDate: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Units: 25, Amount: 27.5, Price: 1.1, Fee: 0.25, Currency: brokerage.Currency{Code: "USD"},
		SourceRecordID: "ibkr-flex:trade:ibexecid:fx-sell", SourceFingerprint: "fingerprint",
	}
	sell := expandFlexConversion(base, flexRecord{Attrs: map[string]string{
		"symbol": "EUR.USD", "currency": "USD", "buysell": "SELL", "ibcommissioncurrency": "USD",
	}})
	if len(sell) != 3 || sell[0].Type != brokerage.ActivityTransferOut || sell[0].Currency.Code != "EUR" ||
		sell[0].Amount != 25 || sell[0].Fee != 0 {
		t.Fatalf("sell out leg = %#v", sell)
	}
	if sell[1].Type != brokerage.ActivityTransferIn || sell[1].Currency.Code != "USD" || sell[1].Amount != 27.5 || sell[1].Fee != 0 {
		t.Fatalf("sell in leg = %#v", sell)
	}
	if sell[2].Type != brokerage.ActivityFee || sell[2].Subtype != "FX_COMMISSION" ||
		sell[2].Currency.Code != "USD" || sell[2].Amount != 0.25 || sell[2].Fee != 0 {
		t.Fatalf("sell fee leg = %#v", sell)
	}

	malformed := expandFlexConversion(base, flexRecord{Attrs: map[string]string{
		"symbol": "INVALID", "currency": "USD", "buysell": "BUY",
	}})
	if len(malformed) != 1 || malformed[0].Type != brokerage.ActivityUnknown || !malformed[0].NeedsReview {
		t.Fatalf("malformed conversion = %#v", malformed)
	}
}

func TestMapFlexReportReconcilesFXCommissionsWithCashReportCurrencies(t *testing.T) {
	report := flexReport{
		Accounts: map[string]struct{}{"U1234567": {}},
		Records: []flexRecord{
			{Kind: "ConversionRate", Attrs: map[string]string{
				"reportdate": "2024-08-20", "fromcurrency": "EUR", "tocurrency": "USD", "rate": "1.113",
			}},
			{Kind: "Trade", Attrs: map[string]string{
				"accountid": "U1234567", "assetcategory": "CASH", "symbol": "EUR.USD", "currency": "USD",
				"tradedate": "20240820", "buysell": "SELL", "quantity": "-200", "tradeprice": "1.10",
				"proceeds": "220", "ibcommission": "-2.00803008", "ibcommissioncurrency": "USD", "ibexecid": "fx-eur-fee",
			}},
			{Kind: "Trade", Attrs: map[string]string{
				"accountid": "U1234567", "assetcategory": "CASH", "symbol": "EUR.USD", "currency": "USD",
				"tradedate": "20250318", "buysell": "SELL", "quantity": "-400", "tradeprice": "1.09",
				"proceeds": "436", "ibcommission": "-2.00", "ibcommissioncurrency": "USD", "ibexecid": "fx-usd-fee",
			}},
			{Kind: "CashReportCurrency", Attrs: map[string]string{
				"accountid": "U1234567", "currency": "EUR", "fromdate": "20240820", "todate": "20250318",
				"commissions": "-1.80416",
			}},
			{Kind: "CashReportCurrency", Attrs: map[string]string{
				"accountid": "U1234567", "currency": "USD", "fromdate": "20240820", "todate": "20250318",
				"commissions": "-2.00",
			}},
		},
	}

	mapped := mapFlexReport(report, "U1234567", "USD")
	bySourceID := make(map[string]brokerage.Activity, len(mapped.Activities))
	for _, activity := range mapped.Activities {
		bySourceID[activity.SourceRecordID] = activity
	}

	eurFee := bySourceID["ibkr-flex:trade:ibexecid:fx-eur-fee"]
	if eurFee.Type != brokerage.ActivityTransferOut || eurFee.Currency.Code != "EUR" ||
		math.Abs(eurFee.Fee-1.80416) > 1e-9 {
		t.Fatalf("EUR-funded FX commission = %#v", eurFee)
	}
	usdFee := bySourceID["ibkr-flex:trade:ibexecid:fx-usd-fee:fee"]
	if usdFee.Type != brokerage.ActivityFee || usdFee.Currency.Code != "USD" ||
		usdFee.Subtype != "FX_COMMISSION" || usdFee.Amount != 2 {
		t.Fatalf("USD-funded FX commission = %#v", usdFee)
	}
}

func TestMapFlexReportClassifiesDividendTaxTextAndPositiveWithholdingRefund(t *testing.T) {
	report := flexReport{
		Accounts: map[string]struct{}{"U1234567": {}},
		Records: []flexRecord{
			{Kind: "CashTransaction", Attrs: map[string]string{
				"accountid": "U1234567", "currency": "USD", "date": "20240102", "amount": "4.28",
				"type": "Dividends", "description": "Dividend tax adjustment", "transactionid": "dividend",
			}},
			{Kind: "CashTransaction", Attrs: map[string]string{
				"accountid": "U1234567", "currency": "USD", "date": "20240102", "amount": "2.42",
				"type": "Withholding Tax", "description": "Withholding tax refund", "transactionid": "refund",
			}},
			{Kind: "CashTransaction", Attrs: map[string]string{
				"accountid": "U1234567", "currency": "USD", "date": "20240102", "amount": "-3.00",
				"type": "Withholding Tax", "description": "Withholding tax", "transactionid": "tax",
			}},
		},
	}

	mapped := mapFlexReport(report, "U1234567", "USD")
	bySourceID := make(map[string]brokerage.Activity, len(mapped.Activities))
	for _, activity := range mapped.Activities {
		bySourceID[activity.SourceRecordID] = activity
	}

	dividend := bySourceID["ibkr-flex:cashtransaction:transactionid:dividend"]
	if dividend.Type != brokerage.ActivityDividend || dividend.NeedsReview {
		t.Fatalf("dividend tax text classification = %#v", dividend)
	}
	refund := bySourceID["ibkr-flex:cashtransaction:transactionid:refund"]
	if refund.Type != brokerage.ActivityCredit || refund.Subtype != "REFUND" || refund.NeedsReview {
		t.Fatalf("positive withholding classification = %#v", refund)
	}
	tax := bySourceID["ibkr-flex:cashtransaction:transactionid:tax"]
	if tax.Type != brokerage.ActivityTax || tax.NeedsReview {
		t.Fatalf("negative withholding classification = %#v", tax)
	}
}

func TestFlexClientBackfillsIn365DayWindowsAndResumes(t *testing.T) {
	var mutex sync.Mutex
	requested := make([][2]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/SendRequest":
			mutex.Lock()
			requested = append(requested, [2]string{request.URL.Query().Get("fd"), request.URL.Query().Get("td")})
			index := len(requested)
			mutex.Unlock()
			fmt.Fprintf(writer, `<FlexStatementResponse><Status>Success</Status><ReferenceCode>ref-%d</ReferenceCode></FlexStatementResponse>`, index)
		case "/GetStatement":
			fmt.Fprint(writer, `<FlexQueryResponse><FlexStatements count="1"><FlexStatement accountId="U1234567"/></FlexStatements></FlexQueryResponse>`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	start := time.Date(2023, 8, 1, 0, 0, 0, 0, time.UTC)
	client := newFlexTestClient(t, server.URL, start)
	client.api.sendEvery = time.Millisecond
	client.now = func() time.Time { return time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC) }
	pages := make([]domainsync.ActivityPage, 0)
	err := client.StreamActivities(context.Background(), []domainsync.ActivitySyncState{{
		AccountID: "ibkr-U1234567",
	}}, func(_ context.Context, page domainsync.ActivityPage) error {
		pages = append(pages, page)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 || requested[0] != [2]string{"20230801", "20240730"} || requested[1] != [2]string{"20240731", "20240731"} {
		t.Fatalf("unexpected windows: %#v", requested)
	}
	if len(pages) != 2 || pages[0].NextOffset != 365 || pages[0].Complete || !pages[1].Complete {
		t.Fatalf("unexpected checkpoints: %#v", pages)
	}
	if pages[1].FirstTransactionDate == nil || !pages[1].FirstTransactionDate.Equal(start) {
		t.Fatalf("empty history coverage marker missing: %#v", pages[1].FirstTransactionDate)
	}

	requested = nil
	pages = nil
	err = client.StreamActivities(context.Background(), []domainsync.ActivitySyncState{{
		AccountID: "ibkr-U1234567", NextOffset: 365,
	}}, func(_ context.Context, page domainsync.ActivityPage) error {
		pages = append(pages, page)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(requested) != 1 || requested[0] != [2]string{"20240731", "20240731"} {
		t.Fatalf("resume window = %#v", requested)
	}
}

func TestFlexClientSkipsActivityHistoryBeforeItsCadence(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		http.Error(writer, "Flex should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newFlexTestClient(t, server.URL, time.Date(2023, 8, 1, 0, 0, 0, 0, time.UTC))
	now := time.Date(2024, 8, 1, 12, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return now }
	first := time.Date(2023, 8, 3, 0, 0, 0, 0, time.UTC)
	last := now.Add(-time.Hour)
	called := false
	err := client.StreamActivities(context.Background(), []domainsync.ActivitySyncState{{
		AccountID: "ibkr-U1234567", InitialSyncCompleted: true,
		FirstTransactionDate: &first, LastSuccessfulSync: &last,
	}}, func(context.Context, domainsync.ActivityPage) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called || requests != 0 {
		t.Fatalf("history ran before it was due: sink=%v requests=%d", called, requests)
	}
}

func TestDecodeFlexReportRejectsWrongAccount(t *testing.T) {
	report, err := decodeFlexReport(strings.NewReader(`<FlexQueryResponse><FlexStatements><FlexStatement accountId="OTHER"><Trades><Trade tradeID="1"/></Trades></FlexStatement></FlexStatements></FlexQueryResponse>`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFlexAccount(report, "U1234567"); err == nil {
		t.Fatal("expected account mismatch")
	}
}

func TestValidateFlexSnapshotSectionsNamesEveryMissingQuerySection(t *testing.T) {
	report := flexReport{Elements: map[string]int{"OpenPositions": 1}}
	err := validateFlexSnapshotSections(report)
	if err == nil || !strings.Contains(err.Error(), "Cash Report") ||
		!strings.Contains(err.Error(), "Net Asset Value") || strings.Contains(err.Error(), "Open Positions") {
		t.Fatalf("missing section validation = %v", err)
	}
}

func TestFlexAPIRedactsTokenFromTransportErrors(t *testing.T) {
	api, err := newFlexAPI(
		"http://127.0.0.1:12345", "private-token", "123456", flexErrorDoer{},
		time.Second, time.Millisecond, time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.fetch(context.Background(), time.Now().AddDate(0, 0, -1), time.Now())
	if err == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(err.Error(), "private-token") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("token was not redacted: %v", err)
	}
}

func TestFlexStatementURLRejectsHTTPSDowngrade(t *testing.T) {
	api, err := newFlexAPI(
		"https://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService",
		"private-token", "123456", flexErrorDoer{}, time.Second, time.Millisecond, time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.statementURL("http://ndcdyn.interactivebrokers.com/GetStatement"); err == nil {
		t.Fatal("expected HTTPS-to-HTTP downgrade to be rejected")
	}
}

func TestFlexStatementURLAllowsSameLocalHTTPOrigin(t *testing.T) {
	api, err := newFlexAPI(
		"http://127.0.0.1:12345/FlexWebService",
		"private-token", "123456", flexErrorDoer{}, time.Second, time.Millisecond, time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := api.statementURL("http://127.0.0.1:12345/GetStatement")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.String() != "http://127.0.0.1:12345/GetStatement" {
		t.Fatalf("statement URL = %q", parsed.String())
	}
}

func TestMapFlexReportCoversNonTradeActivities(t *testing.T) {
	report := flexReport{Accounts: map[string]struct{}{"U1234567": {}}, Records: []flexRecord{
		{Kind: "CorporateAction", Attrs: map[string]string{
			"accountid": "U1234567", "date": "2024-01-02", "type": "Split",
			"description": "ACME split 4 FOR 1", "transactionid": "corp-1", "symbol": "ACME", "assetcategory": "STK",
		}},
		{Kind: "Transfer", Attrs: map[string]string{
			"accountid": "U1234567", "date": "2024-01-03", "direction": "IN",
			"transactionid": "transfer-1", "symbol": "ACME", "assetcategory": "STK", "quantity": "4",
		}},
		{Kind: "OptionEAE", Attrs: map[string]string{
			"accountid": "U1234567", "date": "2024-01-04", "transactiontype": "Expiration",
			"tradeid": "option-1", "symbol": "ACME  240104C00100000", "assetcategory": "OPT",
			"strike": "100", "expiry": "20240104", "putcall": "C", "underlyingsymbol": "ACME",
		}},
		{Kind: "TransactionTax", Attrs: map[string]string{
			"accountid": "U1234567", "date": "2024-01-05", "transactionid": "tax-1",
			"taxamount": "-2.5", "currency": "USD", "taxdescription": "Transaction tax",
		}},
		{Kind: "DebitCardActivity", Attrs: map[string]string{
			"transactiondatetime": "2024-01-06 12:00:00", "amount": "-20", "category": "Purchase",
		}},
	}}
	result := mapFlexReport(report, "U1234567", "USD")
	if result.Skipped != 0 || len(result.Activities) != 5 {
		t.Fatalf("mapping result = %#v", result)
	}
	wantTypes := []brokerage.ActivityType{
		brokerage.ActivitySplit, brokerage.ActivityTransferIn, brokerage.ActivityOptionExpiry,
		brokerage.ActivityTax, brokerage.ActivityWithdrawal,
	}
	for index, want := range wantTypes {
		if got := result.Activities[index].Type; got != want {
			t.Fatalf("activity %d type = %s, want %s", index, got, want)
		}
	}
	if result.Activities[0].Amount != 4 {
		t.Fatalf("split ratio = %v", result.Activities[0].Amount)
	}
}

func TestMapFlexReportMapsPositiveFeeCashTransactionAsRefund(t *testing.T) {
	report := flexReport{Accounts: map[string]struct{}{"U1234567": {}}, Records: []flexRecord{
		{Kind: "CashTransaction", Attrs: map[string]string{
			"accountid": "U1234567", "date": "2024-01-02", "transactionid": "fee-charge",
			"type": "Other Fees", "amount": "-0.16", "currency": "EUR", "description": "Market data fee",
		}},
		{Kind: "CashTransaction", Attrs: map[string]string{
			"accountid": "U1234567", "date": "2024-01-03", "transactionid": "fee-refund",
			"type": "Other Fees", "amount": "0.16", "currency": "EUR", "description": "Market data fee refund",
		}},
	}}

	mapped := mapFlexReport(report, "U1234567", "USD")
	if len(mapped.Activities) != 2 || mapped.Activities[0].Type != brokerage.ActivityFee ||
		mapped.Activities[1].Type != brokerage.ActivityCredit || mapped.Activities[1].Subtype != "FEE_REFUND" {
		t.Fatalf("fee charge/refund mapping = %#v", mapped.Activities)
	}
}

func newFlexTestClient(t *testing.T, baseURL string, historyStart time.Time) *FlexClient {
	t.Helper()
	client, err := NewFlex("U1234567", config.IBKRFlexConfig{
		Enabled: true, Token: "secret-token", QueryID: "123456", BaseURL: baseURL,
		BaseCurrency: "USD", HistoryStartDate: historyStart, SyncInterval: 24 * time.Hour,
		IncrementalOverlap: 7 * 24 * time.Hour, RequestTimeout: time.Second,
		PollInterval: time.Millisecond, PollTimeout: time.Second,
	}, zerolog.Nop(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}
