// Package futu implements a BrokerClient that talks directly to a locally
// running Futu OpenD daemon via the github.com/santsai/futu-go SDK.
//
// Connection model:
//
//  1. Each Fetch() opens a fresh TCP+protobuf session to OpenD, calls
//     UnlockTrade with the user's MD5'd trading password, lists every
//     authorized account via GetAccList, then iterates accounts to pull
//     Funds and Positions.
//  2. The session is closed when Fetch returns. OpenD sessions are cheap
//     and the sync interval (hours) makes long-lived connections more
//     trouble than they are worth.
package futu

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	futugo "github.com/santsai/futu-go"
	"github.com/santsai/futu-go/pb"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
)

// Dialer abstracts the OpenD session so unit tests can plug in a fake.
// The real implementation in dialOpenD wraps go-futu-api.
type Dialer interface {
	Dial(ctx context.Context, host string, port int) (Session, error)
}

// Session models a single live OpenD connection. It is a small wrapper
// around the SDK so we can swap it out in tests.
type Session interface {
	Unlock(ctx context.Context, password string) error
	ListAccounts(ctx context.Context) ([]Account, error)
	Funds(ctx context.Context, acc Account) (*pb.Funds, error)
	Positions(ctx context.Context, acc Account) ([]*pb.Position, error)
	// HistoryDeals returns historical order fills ("成交") within [begin, end].
	// Times must be formatted as "YYYY-MM-DD HH:MM:SS" — the format Futu OpenD
	// requires. Implementations should hard-fail on a missing window because
	// OpenD returns an error otherwise.
	HistoryDeals(ctx context.Context, acc Account, begin, end time.Time) ([]*pb.OrderFill, error)
	Close(ctx context.Context) error
}

// Account is a small projection of pb.TrdAcc that callers (and tests)
// can construct without depending on protobuf details.
type Account struct {
	AccID     uint64
	TrdEnv    pb.TrdEnv     // simulate vs real
	TrdMarket pb.TrdMarket  // HK / US / CN / SG ...
	AccType   pb.TrdAccType // cash vs margin
	CardNum   string        // human-friendly id, e.g. "281000123"
}

// Client is the Futu BrokerClient.
type Client struct {
	host         string
	port         int
	password     string // pre-computed MD5 hex of the trade password
	connectionID string
	rsaKey       []byte // PEM-encoded RSA private key for encrypted OpenD sessions
	dialer       Dialer
	log          zerolog.Logger
}

// New builds a client. Pass nil dialer to use a real OpenD connection.
// rsaKey may be nil when OpenD does not require RSA encryption.
func New(host string, port int, password, connectionID string, rsaKey []byte, d Dialer) *Client {
	if d == nil {
		d = realDialer{connectionID: connectionID, rsaKey: rsaKey}
	}
	return &Client{
		host:         host,
		port:         port,
		password:     password,
		connectionID: connectionID,
		rsaKey:       rsaKey,
		dialer:       d,
		log:          zerolog.Nop(),
	}
}

// SetLogger attaches a structured logger for operational visibility.
func (c *Client) SetLogger(log zerolog.Logger) {
	c.log = log
}

// ID returns the slug used by sync orchestration.
func (c *Client) ID() string { return "futu" }

// historyWindowDays bounds the trade history we ask OpenD for. Futu enforces
// a 90-day cap per call; longer ranges return an error. Going back further
// would require pagination by sliding window, which we leave as a TODO once
// real users hit the limit.
const historyWindowDays = 90

// currencyHKD is the default currency used by Futu when an account or position
// does not expose an explicit currency (Hong Kong securities are the dominant
// case so HKD is the safest fallback).
const currencyHKD = "HKD"

