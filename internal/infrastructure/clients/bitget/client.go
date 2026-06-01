// Package bitget implements a BrokerClient that reads spot balances from
// Bitget's v2 REST API using the official HMAC-SHA256 signing scheme:
//
//	ACCESS-SIGN = base64(HMAC_SHA256(timestamp + method + path + body, secret))
//
// where timestamp is a millisecond Unix epoch as a decimal string.
//
// Endpoint reference:
//
//	GET /api/v2/spot/account/assets — every non-zero spot balance
//	GET /api/v2/spot/market/tickers — last price for symbols ending in USDT
package bitget

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

	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/cexcommon"
)

// HTTPDoer mirrors http.Client.Do for testability.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

const defaultBaseURL = "https://api.bitget.com"

// Client is the Bitget BrokerClient.
type Client struct {
	apiKey, secret, passphrase string
	baseURL                    string
	http                       HTTPDoer
}

// New builds a client.
func New(apiKey, secret, passphrase, baseURL string, h HTTPDoer) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if h == nil {
		h = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		apiKey: apiKey, secret: secret, passphrase: passphrase,
		baseURL: baseURL, http: h,
	}
}

// ID returns the slug.
func (c *Client) ID() string { return "bitget" }

// Fetch retrieves balances + USDT ticker prices and translates them.
func (c *Client) Fetch(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	if c.apiKey == "" || c.secret == "" || c.passphrase == "" {
		return domainsync.BrokerSnapshot{}, errors.New("bitget: API credentials not fully configured")
	}
	balances, err := c.getAssets(ctx)
	if err != nil {
		return domainsync.BrokerSnapshot{}, fmt.Errorf("bitget: assets: %w", err)
	}
	prices, err := c.getTickers(ctx)
	if err != nil {
		prices = map[string]float64{} // best-effort
	}
	return cexcommon.Translate("bitget", "Bitget", buildSnapshot(balances, prices)), nil
}

type rawAsset struct {
	Coin   string
	Avail  float64
	Frozen float64
	Locked float64
}

func (c *Client) getAssets(ctx context.Context) ([]rawAsset, error) {
	type asset struct {
		Coin   string `json:"coin"`
		Avail  string `json:"available"`
		Frozen string `json:"frozen"`
		Locked string `json:"locked"`
	}
	type envelope struct {
		Code string  `json:"code"`
		Msg  string  `json:"msg"`
		Data []asset `json:"data"`
	}
	var env envelope
	if err := c.signedGet(ctx, "/api/v2/spot/account/assets", nil, &env); err != nil {
		return nil, err
	}
	if env.Code != "00000" && env.Code != "" {
		return nil, fmt.Errorf("api error %s: %s", env.Code, env.Msg)
	}
	out := make([]rawAsset, 0, len(env.Data))
	for _, a := range env.Data {
		out = append(
			out, rawAsset{
				Coin:   strings.ToUpper(a.Coin),
				Avail:  atof(a.Avail),
				Frozen: atof(a.Frozen),
				Locked: atof(a.Locked),
			},
		)
	}
	return out, nil
}

func (c *Client) getTickers(ctx context.Context) (map[string]float64, error) {
	type ticker struct {
		Symbol string `json:"symbol"`
		LastPr string `json:"lastPr"`
	}
	type envelope struct {
		Code string   `json:"code"`
		Msg  string   `json:"msg"`
		Data []ticker `json:"data"`
	}
	var env envelope
	// Tickers is a public endpoint, no signing needed; reuse the same path
	// but with creds-less call.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v2/spot/market/tickers", http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	// Bitget surfaces upstream issues via a non-zero `code` even on a 200
	// response. Without this guard a partial outage silently turns every
	// holding into a $0 USD value.
	if env.Code != "00000" && env.Code != "" {
		return nil, fmt.Errorf("api error %s: %s", env.Code, env.Msg)
	}
	out := make(map[string]float64, len(env.Data))
	for _, t := range env.Data {
		out[t.Symbol] = atof(t.LastPr)
	}
	return out, nil
}

func buildSnapshot(balances []rawAsset, prices map[string]float64) cexcommon.Snapshot {
	out := cexcommon.Snapshot{}
	for _, a := range balances {
		qty := a.Avail + a.Frozen + a.Locked
		if qty == 0 {
			continue
		}
		var price, usd float64
		if cexcommon.IsStablecoin(a.Coin) {
			price = 1
			usd = qty
		} else {
			price = prices[a.Coin+"USDT"]
			usd = price * qty
		}
		out.Balances = append(
			out.Balances, cexcommon.Balance{
				Asset:    a.Coin,
				Quantity: qty,
				PriceUSD: price,
				USDValue: usd,
			},
		)
	}
	return out
}

// ===================== shared signing =====================

func (c *Client) signedGet(ctx context.Context, path string, query url.Values, into any) error {
	requestPath := path
	if len(query) > 0 {
		requestPath = path + "?" + query.Encode()
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	prehash := timestamp + http.MethodGet + requestPath
	mac := hmac.New(sha256.New, []byte(c.secret))
	_, _ = mac.Write([]byte(prehash))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+requestPath, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("ACCESS-KEY", c.apiKey)
	req.Header.Set("ACCESS-SIGN", sign)
	req.Header.Set("ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("ACCESS-PASSPHRASE", c.passphrase)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("locale", "en-US")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, raw)
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func atof(s string) float64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64) //nolint:errcheck // exchange returns numeric strings; treat unparsable as zero
	return v
}
