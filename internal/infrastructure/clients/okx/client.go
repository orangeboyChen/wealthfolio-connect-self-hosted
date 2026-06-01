// Package okx implements two BrokerClients backed by the OKX REST API:
//
//   - CEX  : /api/v5/account/balance — the regular spot/funding account
//   - Web3 : /api/v5/dex/balance/all-token-balances-by-address — wallet
//     balances on every supported EVM chain
//
// Both clients share the same v5 HMAC-SHA256 signing scheme (the Web3
// product issues its own API key but signing is identical):
//
//	OK-ACCESS-SIGN = base64(HMAC_SHA256(timestamp + method + path + body, secret))
//
// where timestamp is an ISO 8601 string with milliseconds.
package okx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/cexcommon"
)

// HTTPDoer mirrors http.Client.Do so tests can stub it.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Credentials are the per-request fields used to sign every authenticated
// call. Both the CEX and Web3 products use the same shape.
type Credentials struct {
	APIKey     string
	Secret     string
	Passphrase string
}

const (
	defaultBaseURL = "https://www.okx.com"
)

// ============================== CEX client ==============================

// CEXClient reads spot + funding balances from /api/v5/account/balance.
type CEXClient struct {
	baseURL string
	creds   Credentials
	http    HTTPDoer
}

// NewCEX builds a CEX client. Pass nil http to use a default 15s client.
func NewCEX(creds Credentials, baseURL string, h HTTPDoer) *CEXClient {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	// Trim trailing slashes so callers can configure either form without
	// producing double-slash URLs (e.g. https://www.okx.com//api/v5/...).
	baseURL = strings.TrimRight(baseURL, "/")
	if h == nil {
		h = &http.Client{Timeout: 15 * time.Second}
	}
	return &CEXClient{baseURL: baseURL, creds: creds, http: h}
}

// ID returns the slug used by sync orchestration.
func (c *CEXClient) ID() string { return "okx" }

// Fetch retrieves balances and trade history, then translates them into a BrokerSnapshot.
func (c *CEXClient) Fetch(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	if c.creds.APIKey == "" || c.creds.Secret == "" || c.creds.Passphrase == "" {
		return domainsync.BrokerSnapshot{}, errors.New("okx: API credentials not fully configured")
	}
	type asset struct {
		Ccy     string `json:"ccy"`
		EqUSD   string `json:"eqUsd"`
		CashBal string `json:"cashBal"`
	}
	type detail struct {
		Details []asset `json:"details"`
	}
	type envelope struct {
		Code string   `json:"code"`
		Msg  string   `json:"msg"`
		Data []detail `json:"data"`
	}
	var env envelope
	if err := c.signedGet(ctx, "/api/v5/account/balance", nil, &env); err != nil {
		return domainsync.BrokerSnapshot{}, fmt.Errorf("okx: balance: %w", err)
	}
	if env.Code != "0" && env.Code != "" {
		return domainsync.BrokerSnapshot{}, fmt.Errorf("okx: api error %s: %s", env.Code, env.Msg)
	}
	snap := cexcommon.Snapshot{}
	for _, d := range env.Data {
		for _, a := range d.Details {
			qty := atof(a.CashBal)
			usd := atof(a.EqUSD)
			if qty == 0 {
				continue
			}
			price := 0.0
			if qty > 0 {
				price = usd / qty
			}
			snap.Balances = append(
				snap.Balances, cexcommon.Balance{
					Asset:    strings.ToUpper(a.Ccy),
					Quantity: qty,
					PriceUSD: price,
					USDValue: usd,
				},
			)
		}
	}

	// Fetch recent trade fills (best-effort; failures don't block the snapshot).
	trades, err := c.getFillsHistory(ctx)
	if err == nil {
		snap.Trades = trades
	}

	return cexcommon.Translate("okx", "OKX", snap), nil
}