// Fetch establishes one OpenD session, pulls funds + positions for every
// authorized account and returns a fully translated BrokerSnapshot.
func (c *Client) Fetch(ctx context.Context) (snap domainsync.BrokerSnapshot, err error) {
	// Guard against nil-pointer panics inside the SDK (e.g. when the
	// connection drops mid-request and internal state becomes nil).
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("futu: panic recovered: %v", r)
			c.log.Error().Interface("panic", r).Msg("recovered panic during Fetch")
		}
	}()

	if c.host == "" || c.port == 0 {
		return domainsync.BrokerSnapshot{}, errors.New("futu: OpenD host/port not configured")
	}
	// Apply a connect timeout so we don't block forever when OpenD is down.
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	sess, err := c.dialer.Dial(dialCtx, c.host, c.port)
	if err != nil {
		return domainsync.BrokerSnapshot{}, fmt.Errorf("futu: dial OpenD: %w", err)
	}
	c.log.Info().Str("host", c.host).Int("port", c.port).Msg("OpenD connected")
	defer func() { _ = sess.Close(ctx) }() //nolint:errcheck // best-effort cleanup; deferred Close errors are not actionable

	// Unlock is mandatory before every Get* call that touches an account.
	// Skipping unlock returns "trade unlock required" from OpenD.
	if c.password != "" {
		c.log.Info().Msg("unlocking trade session")
		if unlockErr := sess.Unlock(ctx, c.password); unlockErr != nil {
			return domainsync.BrokerSnapshot{}, fmt.Errorf("futu: unlock trade: %w", unlockErr)
		}
		c.log.Info().Msg("trade session unlocked")
	} else {
		c.log.Warn().Msg("FUTU_TRADE_PASSWORD is empty, skipping unlock — real accounts will return empty data")
	}

	accs, err := sess.ListAccounts(ctx)
	if err != nil {
		return domainsync.BrokerSnapshot{}, fmt.Errorf("futu: list accounts: %w", err)
	}

	c.log.Info().Int("total", len(accs)).Msg("accounts discovered")
	for i, a := range accs {
		c.log.Info().
			Int("idx", i).
			Uint64("accID", a.AccID).
			Str("env", a.TrdEnv.String()).
			Str("market", a.TrdMarket.String()).
			Str("type", a.AccType.String()).
			Str("card", a.CardNum).
			Msg("account")
	}

	now := time.Now().UTC()
	windowStart := now.Add(-historyWindowDays * 24 * time.Hour)

	data := make([]AccountSnapshot, 0, len(accs))
	for _, a := range accs {
		c.log.Info().
			Uint64("accID", a.AccID).
			Str("env", a.TrdEnv.String()).
			Str("market", a.TrdMarket.String()).
			Msg("fetching account data")
		funds, fErr := sess.Funds(ctx, a)
		if fErr != nil {
			c.log.Warn().Err(fErr).Uint64("accID", a.AccID).Msg("Funds failed")
		} else {
			c.log.Info().
				Uint64("accID", a.AccID).
				Str("reqCurrency", marketCurrency(a.TrdMarket).String()).
				Float64("totalAssets", funds.GetTotalAssets()).
				Float64("cash", funds.GetCash()).
				Float64("marketVal", funds.GetMarketVal()).
				Int32("currency", int32(funds.GetCurrency())).
				Str("raw", funds.String()).
				Msg("Funds ok")
		}
		positions, pErr := sess.Positions(ctx, a)
		if pErr != nil {
			c.log.Warn().Err(pErr).Uint64("accID", a.AccID).Msg("Positions failed")
		} else {
			c.log.Info().Uint64("accID", a.AccID).Int("count", len(positions)).Msg("Positions ok")
			for i, p := range positions {
				c.log.Debug().
					Uint64("accID", a.AccID).
					Int("idx", i).
					Str("code", p.GetCode()).
					Str("name", p.GetName()).
					Float64("qty", p.GetQty()).
					Float64("costPrice", p.GetCostPrice()).
					Float64("price", p.GetPrice()).
					Float64("val", p.GetVal()).
					Msg("position")
			}
		}
		if fErr != nil && pErr != nil {
			// Skip accounts where both calls failed but keep the others.
			continue
		}
		// Skip accounts with no assets and no positions.
		if (funds == nil || funds.GetTotalAssets() == 0) && len(positions) == 0 {
			continue
		}
		// Trade history is best-effort: simulated accounts and accounts
		// without trading authority will return errors here. We swallow
		// the error and translate with an empty fill list, mirroring how
		// the snapshot already tolerates fund/position partial failures.
		deals, dErr := sess.HistoryDeals(ctx, a, windowStart, now)
		if dErr != nil {
			deals = nil
		}
		data = append(
			data, AccountSnapshot{
				Account:   a,
				Funds:     funds,
				Positions: positions,
				Deals:     deals,
			},
		)
	}

	return Translate(data), nil
}

