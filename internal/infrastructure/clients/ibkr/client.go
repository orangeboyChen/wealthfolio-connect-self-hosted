// Package ibkr implements a BrokerClient that talks directly to a locally
// running Interactive Brokers Gateway (or TWS) via the github.com/scmhub/ibapi
// SDK.
//
// IB's API is asynchronous and event-driven: every request triggers a
// stream of callbacks ending in a sentinel like PositionEnd /
// AccountSummaryEnd. We translate that into a synchronous Fetch() by
// implementing a tiny EWrapper that buffers callbacks and signals
// completion through channels.
package ibkr

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scmhub/ibapi"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
)

// Connector abstracts the IB SDK so unit tests can plug in a fake.
type Connector interface {
	Fetch(ctx context.Context, host string, port int, clientID int64, accountFilter string) (RawSnapshot, error)
}

// RawSnapshot is the per-account raw payload returned by the gateway,
// before brokerage-domain translation. Public for testability.
type RawSnapshot struct {
	Accounts  map[string]*RawAccount // keyed by IB account id (e.g. "DU1234567")
	Positions []RawPosition
}

// RawAccount holds the subset of AccountSummary tags we care about, all in
// the account's native currency.
type RawAccount struct {
	AccountID      string
	NetLiquidation float64
	TotalCash      float64
	BuyingPower    float64
	UnrealizedPnL  float64
	RealizedPnL    float64
	Currency       string
}

// RawPosition is one position row.
type RawPosition struct {
	Account  string
	Symbol   string
	SecType  string // STK, OPT, FUT, CASH, ...
	Exchange string
	Currency string
	Quantity float64
	AvgCost  float64
}

// Client is the IBKR BrokerClient.
type Client struct {
	host          string
	port          int
	clientID      int64
	accountFilter string // empty means "all accounts"
	conn          Connector
}

// New builds a client. Pass nil connector to use a real IB Gateway connection.
func New(host string, port int, clientID int64, accountFilter string, c Connector) *Client {
	if c == nil {
		c = realConnector{}
	}
	return &Client{
		host:          host,
		port:          port,
		clientID:      clientID,
		accountFilter: accountFilter,
		conn:          c,
	}
}

// ID returns the slug used by sync orchestration.
func (c *Client) ID() string { return "ibkr" }

// Fetch opens a single IB Gateway session, requests every authorized
// account's summary + positions, and translates the result.
func (c *Client) Fetch(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	if c.host == "" || c.port == 0 {
		return domainsync.BrokerSnapshot{}, errors.New("ibkr: gateway host/port not configured")
	}
	raw, err := c.conn.Fetch(ctx, c.host, c.port, c.clientID, c.accountFilter)
	if err != nil {
		return domainsync.BrokerSnapshot{}, err
	}
	return Translate(raw), nil
}

// Translate converts the raw gateway payload into a BrokerSnapshot. Public
// so unit tests can drive the mapping without touching the network.
func Translate(raw RawSnapshot) domainsync.BrokerSnapshot {
	now := time.Now().UTC()

	connection := brokerage.Connection{
		ID:              "ibkr-conn",
		AuthorizationID: "ibkr-auth",
		BrokerageName:   "Interactive Brokers",
		BrokerageSlug:   "ibkr",
		DisplayName:     "Interactive Brokers",
		Name:            "IBKR",
		Status:          brokerage.ConnectionActive,
		UpdatedAt:       now,
	}

	accounts := make([]brokerage.Account, 0, len(raw.Accounts))
	holdings := make([]brokerage.Holdings, 0, len(raw.Accounts))

	// Group positions by account.
	posByAcc := map[string][]RawPosition{}
	for _, p := range raw.Positions {
		posByAcc[p.Account] = append(posByAcc[p.Account], p)
	}

	for accID, summary := range raw.Accounts {
		cur := summary.Currency
		if cur == "" {
			cur = "USD"
		}
		acc := brokerage.Account{
			ID:                     "ibkr-" + accID,
			Name:                   "IBKR " + accID,
			AccountNumber:          accID,
			Type:                   brokerage.AccountTypeMargin,
			RawType:                "MARGIN",
			Currency:               cur,
			BalanceTotal:           summary.NetLiquidation,
			BalanceCurrency:        cur,
			BrokerageAuthorization: "ibkr-auth",
			InstitutionName:        "Interactive Brokers",
			SyncEnabled:            true,
			IsPaper:                strings.HasPrefix(accID, "DU"),
			Status:                 "open",
			CreatedDate:            now,
			LastHoldingsSync:       &now,
			InitialHoldingsDone:    true,
		}
		accounts = append(accounts, acc)

		positions := make([]brokerage.Position, 0, len(posByAcc[accID]))
		for _, p := range posByAcc[accID] {
			if p.Quantity == 0 {
				continue
			}
			typeCode := "EQUITY"
			switch p.SecType {
			case "OPT":
				typeCode = "OPTION"
			case "FUT":
				typeCode = "FUTURE"
			case "CASH":
				typeCode = "FOREX"
			case "BOND":
				typeCode = "BOND"
			}
			ex := p.Exchange
			if ex == "" {
				ex = "SMART"
			}
			sym := formatSymbol(p.Symbol, p.Exchange, p.Currency)
			positions = append(positions, brokerage.Position{
				Symbol: brokerage.Symbol{
					Symbol:    sym,
					RawSymbol: p.Symbol,
					Name:      sym,
					Type:      brokerage.SymbolType{Code: typeCode, IsSupported: true, Description: typeCode},
					Exchange:  brokerage.Exchange{Code: ex},
					Currency:  brokerage.Currency{Code: p.Currency},
				},
				Units:                p.Quantity,
				AveragePurchasePrice: p.AvgCost,
				Currency:             brokerage.Currency{Code: p.Currency},
			})
		}

		holdings = append(holdings, brokerage.Holdings{
			AccountID: acc.ID,
			Balances: []brokerage.Balance{{
				Currency:    brokerage.Currency{Code: cur},
				Cash:        summary.TotalCash,
				BuyingPower: summary.BuyingPower,
			}},
			Positions:  positions,
			CapturedAt: now,
		})
	}

	return domainsync.BrokerSnapshot{
		Connection: connection,
		Accounts:   accounts,
		Holdings:   holdings,
		Activities: map[string][]brokerage.Activity{},
	}
}

