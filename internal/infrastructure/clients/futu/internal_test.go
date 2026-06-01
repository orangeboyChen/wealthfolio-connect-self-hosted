package futu

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/santsai/futu-go/pb"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
)

func TestFutuCurrency_AllBranches(t *testing.T) {
	cases := map[pb.Currency]string{
		pb.Currency_Currency_HKD:     "HKD",
		pb.Currency_Currency_USD:     "USD",
		pb.Currency_Currency_CNH:     "CNH",
		pb.Currency_Currency_JPY:     "JPY",
		pb.Currency_Currency_SGD:     "SGD",
		pb.Currency_Currency_AUD:     "AUD",
		pb.Currency_Currency_Unknown: "",
	}
	for in, want := range cases {
		if got := futuCurrency(in); got != want {
			t.Errorf("futuCurrency(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestPrimaryCurrency_AllBranches(t *testing.T) {
	cases := map[pb.TrdMarket]string{
		pb.TrdMarket_TrdMarket_HK:      "HKD",
		pb.TrdMarket_TrdMarket_HK_Fund: "HKD",
		pb.TrdMarket_TrdMarket_US:      "USD",
		pb.TrdMarket_TrdMarket_US_Fund: "USD",
		pb.TrdMarket_TrdMarket_CN:      "CNH",
		pb.TrdMarket_TrdMarket_HKCC:    "CNH",
		pb.TrdMarket_TrdMarket_SG:      "SGD",
		pb.TrdMarket_TrdMarket_JP:      "JPY",
		pb.TrdMarket_TrdMarket_Unknown: "HKD", // default
	}
	for in, want := range cases {
		if got := primaryCurrency(in); got != want {
			t.Errorf("primaryCurrency(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestMarketCode_AllBranches(t *testing.T) {
	cases := map[pb.TrdMarket]string{
		pb.TrdMarket_TrdMarket_HK:      "HKEX",
		pb.TrdMarket_TrdMarket_HK_Fund: "HKEX",
		pb.TrdMarket_TrdMarket_US:      "NASDAQ",
		pb.TrdMarket_TrdMarket_US_Fund: "NASDAQ",
		pb.TrdMarket_TrdMarket_CN:      "SSE",
		pb.TrdMarket_TrdMarket_HKCC:    "HKCC",
		pb.TrdMarket_TrdMarket_SG:      "SGX",
		pb.TrdMarket_TrdMarket_JP:      "TSE",
		pb.TrdMarket_TrdMarket_Unknown: "FUTU",
	}
	for in, want := range cases {
		if got := marketCode(in); got != want {
			t.Errorf("marketCode(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestMarketCurrency_AllBranches(t *testing.T) {
	cases := map[pb.TrdMarket]pb.Currency{
		pb.TrdMarket_TrdMarket_HK:      pb.Currency_Currency_HKD,
		pb.TrdMarket_TrdMarket_HK_Fund: pb.Currency_Currency_HKD,
		pb.TrdMarket_TrdMarket_US:      pb.Currency_Currency_USD,
		pb.TrdMarket_TrdMarket_US_Fund: pb.Currency_Currency_USD,
		pb.TrdMarket_TrdMarket_CN:      pb.Currency_Currency_CNH,
		pb.TrdMarket_TrdMarket_HKCC:    pb.Currency_Currency_CNH,
		pb.TrdMarket_TrdMarket_SG:      pb.Currency_Currency_SGD,
		pb.TrdMarket_TrdMarket_JP:      pb.Currency_Currency_JPY,
		pb.TrdMarket_TrdMarket_Unknown: pb.Currency_Currency_HKD, // default
	}
	for in, want := range cases {
		if got := marketCurrency(in); got != want {
			t.Errorf("marketCurrency(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestMarketName_AllBranches(t *testing.T) {
	cases := map[pb.TrdMarket]string{
		pb.TrdMarket_TrdMarket_HK:      "Hong Kong",
		pb.TrdMarket_TrdMarket_HK_Fund: "Hong Kong Fund",
		pb.TrdMarket_TrdMarket_US:      "US",
		pb.TrdMarket_TrdMarket_US_Fund: "US Fund",
		pb.TrdMarket_TrdMarket_CN:      "China A",
		pb.TrdMarket_TrdMarket_HKCC:    "Stock Connect",
		pb.TrdMarket_TrdMarket_SG:      "Singapore",
		pb.TrdMarket_TrdMarket_JP:      "Japan",
		pb.TrdMarket_TrdMarket_Unknown: "Futu",
	}
	for in, want := range cases {
		if got := marketName(in); got != want {
			t.Errorf("marketName(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFutuAccType_AllBranches(t *testing.T) {
	cases := map[pb.TrdAccType]brokerage.AccountType{
		pb.TrdAccType_TrdAccType_Cash:    brokerage.AccountTypeCash,
		pb.TrdAccType_TrdAccType_Margin:  brokerage.AccountTypeMargin,
		pb.TrdAccType_TrdAccType_Unknown: brokerage.AccountTypeSecurities,
	}
	for in, want := range cases {
		if got := futuAccType(in); got != want {
			t.Errorf("futuAccType(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildBalances_NilFundsReturnsNil(t *testing.T) {
	if got := buildBalances(nil); got != nil {
		t.Fatalf("expected nil for nil funds, got %+v", got)
	}
}

func TestBuildBalances_UnknownCurrencyDefaultsToHKD(t *testing.T) {
	cur := pb.Currency_Currency_Unknown
	total, cash := 0.0, 100.0
	f := &pb.Funds{Currency: &cur, TotalAssets: &total, Cash: &cash}
	out := buildBalances(f)
	if len(out) != 1 || out[0].Currency.Code != "HKD" {
		t.Fatalf("expected HKD fallback, got %+v", out)
	}
}

func TestBuildPositions_FiltersAndFallsBackCurrency(t *testing.T) {
	zeroQty := 0.0
	skipped := mkPositionInternal("X", "", zeroQty, 0, 0, pb.Currency_Currency_HKD)

	unknownCur := pb.Currency_Currency_Unknown
	qty := 10.0
	price := 1.0
	cost := 1.0
	posCurFallback := &pb.Position{
		Code: stringPtr("AAPL"), Name: stringPtr("Apple"),
		Qty: &qty, Price: &price, CostPrice: &cost, Currency: &unknownCur,
	}

	out := buildPositions([]*pb.Position{nil, skipped, posCurFallback}, pb.TrdMarket_TrdMarket_US)
	if len(out) != 1 {
		t.Fatalf("expected 1 position, got %d", len(out))
	}
	if out[0].Symbol.Currency.Code != "USD" {
		t.Errorf("expected USD fallback from market, got %q", out[0].Symbol.Currency.Code)
	}
}

func TestPickMarket_AllBranches(t *testing.T) {
	if pickMarket(nil) != pb.TrdMarket_TrdMarket_HK {
		t.Fatal("nil auths should default to HK")
	}
	// HK wins over US and CN due to priority.
	hkUS := []pb.TrdMarket{
		pb.TrdMarket_TrdMarket_US,
		pb.TrdMarket_TrdMarket_HK,
	}
	if pickMarket(hkUS) != pb.TrdMarket_TrdMarket_HK {
		t.Fatal("HK should win priority")
	}
	// US wins when HK absent.
	if pickMarket([]pb.TrdMarket{pb.TrdMarket_TrdMarket_US}) != pb.TrdMarket_TrdMarket_US {
		t.Fatal("US should win when alone")
	}
	// SG wins when HK/US/CN absent.
	if pickMarket([]pb.TrdMarket{pb.TrdMarket_TrdMarket_SG}) != pb.TrdMarket_TrdMarket_SG {
		t.Fatal("SG should win when alone")
	}
	// Unknown market not in priority → falls back to first auth value.
	odd := []pb.TrdMarket{pb.TrdMarket_TrdMarket_HK_Fund}
	if pickMarket(odd) != pb.TrdMarket_TrdMarket_HK_Fund {
		t.Fatalf("expected first-auth fallback, got %v", pickMarket(odd))
	}
}

func TestHeaderFor(t *testing.T) {
	a := Account{
		AccID:     281000999,
		TrdEnv:    pb.TrdEnv_TrdEnv_Real,
		TrdMarket: pb.TrdMarket_TrdMarket_HK,
	}
	h := headerFor(a)
	if h == nil || h.AccID == nil || *h.AccID != a.AccID {
		t.Fatalf("AccID not propagated: %+v", h)
	}
	if h.TrdEnv == nil || *h.TrdEnv != a.TrdEnv {
		t.Fatal("TrdEnv not propagated")
	}
	if h.TrdMarket == nil || *h.TrdMarket != a.TrdMarket {
		t.Fatal("TrdMarket not propagated")
	}
}

func TestNew_NilDialerUsesRealDialer(t *testing.T) {
	c := New("h", 1, "p", "id", nil, nil)
	if _, ok := c.dialer.(realDialer); !ok {
		t.Fatalf("expected realDialer, got %T", c.dialer)
	}
}

// TestRealDialer_FailsToConnectQuickly exercises the realDialer path
// without standing up an OpenD daemon. Port 1 is closed on every reasonable
// host, so the underlying TCP dial fails immediately.
func TestRealDialer_FailsToConnectQuickly(t *testing.T) {
	d := realDialer{connectionID: "x"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := d.Dial(ctx, "127.0.0.1", 1); err == nil {
		t.Fatal("expected dial error against closed port")
	}
}

func TestSetLogger(t *testing.T) {
	c := New("h", 1, "p", "id", nil, nil)
	c.SetLogger(zerolog.Nop())
	// No panic means success.
}

func TestMarketSlug_AllBranches(t *testing.T) {
	cases := map[pb.TrdMarket]string{
		pb.TrdMarket_TrdMarket_HK:      "hk",
		pb.TrdMarket_TrdMarket_US:      "us",
		pb.TrdMarket_TrdMarket_CN:      "cn",
		pb.TrdMarket_TrdMarket_SG:      "sg",
		pb.TrdMarket_TrdMarket_JP:      "jp",
		pb.TrdMarket_TrdMarket_HK_Fund: "hk_fund",
		pb.TrdMarket_TrdMarket_US_Fund: "us_fund",
	}
	for in, want := range cases {
		if got := marketSlug(in); got != want {
			t.Errorf("marketSlug(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFutuTrdSide_AllBranches(t *testing.T) {
	type result struct {
		actType brokerage.ActivityType
		raw     string
		ok      bool
	}
	cases := map[pb.TrdSide]result{
		pb.TrdSide_TrdSide_Buy:       {brokerage.ActivityBuy, "BUY", true},
		pb.TrdSide_TrdSide_Sell:      {brokerage.ActivitySell, "SELL", true},
		pb.TrdSide_TrdSide_SellShort: {brokerage.ActivitySell, "SELL_SHORT", true},
		pb.TrdSide_TrdSide_BuyBack:   {brokerage.ActivityBuy, "BUY_BACK", true},
		pb.TrdSide_TrdSide_Unknown:   {"", "", false},
	}
	for in, want := range cases {
		at, raw, ok := futuTrdSide(in)
		if at != want.actType || raw != want.raw || ok != want.ok {
			t.Errorf("futuTrdSide(%v) = (%v, %q, %v), want (%v, %q, %v)",
				in, at, raw, ok, want.actType, want.raw, want.ok)
		}
	}
}

func TestDealTimestamp_Unix(t *testing.T) {
	ts := float64(1700000000)
	f := &pb.OrderFill{CreateTimestamp: &ts}
	got := dealTimestamp(f)
	if got.Unix() != 1700000000 {
		t.Errorf("expected unix 1700000000, got %d", got.Unix())
	}
}

func TestDealTimestamp_StringFormat(t *testing.T) {
	s := "2024-01-15 09:30:00"
	f := &pb.OrderFill{CreateTime: &s}
	got := dealTimestamp(f)
	if got.Year() != 2024 || got.Month() != 1 || got.Day() != 15 {
		t.Errorf("expected 2024-01-15, got %v", got)
	}
}

func TestDealTimestamp_StringFormatWithMS(t *testing.T) {
	s := "2024-01-15 09:30:00.123"
	f := &pb.OrderFill{CreateTime: &s}
	got := dealTimestamp(f)
	if got.Year() != 2024 || got.Month() != 1 || got.Day() != 15 {
		t.Errorf("expected 2024-01-15, got %v", got)
	}
}

func TestDealTimestamp_Fallback(t *testing.T) {
	f := &pb.OrderFill{}
	got := dealTimestamp(f)
	if time.Since(got) > time.Minute {
		t.Errorf("expected ~now, got %v", got)
	}
}

func TestDealToActivity_NilFill(t *testing.T) {
	_, ok := dealToActivity(nil, "acc1", pb.TrdMarket_TrdMarket_HK)
	if ok {
		t.Fatal("expected false for nil fill")
	}
}

func TestDealToActivity_ZeroQty(t *testing.T) {
	qty := 0.0
	f := &pb.OrderFill{Qty: &qty}
	_, ok := dealToActivity(f, "acc1", pb.TrdMarket_TrdMarket_HK)
	if ok {
		t.Fatal("expected false for zero qty")
	}
}

func TestDealToActivity_UnknownSide(t *testing.T) {
	qty := 10.0
	side := pb.TrdSide_TrdSide_Unknown
	f := &pb.OrderFill{Qty: &qty, TrdSide: &side}
	_, ok := dealToActivity(f, "acc1", pb.TrdMarket_TrdMarket_HK)
	if ok {
		t.Fatal("expected false for unknown side")
	}
}

func TestDealToActivity_FallbackFillID(t *testing.T) {
	qty := 10.0
	price := 100.0
	side := pb.TrdSide_TrdSide_Buy
	fillID := uint64(999)
	code := "00700"
	name := "Tencent"
	f := &pb.OrderFill{
		Qty: &qty, Price: &price, TrdSide: &side,
		FillID: &fillID, Code: &code, Name: &name,
	}
	a, ok := dealToActivity(f, "acc1", pb.TrdMarket_TrdMarket_HK)
	if !ok {
		t.Fatal("expected ok")
	}
	if a.SourceRecordID != "999" {
		t.Errorf("expected source record ID 999, got %q", a.SourceRecordID)
	}
}

func TestDealToActivity_NoIDs(t *testing.T) {
	qty := 10.0
	side := pb.TrdSide_TrdSide_Buy
	f := &pb.OrderFill{Qty: &qty, TrdSide: &side}
	_, ok := dealToActivity(f, "acc1", pb.TrdMarket_TrdMarket_HK)
	if ok {
		t.Fatal("expected false when no fill ID")
	}
}

func TestDealToActivity_Success(t *testing.T) {
	qty := 10.0
	price := 320.0
	side := pb.TrdSide_TrdSide_Buy
	code := "00700"
	name := "Tencent"
	fillIDEx := "fill-abc"
	f := &pb.OrderFill{
		Qty: &qty, Price: &price, TrdSide: &side,
		FillIDEx: &fillIDEx, Code: &code, Name: &name,
	}
	a, ok := dealToActivity(f, "acc1", pb.TrdMarket_TrdMarket_HK)
	if !ok {
		t.Fatal("expected ok")
	}
	if a.Type != brokerage.ActivityBuy {
		t.Errorf("expected BUY, got %v", a.Type)
	}
	if a.Units != 10 {
		t.Errorf("expected 10 units, got %v", a.Units)
	}
	if a.Symbol.Symbol != "00700.HK" {
		t.Errorf("expected 00700.HK, got %q", a.Symbol.Symbol)
	}
	if a.Currency.Code != "HKD" {
		t.Errorf("expected HKD, got %q", a.Currency.Code)
	}
}

// helpers

func stringPtr(s string) *string { return &s }

func mkPositionInternal(code, name string, qty, price, cost float64, cur pb.Currency) *pb.Position {
	c := cur
	return &pb.Position{
		Code: &code, Name: &name, Qty: &qty, Price: &price,
		CostPrice: &cost, Currency: &c,
	}
}