// AccountSnapshot bundles all per-account upstream data the translator needs.
type AccountSnapshot struct {
	Account   Account
	Funds     *pb.Funds
	Positions []*pb.Position
	Deals     []*pb.OrderFill
}

// Translate converts every per-account upstream payload into one BrokerSnapshot
// with one brokerage.Account per Futu trading account. Public for testability.
func Translate(snaps []AccountSnapshot) domainsync.BrokerSnapshot {
	now := time.Now().UTC()

	connection := brokerage.Connection{
		ID:              "futu-conn",
		AuthorizationID: "futu-auth",
		BrokerageName:   "Futu Securities",
		BrokerageSlug:   "futu",
		DisplayName:     "Futu Securities",
		Name:            "Futu",
		Status:          brokerage.ConnectionActive,
		UpdatedAt:       now,
	}

	accounts := make([]brokerage.Account, 0, len(snaps))
	holdings := make([]brokerage.Holdings, 0, len(snaps))
	activities := map[string][]brokerage.Activity{}

	for _, s := range snaps {
		accID := fmt.Sprintf("futu-%d-%s", s.Account.AccID, marketSlug(s.Account.TrdMarket))
		mainCur := primaryCurrency(s.Account.TrdMarket)
		acc := brokerage.Account{
			ID:                     accID,
			Name:                   accountDisplay(s.Account),
			AccountNumber:          s.Account.CardNum,
			Type:                   futuAccType(s.Account.AccType),
			RawType:                strings.TrimPrefix(s.Account.AccType.String(), "TrdAccType_"),
			Currency:               mainCur,
			BalanceCurrency:        mainCur,
			BrokerageAuthorization: "futu-auth",
			InstitutionName:        "Futu Securities",
			SyncEnabled:            true,
			IsPaper:                s.Account.TrdEnv == pb.TrdEnv_TrdEnv_Simulate,
			Status:                 "open",
			CreatedDate:            now,
			LastHoldingsSync:       &now,
			InitialHoldingsDone:    true,
		}
		if s.Funds != nil {
			acc.BalanceTotal = s.Funds.GetTotalAssets()
			if cur := futuCurrency(s.Funds.GetCurrency()); cur != "" {
				acc.BalanceCurrency = cur
			}
		}
		if acts := buildActivities(s.Deals, accID, s.Account.TrdMarket); len(acts) > 0 {
			activities[accID] = acts
			acc.LastTxSync = &now
			acc.InitialTxSyncDone = true
		}
		accounts = append(accounts, acc)
		holdings = append(
			holdings, brokerage.Holdings{
				AccountID:  accID,
				Balances:   buildBalances(s.Funds),
				Positions:  buildPositions(s.Positions, s.Account.TrdMarket),
				CapturedAt: now,
			},
		)
	}

	return domainsync.BrokerSnapshot{
		Connection: connection,
		Accounts:   accounts,
		Holdings:   holdings,
		Activities: activities,
	}
}

func buildBalances(f *pb.Funds) []brokerage.Balance {
	if f == nil {
		return nil
	}
	cur := futuCurrency(f.GetCurrency())
	if cur == "" {
		cur = currencyHKD
	}
	return []brokerage.Balance{
		{
			Currency:    brokerage.Currency{Code: cur},
			Cash:        f.GetCash(),
			BuyingPower: f.GetPower(),
		},
	}
}

func buildPositions(in []*pb.Position, mkt pb.TrdMarket) []brokerage.Position {
	out := make([]brokerage.Position, 0, len(in))
	for _, p := range in {
		if p == nil || p.GetQty() == 0 {
			continue
		}
		cur := futuCurrency(p.GetCurrency())
		if cur == "" {
			cur = primaryCurrency(mkt)
		}
		sym := formatFutuSymbol(p.GetCode(), mkt)
		out = append(
			out, brokerage.Position{
				Symbol: brokerage.Symbol{
					Symbol:      sym,
					RawSymbol:   p.GetCode(),
					Description: p.GetName(),
					Name:        p.GetName(),
					Type:        brokerage.SymbolType{Code: "EQUITY", IsSupported: true, Description: "Equity"},
					Exchange:    brokerage.Exchange{Code: marketCode(mkt), Name: marketName(mkt)},
					Currency:    brokerage.Currency{Code: cur},
				},
				Units:                p.GetQty(),
				Price:                p.GetPrice(),
				OpenPnL:              p.GetPlVal(),
				AveragePurchasePrice: p.GetCostPrice(),
				Currency:             brokerage.Currency{Code: cur},
			},
		)
	}
	return out
}