// ===================== real IB Gateway connector =====================

// realConnector connects to a real IB Gateway / TWS. Each Fetch opens a
// fresh session, waits for the data, then disconnects.
type realConnector struct{}

// Fetch opens a fresh IB Gateway session, gathers the account summary and
// open positions, then disconnects.
func (realConnector) Fetch(ctx context.Context, host string, port int, clientID int64, accountFilter string) (RawSnapshot, error) {
	w := newGatherWrapper(accountFilter)
	client := ibapi.NewEClient(w)
	if err := client.Connect(host, port, clientID); err != nil {
		return RawSnapshot{}, fmt.Errorf("ibkr: connect: %w", err)
	}
	defer func() { _ = client.Disconnect() }() //nolint:errcheck // best-effort cleanup; deferred Disconnect errors are not actionable

	// Wait for the Gateway to signal readiness via NextValidID before
	// issuing any data requests. Without this, requests sent immediately
	// after Connect may be silently dropped.
	if err := w.waitReady(ctx); err != nil {
		return RawSnapshot{}, err
	}

	// AccountSummary tags we care about. "All" group means every linked account.
	tags := strings.Join([]string{
		ibapi.NetLiquidation,
		ibapi.TotalCashValue,
		ibapi.BuyingPower,
	}, ",")

	const summaryReqID int64 = 9001
	client.ReqAccountSummary(summaryReqID, "All", tags)
	if err := w.waitAccountSummary(ctx); err != nil {
		return RawSnapshot{}, err
	}
	client.CancelAccountSummary(summaryReqID)

	client.ReqPositions()
	if err := w.waitPositions(ctx); err != nil {
		return RawSnapshot{}, err
	}
	client.CancelPositions()

	return w.snapshot(), nil
}

// gatherWrapper implements the (very large) ibapi.EWrapper interface by
// embedding the SDK's default no-op Wrapper and overriding only the
// callbacks we care about. It buffers data in-memory and exposes channels
// to wait for the *End sentinels.
type gatherWrapper struct {
	ibapi.Wrapper // default no-op implementations for every other callback

	mu sync.Mutex

	accountFilter string
	accounts      map[string]*RawAccount
	positions     []RawPosition

	readyCh     chan struct{} // closed when NextValidID is received (API ready)
	readyOnce   sync.Once
	summaryDone chan struct{}
	summaryOnce sync.Once
	posDone     chan struct{}
	posOnce     sync.Once
	errCh       chan error
}

func newGatherWrapper(filter string) *gatherWrapper {
	return &gatherWrapper{
		accountFilter: filter,
		accounts:      map[string]*RawAccount{},
		readyCh:       make(chan struct{}),
		summaryDone:   make(chan struct{}),
		posDone:       make(chan struct{}),
		errCh:         make(chan error, 4),
	}
}