// getFillsHistory retrieves the recent 90-day trade fills from OKX.
// OKX /api/v5/trade/fills-history returns up to 100 fills per page; we
// paginate up to 3 pages (300 fills) to capture recent history.
func (c *CEXClient) getFillsHistory(ctx context.Context) ([]cexcommon.Trade, error) {
	type fill struct {
		BillID string `json:"billId"`
		InstID string `json:"instId"`
		Side   string `json:"side"`
		FillPx string `json:"fillPx"`
		FillSz string `json:"fillSz"`
		Fee    string `json:"fee"` // negative means fee charged
		FeeCcy string `json:"feeCcy"`
		TS     string `json:"ts"`
	}
	type envelope struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []fill `json:"data"`
	}

	var all []cexcommon.Trade
	var after string
	for page := 0; page < 3; page++ {
		q := url.Values{}
		q.Set("instType", "SPOT")
		if after != "" {
			q.Set("after", after)
		}
		var env envelope
		if err := c.signedGet(ctx, "/api/v5/trade/fills-history", q, &env); err != nil {
			return all, err
		}
		if env.Code != "0" && env.Code != "" {
			return all, fmt.Errorf("okx fills api error %s: %s", env.Code, env.Msg)
		}
		if len(env.Data) == 0 {
			break
		}
		for _, f := range env.Data {
			ts := time.UnixMilli(int64(atof(f.TS))).UTC()
			fee := atof(f.Fee)
			if fee < 0 {
				fee = -fee
			}
			all = append(all, cexcommon.Trade{
				ID:        f.BillID,
				Symbol:    strings.ReplaceAll(f.InstID, "-", ""),
				Side:      f.Side,
				Price:     atof(f.FillPx),
				Quantity:  atof(f.FillSz),
				Fee:       fee,
				FeeAsset:  f.FeeCcy,
				Timestamp: ts,
			})
		}
		after = env.Data[len(env.Data)-1].BillID
	}
	return all, nil
}

func (c *CEXClient) signedGet(ctx context.Context, path string, query url.Values, into any) error {
	return signedRequest(ctx, c.http, c.baseURL, http.MethodGet, path, query, nil, c.creds, into)
}

// ============================== Web3 client ==============================

// Web3Client reads on-chain balances per (address, chain) tuple via OKX's
// DEX product. It is wired separately from the CEX client because:
//
//   - It uses a *different* set of API credentials issued under OKX Web3.
//   - The endpoint expects an `address` + `chains` query string instead of
//     pulling balances from the user's centralized account.
//
// We aggregate every configured wallet into a single connection ("okx-web3")
// with one brokerage Account per (chain, wallet).
type Web3Client struct {
	baseURL string
	creds   Credentials
	wallets []Wallet
	http    HTTPDoer
	log     zerolog.Logger
}

// Wallet is one configured user wallet driven through OKX Web3.
type Wallet struct {
	Address string   // EVM hex address, lower or mixed case
	Chains  []string // OKX chainIndex strings: "1" eth, "56" bsc, "137" matic, "42161" arb, ...
	Label   string   // optional human label
}

// NewWeb3 builds a Web3 client.
func NewWeb3(creds Credentials, wallets []Wallet, baseURL string, h HTTPDoer) *Web3Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second}
	}
	return &Web3Client{baseURL: baseURL, creds: creds, wallets: wallets, http: h, log: zerolog.Nop()}
}

// SetLogger attaches a structured logger so transient per-wallet failures
// in Fetch can be surfaced to operators instead of being silently dropped.
// Safe to call before/after construction; nil resets to a no-op logger.
func (c *Web3Client) SetLogger(log zerolog.Logger) {
	c.log = log
}

// ID returns the slug "okx_web3".
func (c *Web3Client) ID() string { return "okx_web3" }

// Web3Token is one balance row returned by the OKX DEX API.
type Web3Token struct {
	Symbol     string // e.g. "ETH"
	TokenAddr  string // contract address (empty for native)
	ChainIndex string // OKX chainIndex
	Quantity   float64
	PriceUSD   float64 // per-token USD price
}