// buildActivities converts a slice of OpenD OrderFill records ("成交") into
// canonical brokerage.Activity rows. SourceRecordID is keyed off the OpenD
// fill identifier ("FillIDEx" preferred, FillID fallback) so re-imports are
// idempotent via ActivityRepository.UpsertBatch.
func buildActivities(in []*pb.OrderFill, accountID string, mkt pb.TrdMarket) []brokerage.Activity {
	if len(in) == 0 {
		return nil
	}
	out := make([]brokerage.Activity, 0, len(in))
	for _, f := range in {
		act, ok := dealToActivity(f, accountID, mkt)
		if !ok {
			continue
		}
		out = append(out, act)
	}
	return out
}

// dealToActivity maps one OpenD fill into a domain Activity. Returns
// (_, false) when the fill is unusable (zero qty, missing id, unknown side).
func dealToActivity(f *pb.OrderFill, accountID string, mkt pb.TrdMarket) (brokerage.Activity, bool) {
	if f == nil || f.GetQty() == 0 {
		return brokerage.Activity{}, false
	}
	actType, rawSide, ok := futuTrdSide(f.GetTrdSide())
	if !ok {
		return brokerage.Activity{}, false
	}
	sourceID := f.GetFillIDEx()
	if sourceID == "" {
		if f.GetFillID() == 0 {
			return brokerage.Activity{}, false
		}
		sourceID = fmt.Sprintf("%d", f.GetFillID())
	}
	cur := primaryCurrency(mkt)
	ts := dealTimestamp(f)
	sym := formatFutuSymbol(f.GetCode(), mkt)
	return brokerage.Activity{
		ID:        sourceID,
		AccountID: accountID,
		Type:      actType,
		RawType:   rawSide,
		TradeDate: ts,
		Price:     f.GetPrice(),
		Units:     f.GetQty(),
		Amount:    f.GetPrice() * f.GetQty(),
		Currency:  brokerage.Currency{Code: cur},
		Symbol: &brokerage.Symbol{
			Symbol:      sym,
			RawSymbol:   f.GetCode(),
			Description: f.GetName(),
			Name:        f.GetName(),
			Type:        brokerage.SymbolType{Code: "EQUITY", IsSupported: true, Description: "Equity"},
			Exchange:    brokerage.Exchange{Code: marketCode(mkt), Name: marketName(mkt)},
			Currency:    brokerage.Currency{Code: cur},
		},
		ProviderType:   "futu",
		SourceSystem:   "futu",
		SourceRecordID: sourceID,
	}, true
}

// futuTrdSide maps OpenD's TrdSide enum into a canonical ActivityType plus
// the original raw label. SellShort/BuyBack collapse to SELL/BUY because the
// domain has no dedicated short-side concepts yet.
func futuTrdSide(side pb.TrdSide) (brokerage.ActivityType, string, bool) {
	switch side {
	case pb.TrdSide_TrdSide_Buy:
		return brokerage.ActivityBuy, "BUY", true
	case pb.TrdSide_TrdSide_Sell:
		return brokerage.ActivitySell, "SELL", true
	case pb.TrdSide_TrdSide_SellShort:
		return brokerage.ActivitySell, "SELL_SHORT", true
	case pb.TrdSide_TrdSide_BuyBack:
		return brokerage.ActivityBuy, "BUY_BACK", true
	default:
		return "", "", false
	}
}

