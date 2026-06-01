package ibkr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/scmhub/ibapi"
)

func TestAtof(t *testing.T) {
	cases := map[string]float64{
		"":        0,
		"1.5":     1.5,
		"not-a-#": 0, // ParseFloat error swallowed by design
		"  1e3  ": 0, // surrounding whitespace also fails ParseFloat
		"42":      42,
	}
	for in, want := range cases {
		if got := atof(in); got != want {
			t.Errorf("atof(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAcctOK(t *testing.T) {
	w := newGatherWrapper("")
	if !w.acctOK("ANY") {
		t.Fatal("empty filter should accept any account")
	}
	w2 := newGatherWrapper("U1")
	if !w2.acctOK("U1") {
		t.Fatal("matching filter should accept")
	}
	if w2.acctOK("U2") {
		t.Fatal("non-matching filter should reject")
	}
}

func TestAccountSummary_FiltersAndAggregates(t *testing.T) {
	w := newGatherWrapper("U1")

	// Filtered out: different account.
	w.AccountSummary(1, "U2", ibapi.NetLiquidation, "100", "USD")

	// First tag with NetLiquidation sets the canonical currency.
	w.AccountSummary(1, "U1", ibapi.NetLiquidation, "1000", "USD")
	w.AccountSummary(1, "U1", ibapi.TotalCashValue, "200", "USD")
	w.AccountSummary(1, "U1", ibapi.BuyingPower, "500", "USD")
	// Unknown tag is ignored gracefully.
	w.AccountSummary(1, "U1", "SomeRandomTag", "9", "USD")

	w.AccountSummaryEnd(1)
	// Idempotent close — must not panic on second call.
	w.AccountSummaryEnd(1)

	snap := w.snapshot()
	if _, ok := snap.Accounts["U2"]; ok {
		t.Fatal("filtered account leaked through")
	}
	a := snap.Accounts["U1"]
	if a == nil {
		t.Fatal("expected U1 in snapshot")
	}
	if a.NetLiquidation != 1000 || a.TotalCash != 200 || a.BuyingPower != 500 {
		t.Errorf("unexpected aggregates: %+v", a)
	}
	if a.Currency != "USD" {
		t.Errorf("currency = %q, want USD", a.Currency)
	}
}

func TestAccountSummary_FallbackCurrency(t *testing.T) {
	w := newGatherWrapper("")
	// First tag is *not* NetLiquidation, so currency falls back to first
	// non-empty value.
	w.AccountSummary(1, "U1", ibapi.TotalCashValue, "10", "EUR")
	if w.accounts["U1"].Currency != "EUR" {
		t.Errorf("expected EUR fallback, got %q", w.accounts["U1"].Currency)
	}
	// Subsequent NetLiquidation with currency overrides.
	w.AccountSummary(1, "U1", ibapi.NetLiquidation, "100", "USD")
	if w.accounts["U1"].Currency != "USD" {
		t.Errorf("NetLiquidation should override, got %q", w.accounts["U1"].Currency)
	}
}

func TestPosition_SkipsNilContractAndFiltered(t *testing.T) {
	w := newGatherWrapper("U1")

	// nil contract → ignored (no panic).
	w.Position("U1", nil, ibapi.Decimal{}, 0)

	// Filtered account.
	w.Position("U2", &ibapi.Contract{Symbol: "X"}, ibapi.Decimal{}, 0)

	// Accepted.
	w.Position("U1", &ibapi.Contract{
		Symbol:   "AAPL",
		SecType:  "STK",
		Exchange: "NASDAQ",
		Currency: "USD",
	}, ibapi.Decimal{}, 150)

	w.PositionEnd()
	w.PositionEnd() // idempotent

	snap := w.snapshot()
	if len(snap.Positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(snap.Positions))
	}
	if snap.Positions[0].Symbol != "AAPL" {
		t.Errorf("unexpected symbol: %q", snap.Positions[0].Symbol)
	}
}

func TestError_OnlySurfacesGenuineFailures(t *testing.T) {
	w := newGatherWrapper("")

	// Informational (>= 2100) is swallowed.
	w.Error(1, 0, 2104, "Market data farm connection is OK", "")
	select {
	case e := <-w.errCh:
		t.Fatalf("informational error leaked: %v", e)
	default:
	}

	// Genuine failure is forwarded.
	w.Error(1, 0, 502, "couldn't connect", "")
	select {
	case err := <-w.errCh:
		if err == nil {
			t.Fatal("expected non-nil error")
		}
	default:
		t.Fatal("expected genuine error to be forwarded")
	}
}

func TestError_NonBlockingWhenChannelFull(t *testing.T) {
	w := newGatherWrapper("")
	// Saturate the buffered errCh (cap=4).
	for i := 0; i < cap(w.errCh); i++ {
		w.errCh <- errors.New("filler")
	}
	// One more must NOT block thanks to the default branch.
	done := make(chan struct{})
	go func() {
		w.Error(1, 0, 500, "drop me", "")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Error blocked when errCh was full")
	}
}

func TestWaitOrErr_Done(t *testing.T) {
	done := make(chan struct{})
	close(done)
	if err := waitOrErr(context.Background(), done, make(chan error), "x"); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestWaitOrErr_Error(t *testing.T) {
	errCh := make(chan error, 1)
	errCh <- errors.New("boom")
	if err := waitOrErr(context.Background(), make(chan struct{}), errCh, "x"); err == nil ||
		err.Error() != "boom" {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestWaitOrErr_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitOrErr(ctx, make(chan struct{}), make(chan error), "x")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestNextValidID_SignalsReady(t *testing.T) {
	w := newGatherWrapper("")
	w.NextValidID(1)
	// Must not block — readyCh is closed.
	if err := w.waitReady(context.Background()); err != nil {
		t.Fatalf("waitReady after NextValidID: %v", err)
	}
	// Idempotent — must not panic on second call.
	w.NextValidID(2)
}

func TestGatherWrapper_WaitMethods(t *testing.T) {
	w := newGatherWrapper("")
	// Closing the done channel makes waitReady/waitAccountSummary return nil.
	close(w.readyCh)
	if err := w.waitReady(context.Background()); err != nil {
		t.Fatalf("waitReady: %v", err)
	}
	close(w.summaryDone)
	if err := w.waitAccountSummary(context.Background()); err != nil {
		t.Fatalf("waitAccountSummary: %v", err)
	}
	close(w.posDone)
	if err := w.waitPositions(context.Background()); err != nil {
		t.Fatalf("waitPositions: %v", err)
	}
}

func TestRealConnector_FailsToConnectQuickly(t *testing.T) {
	// Port 1 is reserved/closed on every reasonable host. The real IB SDK
	// will surface a connection error fast — this exercises the error path
	// of realConnector.Fetch without standing up a fake gateway.
	rc := realConnector{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := rc.Fetch(ctx, "127.0.0.1", 1, 1, "")
	if err == nil {
		t.Fatal("expected connect error")
	}
}

func TestNew_NilConnectorUsesRealConnector(t *testing.T) {
	c := New("h", 4001, 1, "", nil)
	if _, ok := c.conn.(realConnector); !ok {
		t.Fatalf("expected realConnector, got %T", c.conn)
	}
}