// Fetch retrieves balances for every configured wallet and translates them
// into a BrokerSnapshot with one account per wallet. Wallets configured
// but unreachable are skipped on a best-effort basis (the rest of the
// snapshot still goes through), and each failure is logged so operators
// can investigate before the data quietly goes stale.
func (c *Web3Client) Fetch(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	if c.creds.APIKey == "" || c.creds.Secret == "" || c.creds.Passphrase == "" {
		return domainsync.BrokerSnapshot{}, errors.New("okx_web3: API credentials not fully configured")
	}
	if len(c.wallets) == 0 {
		// No wallets configured → do not surface an empty "OKX Web3"
		// connection in the desktop UI.
		return domainsync.BrokerSnapshot{Activities: map[string][]brokerage.Activity{}}, nil
	}
	out := make([]Web3WalletData, 0, len(c.wallets))
	for _, w := range c.wallets {
		tokens, err := c.fetchWallet(ctx, w)
		if err != nil {
			c.log.Warn().
				Err(err).
				Str("address", w.Address).
				Str("label", w.Label).
				Msg("okx_web3: skipping wallet after fetch failure")
			continue
		}
		out = append(out, Web3WalletData{Wallet: w, Tokens: tokens})
	}
	return TranslateWeb3(out), nil
}

// Web3WalletData bundles one wallet's payload for the translator.
type Web3WalletData struct {
	Wallet Wallet
	Tokens []Web3Token
}

func (c *Web3Client) fetchWallet(ctx context.Context, w Wallet) ([]Web3Token, error) {
	type tokenRow struct {
		Symbol       string `json:"symbol"`
		TokenAddress string `json:"tokenAddress"`
		ChainIndex   string `json:"chainIndex"`
		Balance      string `json:"balance"`
		TokenPrice   string `json:"tokenPrice"`
		IsRiskToken  bool   `json:"isRiskToken"`
	}
	type assetGroup struct {
		TokenAssets []tokenRow `json:"tokenAssets"`
	}
	type envelope struct {
		Code string       `json:"code"`
		Msg  string       `json:"msg"`
		Data []assetGroup `json:"data"`
	}
	q := url.Values{}
	q.Set("address", w.Address)
	if len(w.Chains) > 0 {
		q.Set("chains", strings.Join(w.Chains, ","))
	}
	q.Set("filter", "0") // 0 = include everything (1 hides spam tokens)
	var env envelope
	if err := signedRequest(
		ctx, c.http, c.baseURL,
		http.MethodGet, "/api/v5/dex/balance/all-token-balances-by-address",
		q, nil, c.creds, &env,
	); err != nil {
		return nil, err
	}
	if env.Code != "0" && env.Code != "" {
		return nil, fmt.Errorf("okx_web3 api error %s: %s", env.Code, env.Msg)
	}
	out := make([]Web3Token, 0)
	for _, g := range env.Data {
		for _, t := range g.TokenAssets {
			if t.IsRiskToken {
				continue
			}
			qty := atof(t.Balance)
			price := atof(t.TokenPrice)
			if qty == 0 {
				continue
			}
			out = append(
				out, Web3Token{
					Symbol:     strings.ToUpper(t.Symbol),
					TokenAddr:  t.TokenAddress,
					ChainIndex: t.ChainIndex,
					Quantity:   qty,
					PriceUSD:   price,
				},
			)
		}
	}
	return out, nil
}