// dealTimestamp prefers the high-precision Unix timestamp (CreateTimestamp)
// because OpenD historical fills sometimes drop the formatted CreateTime.
func dealTimestamp(f *pb.OrderFill) time.Time {
	if ts := f.GetCreateTimestamp(); ts > 0 {
		sec := int64(ts)
		nsec := int64((ts - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC()
	}
	if s := f.GetCreateTime(); s != "" {
		// Futu format: "YYYY-MM-DD HH:MM:SS" or "...HH:MM:SS.MS".
		layouts := []string{"2006-01-02 15:04:05", "2006-01-02 15:04:05.000"}
		for _, l := range layouts {
			if t, err := time.Parse(l, s); err == nil {
				return t.UTC()
			}
		}
	}
	return time.Now().UTC()
}

func accountDisplay(a Account) string {
	parts := []string{"Futu", marketName(a.TrdMarket)}
	if a.TrdEnv == pb.TrdEnv_TrdEnv_Simulate {
		parts = append(parts, "(Paper)")
	}
	return strings.Join(parts, " ")
}

func futuAccType(t pb.TrdAccType) brokerage.AccountType {
	switch t {
	case pb.TrdAccType_TrdAccType_Cash:
		return brokerage.AccountTypeCash
	case pb.TrdAccType_TrdAccType_Margin:
		return brokerage.AccountTypeMargin
	default:
		return brokerage.AccountTypeSecurities
	}
}

func futuCurrency(c pb.Currency) string {
	switch c {
	case pb.Currency_Currency_HKD:
		return currencyHKD
	case pb.Currency_Currency_USD:
		return "USD"
	case pb.Currency_Currency_CNH:
		return "CNH"
	case pb.Currency_Currency_JPY:
		return "JPY"
	case pb.Currency_Currency_SGD:
		return "SGD"
	case pb.Currency_Currency_AUD:
		return "AUD"
	default:
		return ""
	}
}

func primaryCurrency(m pb.TrdMarket) string {
	switch m {
	case pb.TrdMarket_TrdMarket_HK, pb.TrdMarket_TrdMarket_HK_Fund:
		return currencyHKD
	case pb.TrdMarket_TrdMarket_US, pb.TrdMarket_TrdMarket_US_Fund:
		return "USD"
	case pb.TrdMarket_TrdMarket_CN, pb.TrdMarket_TrdMarket_HKCC:
		return "CNH"
	case pb.TrdMarket_TrdMarket_SG:
		return "SGD"
	case pb.TrdMarket_TrdMarket_JP:
		return "JPY"
	default:
		return currencyHKD
	}
}

// marketCurrency maps a TrdMarket to the corresponding pb.Currency enum.
// This is required for TrdGetFunds on consolidated (综合) accounts.
func marketCurrency(m pb.TrdMarket) pb.Currency {
	switch m {
	case pb.TrdMarket_TrdMarket_HK, pb.TrdMarket_TrdMarket_HK_Fund:
		return pb.Currency_Currency_HKD
	case pb.TrdMarket_TrdMarket_US, pb.TrdMarket_TrdMarket_US_Fund:
		return pb.Currency_Currency_USD
	case pb.TrdMarket_TrdMarket_CN, pb.TrdMarket_TrdMarket_HKCC:
		return pb.Currency_Currency_CNH
	case pb.TrdMarket_TrdMarket_SG:
		return pb.Currency_Currency_SGD
	case pb.TrdMarket_TrdMarket_JP:
		return pb.Currency_Currency_JPY
	default:
		return pb.Currency_Currency_HKD
	}
}

// formatFutuSymbol appends a market suffix to the raw Futu symbol when needed.
// Hong Kong stocks get ".HK" appended (e.g. "00700" → "00700.HK").
func formatFutuSymbol(code string, mkt pb.TrdMarket) string {
	switch mkt {
	case pb.TrdMarket_TrdMarket_HK, pb.TrdMarket_TrdMarket_HK_Fund:
		return code + ".HK"
	default:
		return code
	}
}

func marketCode(m pb.TrdMarket) string {
	switch m {
	case pb.TrdMarket_TrdMarket_HK, pb.TrdMarket_TrdMarket_HK_Fund:
		return "HKEX"
	case pb.TrdMarket_TrdMarket_US, pb.TrdMarket_TrdMarket_US_Fund:
		return "NASDAQ"
	case pb.TrdMarket_TrdMarket_CN:
		return "SSE"
	case pb.TrdMarket_TrdMarket_HKCC:
		return "HKCC"
	case pb.TrdMarket_TrdMarket_SG:
		return "SGX"
	case pb.TrdMarket_TrdMarket_JP:
		return "TSE"
	default:
		return "FUTU"
	}
}

func marketName(m pb.TrdMarket) string {
	switch m {
	case pb.TrdMarket_TrdMarket_HK:
		return "Hong Kong"
	case pb.TrdMarket_TrdMarket_HK_Fund:
		return "Hong Kong Fund"
	case pb.TrdMarket_TrdMarket_US:
		return "US"
	case pb.TrdMarket_TrdMarket_US_Fund:
		return "US Fund"
	case pb.TrdMarket_TrdMarket_CN:
		return "China A"
	case pb.TrdMarket_TrdMarket_HKCC:
		return "Stock Connect"
	case pb.TrdMarket_TrdMarket_SG:
		return "Singapore"
	case pb.TrdMarket_TrdMarket_JP:
		return "Japan"
	default:
		return "Futu"
	}
}

// realDialer wires Session through the real santsai/futu-go implementation.
type realDialer struct {
	connectionID string
	rsaKey       []byte
}

// Dial opens a new OpenD session at host:port using the santsai/futu-go SDK.
// The SDK's NewClient handles connect + initConnect internally with a default
// 5s per-request timeout, and respects context cancellation.
func (d realDialer) Dial(ctx context.Context, host string, port int) (Session, error) {
	addr := fmt.Sprintf("%s:%d", host, port)

	type result struct {
		client *futugo.Client
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		opts := []futugo.ClientOption{
			futugo.WithOpenDAddr(addr),
			futugo.WithClientID(d.connectionID),
			futugo.WithTimeout(10 * time.Second),
		}
		if len(d.rsaKey) > 0 {
			opts = append(opts, futugo.WithPrivateKey(d.rsaKey))
		}
		c, err := futugo.NewClient(opts...)
		ch <- result{client: c, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("futu connect to %s: %w", addr, ctx.Err())
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("futu connect to %s: %w", addr, r.err)
		}
		return &realSession{client: r.client}, nil
	}
}

type realSession struct {
	client *futugo.Client
}

// Unlock authenticates trading on the OpenD session with the given password.
func (s *realSession) Unlock(ctx context.Context, password string) error {
	req := &pb.TrdUnlockTradeRequest{}
	req.WithUnlock(true).WithPwdMD5(password).WithSecurityFirm(pb.SecurityFirm_SecurityFirm_FutuSecurities)
	_, err := req.Dispatch(ctx, s.client)
	return err
}

// ListAccounts returns every brokerage account exposed by the OpenD session.
func (s *realSession) ListAccounts(ctx context.Context) ([]Account, error) {
	req := &pb.TrdGetAccListRequest{}
	req.WithCategory(pb.TrdCategory_TrdCategory_Security).WithNeedGeneralSecAccount(true)
	resp, err := req.Dispatch(ctx, s.client)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("ListAccounts: nil response from OpenD")
	}
	raws := resp.GetAccList()
	out := make([]Account, 0, len(raws)*2)
	seen := make(map[uint64]map[pb.TrdMarket]bool)
	for _, r := range raws {
		// Skip paper/simulated accounts.
		if r.GetTrdEnv() == pb.TrdEnv_TrdEnv_Simulate {
			continue
		}
		markets := r.GetTrdMarketAuthList()
		if len(markets) == 0 {
			markets = []pb.TrdMarket{pb.TrdMarket_TrdMarket_HK}
		}
		for _, mkt := range markets {
			if seen[r.GetAccID()] == nil {
				seen[r.GetAccID()] = make(map[pb.TrdMarket]bool)
			}
			if seen[r.GetAccID()][mkt] {
				continue
			}
			seen[r.GetAccID()][mkt] = true
			out = append(
				out, Account{
					AccID:     r.GetAccID(),
					TrdEnv:    r.GetTrdEnv(),
					TrdMarket: mkt,
					AccType:   r.GetAccType(),
					CardNum:   r.GetCardNum(),
				},
			)
		}
	}
	return out, nil
}

// Funds returns the cash balance and buying power for the given account.
func (s *realSession) Funds(ctx context.Context, acc Account) (*pb.Funds, error) {
	req := &pb.TrdGetFundsRequest{}
	req.WithHeader(headerFor(acc)).WithRefreshCache(true).WithCurrency(marketCurrency(acc.TrdMarket))
	resp, err := req.Dispatch(ctx, s.client)
	if err != nil {
		return nil, fmt.Errorf("Funds(accID=%d, env=%s, market=%s): %w", acc.AccID, acc.TrdEnv, acc.TrdMarket, err)
	}
	if resp == nil {
		return nil, fmt.Errorf("Funds(accID=%d): nil response from OpenD", acc.AccID)
	}
	f := resp.GetFunds()
	if f == nil {
		return nil, fmt.Errorf("Funds(accID=%d): response has nil Funds payload", acc.AccID)
	}
	return f, nil
}

// Positions returns the open positions for the given account.
func (s *realSession) Positions(ctx context.Context, acc Account) ([]*pb.Position, error) {
	req := &pb.TrdGetPositionListRequest{}
	req.WithHeader(headerFor(acc)).WithRefreshCache(true)
	resp, err := req.Dispatch(ctx, s.client)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("Positions: nil response from OpenD")
	}
	return resp.GetPositionList(), nil
}