func (w *gatherWrapper) snapshot() RawSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := RawSnapshot{
		Accounts:  make(map[string]*RawAccount, len(w.accounts)),
		Positions: append([]RawPosition(nil), w.positions...),
	}
	for k, v := range w.accounts {
		acc := *v
		out.Accounts[k] = &acc
	}
	return out
}

func (w *gatherWrapper) waitReady(ctx context.Context) error {
	return waitOrErr(ctx, w.readyCh, w.errCh, "ibkr: API ready timeout (NextValidID not received)")
}

func (w *gatherWrapper) waitAccountSummary(ctx context.Context) error {
	return waitOrErr(ctx, w.summaryDone, w.errCh, "ibkr: AccountSummary timeout")
}

func (w *gatherWrapper) waitPositions(ctx context.Context) error {
	return waitOrErr(ctx, w.posDone, w.errCh, "ibkr: Positions timeout")
}

func waitOrErr(ctx context.Context, done <-chan struct{}, errCh <-chan error, label string) error {
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	select {
	case <-done:
		return nil
	case err := <-errCh:
		return err
	case <-timeout.C:
		return errors.New(label)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ----- Overridden EWrapper callbacks -----
//
// All overrides use POINTER receivers so that NewEClient(w *gatherWrapper)
// dispatches to our methods (ibapi.Wrapper itself uses value receivers, so
// by Go's method-set rules our *gatherWrapper methods take precedence).

func (w *gatherWrapper) acctOK(account string) bool {
	if w.accountFilter == "" {
		return true
	}
	return account == w.accountFilter
}

// NextValidID is called by the Gateway once the API session is fully ready.
// We use it as the signal that the connection is established and requests can
// be safely issued.
func (w *gatherWrapper) NextValidID(orderID int64) {
	w.readyOnce.Do(func() { close(w.readyCh) })
}

// AccountSummary records one summary tag for an account; called repeatedly by
// the IB Gateway client until AccountSummaryEnd.
func (w *gatherWrapper) AccountSummary(reqID int64, account, tag, value, currency string) {
	if !w.acctOK(account) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	a, ok := w.accounts[account]
	if !ok {
		a = &RawAccount{AccountID: account}
		w.accounts[account] = a
	}
	// AccountSummary tags are not delivered in deterministic order. Prefer
	// the currency reported alongside NetLiquidation as the canonical
	// account currency; fall back to the first non-empty value otherwise.
	switch {
	case tag == ibapi.NetLiquidation && currency != "":
		a.Currency = currency
	case a.Currency == "" && currency != "":
		a.Currency = currency
	}
	switch tag {
	case ibapi.NetLiquidation:
		a.NetLiquidation = atof(value)
	case ibapi.TotalCashValue:
		a.TotalCash = atof(value)
	case ibapi.BuyingPower:
		a.BuyingPower = atof(value)
	}
}

// AccountSummaryEnd signals that all summary tags have been delivered.
func (w *gatherWrapper) AccountSummaryEnd(reqID int64) {
	w.summaryOnce.Do(func() { close(w.summaryDone) })
}

// Position records a single open position from the IB Gateway feed.
func (w *gatherWrapper) Position(account string, contract *ibapi.Contract, position ibapi.Decimal, avgCost float64) {
	if !w.acctOK(account) {
		return
	}
	if contract == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.positions = append(w.positions, RawPosition{
		Account:  account,
		Symbol:   contract.Symbol,
		SecType:  contract.SecType,
		Exchange: contract.Exchange,
		Currency: contract.Currency,
		Quantity: position.Float(),
		AvgCost:  avgCost,
	})
}

// PositionEnd signals that all positions have been delivered.
func (w *gatherWrapper) PositionEnd() {
	w.posOnce.Do(func() { close(w.posDone) })
}

// Error is invoked for any IB-level error. We surface it to whichever wait
// is currently blocked.
func (w *gatherWrapper) Error(reqID int64, errorTime int64, errCode int64, errString string, advancedOrderRejectJSON string) {
	// IB Gateway sends a lot of informational "errors" with code < 2100 as
	// well as connection-status pings; only escalate genuine failures.
	if errCode < 2100 {
		select {
		case w.errCh <- fmt.Errorf("ibkr error %d: %s", errCode, errString):
		default:
		}
	}
}

// formatSymbol appends a market suffix to the raw IB symbol when needed.
// For Hong Kong stocks (exchange SEHK or currency HKD), appends ".HK".
func formatSymbol(symbol, exchange, currency string) string {
	if exchange == "SEHK" || currency == "HKD" {
		return symbol + ".HK"
	}
	return symbol
}

func atof(s string) float64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64) //nolint:errcheck // exchange returns numeric strings; treat unparsable as zero
	return v
}