// TranslateWeb3 folds every wallet's tokens into a single connection with
// one account per wallet. Public for testability.
func TranslateWeb3(wallets []Web3WalletData) domainsync.BrokerSnapshot {
	now := time.Now().UTC()
	connection := brokerage.Connection{
		ID:              "okx-web3-conn",
		AuthorizationID: "okx-web3-auth",
		BrokerageName:   "OKX Web3",
		BrokerageSlug:   "okx_web3",
		DisplayName:     "OKX Web3 (DeFi)",
		Name:            "OKX Web3",
		Status:          brokerage.ConnectionActive,
		UpdatedAt:       now,
	}

	accounts := make([]brokerage.Account, 0, len(wallets))
	holdings := make([]brokerage.Holdings, 0, len(wallets))
	for _, w := range wallets {
		accountID := "okxweb3-" + strings.ToLower(w.Wallet.Address)
		var totalUSD, stableCash float64
		var positions []brokerage.Position
		for _, t := range w.Tokens {
			usd := t.Quantity * t.PriceUSD
			// Stablecoins must be swept into the cash balance *before* the
			// dust filter runs, otherwise tiny stable balances (e.g. 0.5
			// USDC) get dropped instead of contributing to cash.
			if cexcommon.IsStablecoin(t.Symbol) {
				stableCash += usd
				totalUSD += usd
				continue
			}
			if usd < 1 {
				continue
			}
			totalUSD += usd
			positions = append(
				positions, brokerage.Position{
					Symbol: brokerage.Symbol{
						Symbol:      t.Symbol,
						RawSymbol:   t.Symbol,
						Name:        t.Symbol,
						Description: t.Symbol,
						Type:        brokerage.SymbolType{Code: "CRYPTO", IsSupported: true},
						Exchange:    brokerage.Exchange{Code: chainName(t.ChainIndex), Name: chainName(t.ChainIndex)},
						Currency:    brokerage.Currency{Code: "USD"},
					},
					Units:                t.Quantity,
					Price:                t.PriceUSD,
					AveragePurchasePrice: t.PriceUSD,
					Currency:             brokerage.Currency{Code: "USD"},
				},
			)
		}
		label := w.Wallet.Label
		if label == "" {
			label = "Wallet " + shortAddr(w.Wallet.Address)
		}
		acc := brokerage.Account{
			ID:                     accountID,
			Name:                   label,
			AccountNumber:          shortAddr(w.Wallet.Address),
			Type:                   brokerage.AccountTypeCryptocurrency,
			RawType:                "CRYPTO_WALLET",
			Currency:               "USD",
			BalanceTotal:           totalUSD,
			BalanceCurrency:        "USD",
			BrokerageAuthorization: "okx-web3-auth",
			InstitutionName:        "OKX Web3",
			SyncEnabled:            true,
			Status:                 "open",
			CreatedDate:            now,
			LastHoldingsSync:       &now,
			InitialHoldingsDone:    true,
		}
		accounts = append(accounts, acc)
		holdings = append(
			holdings, brokerage.Holdings{
				AccountID: accountID,
				Balances: []brokerage.Balance{
					{Currency: brokerage.Currency{Code: "USD"}, Cash: stableCash},
				},
				Positions:  positions,
				CapturedAt: now,
			},
		)
	}
	return domainsync.BrokerSnapshot{
		Connection: connection,
		Accounts:   accounts,
		Holdings:   holdings,
		Activities: map[string][]brokerage.Activity{},
	}
}

// chainName maps OKX chainIndex → human-friendly exchange code.
// Reference: https://www.okx.com/web3/build/docs/waas/dex-supported-chain
func chainName(idx string) string {
	switch idx {
	case "1":
		return "ETHEREUM"
	case "56":
		return "BSC"
	case "137":
		return "POLYGON"
	case "42161":
		return "ARBITRUM"
	case "10":
		return "OPTIMISM"
	case "8453":
		return "BASE"
	case "43114":
		return "AVALANCHE"
	case "324":
		return "ZKSYNC"
	case "59144":
		return "LINEA"
	case "5000":
		return "MANTLE"
	default:
		return "CHAIN-" + idx
	}
}

func shortAddr(a string) string {
	if len(a) < 10 {
		return a
	}
	return a[:6] + "..." + a[len(a)-4:]
}

// ============================== shared signing ==============================

// signedRequest signs an OKX v5 request and decodes its JSON body into into.
// Both CEX and Web3 endpoints accept the exact same signing scheme.
func signedRequest(
	ctx context.Context,
	doer HTTPDoer,
	baseURL, method, path string,
	query url.Values,
	body []byte,
	creds Credentials,
	into any,
) error {
	requestPath := path
	if len(query) > 0 {
		requestPath = path + "?" + query.Encode()
	}
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	prehash := timestamp + method + requestPath
	if len(body) > 0 {
		prehash += string(body)
	}
	mac := hmac.New(sha256.New, []byte(creds.Secret))
	_, _ = mac.Write([]byte(prehash))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, method, baseURL+requestPath, bodyReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("OK-ACCESS-KEY", creds.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", sign)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", creds.Passphrase)
	req.Header.Set("Content-Type", "application/json")

	resp, err := doer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(raw))
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func bodyReader(b []byte) io.Reader {
	if len(b) == 0 {
		return http.NoBody
	}
	return strings.NewReader(string(b))
}

func atof(s string) float64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64) //nolint:errcheck // exchange returns numeric strings; treat unparsable as zero
	return v
}