// HistoryDeals fetches filled orders for the given account in the [begin, end] window.
func (s *realSession) HistoryDeals(ctx context.Context, acc Account, begin, end time.Time) ([]*pb.OrderFill, error) {
	filter := &pb.TrdFilterConditions{}
	filter.WithBeginTime(begin.Format("2006-01-02 15:04:05"))
	filter.WithEndTime(end.Format("2006-01-02 15:04:05"))
	req := &pb.TrdGetHistoryOrderFillListRequest{}
	req.WithHeader(headerFor(acc)).WithFilterConditions(filter)
	resp, err := req.Dispatch(ctx, s.client)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("HistoryDeals: nil response from OpenD")
	}
	return resp.GetOrderFillList(), nil
}

// Close terminates the OpenD session.
func (s *realSession) Close(_ context.Context) error {
	return s.client.Close()
}

func headerFor(a Account) *pb.TrdHeader {
	h := &pb.TrdHeader{}
	h.WithAccID(a.AccID).WithEnv(a.TrdEnv).WithMarket(a.TrdMarket)
	return h
}

// pickMarket picks the most relevant market from the auth list. Futu accounts
// usually have multiple market authorisations (HK + US); we prefer HK because
// that is where most retail Futu users hold their primary cash position.
func pickMarket(auths []pb.TrdMarket) pb.TrdMarket {
	if len(auths) == 0 {
		return pb.TrdMarket_TrdMarket_HK
	}
	priority := []pb.TrdMarket{
		pb.TrdMarket_TrdMarket_HK,
		pb.TrdMarket_TrdMarket_US,
		pb.TrdMarket_TrdMarket_CN,
		pb.TrdMarket_TrdMarket_SG,
	}
	have := make(map[pb.TrdMarket]bool, len(auths))
	for _, a := range auths {
		have[a] = true
	}
	for _, p := range priority {
		if have[p] {
			return p
		}
	}
	return auths[0]
}

// marketSlug returns a short lowercase identifier for a TrdMarket enum value,
// suitable for inclusion in composite IDs (e.g. "futu-12345-hk").
func marketSlug(m pb.TrdMarket) string {
	switch m {
	case pb.TrdMarket_TrdMarket_HK:
		return "hk"
	case pb.TrdMarket_TrdMarket_US:
		return "us"
	case pb.TrdMarket_TrdMarket_CN:
		return "cn"
	case pb.TrdMarket_TrdMarket_SG:
		return "sg"
	case pb.TrdMarket_TrdMarket_JP:
		return "jp"
	case pb.TrdMarket_TrdMarket_HK_Fund:
		return "hk_fund"
	case pb.TrdMarket_TrdMarket_US_Fund:
		return "us_fund"
	default:
		return strings.ToLower(strings.TrimPrefix(m.String(), "TrdMarket_TrdMarket_"))
	}
}
