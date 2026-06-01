// Package hyperliquid implements a BrokerClient that pulls a wallet's
// perpetuals + spot balances from Hyperliquid's public /info endpoint:
//
//	POST https://api.hyperliquid.xyz/info
//	{
//	  "type":  "spotClearinghouseState" | "clearinghouseState",
//	  "user":  "0x..."
//	}
//
// No API keys are required — the wallet address is the only credential.
package hyperliquid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/cexcommon"
)

// HTTPDoer mirrors http.Client.Do.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

const defaultBaseURL = "https://api.hyperliquid.xyz"

// Client reads balances for a single Hyperliquid wallet.
type Client struct {
	wallet  string
	baseURL string
	http    HTTPDoer
}

// New builds a client targeting the supplied wallet address.
func New(wallet, baseURL string, h HTTPDoer) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if h == nil {
		h = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{wallet: wallet, baseURL: baseURL, http: h}
}

// ID returns the slug.
func (c *Client) ID() string { return "hyperliquid" }

// Fetch retrieves spot + perp state and folds them into a snapshot.
func (c *Client) Fetch(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	if c.wallet == "" {
		return domainsync.BrokerSnapshot{}, errors.New("hyperliquid: wallet address not configured")
	}
	spot, spotErr := c.spot(ctx)
	perp, perpErr := c.perp(ctx)
	if spotErr != nil && perpErr != nil {
		return domainsync.BrokerSnapshot{}, fmt.Errorf("hyperliquid: %w", errors.Join(spotErr, perpErr))
	}
	snap := cexcommon.Snapshot{}
	snap.Balances = append(snap.Balances, spot...)
	// Perp account collateral is folded into a synthetic USDC cash balance.
	if perp > 0 {
		snap.Balances = append(
			snap.Balances, cexcommon.Balance{
				Asset:    "USDC",
				Quantity: perp,
				PriceUSD: 1,
				USDValue: perp,
			},
		)
	}
	return cexcommon.Translate("hyperliquid", "Hyperliquid", snap), nil
}

func (c *Client) spot(ctx context.Context) ([]cexcommon.Balance, error) {
	type assetRow struct {
		Coin     string `json:"coin"`
		Total    string `json:"total"`
		EntryNtl string `json:"entryNtl"`
	}
	type envelope struct {
		Balances []assetRow `json:"balances"`
	}
	var env envelope
	if err := c.postInfo(
		ctx, map[string]string{
			"type": "spotClearinghouseState",
			"user": c.wallet,
		}, &env,
	); err != nil {
		return nil, err
	}
	out := make([]cexcommon.Balance, 0, len(env.Balances))
	for _, r := range env.Balances {
		qty := atof(r.Total)
		if qty == 0 {
			continue
		}
		ntl := atof(r.EntryNtl)
		var price, usd float64
		if cexcommon.IsStablecoin(r.Coin) {
			price = 1
			usd = qty
		} else if qty > 0 && ntl > 0 {
			price = ntl / qty
			usd = ntl
		}
		out = append(
			out, cexcommon.Balance{
				Asset:    strings.ToUpper(r.Coin),
				Quantity: qty,
				PriceUSD: price,
				USDValue: usd,
			},
		)
	}
	return out, nil
}

func (c *Client) perp(ctx context.Context) (float64, error) {
	type marginSummary struct {
		AccountValue string `json:"accountValue"`
	}
	type envelope struct {
		MarginSummary marginSummary `json:"marginSummary"`
	}
	var env envelope
	if err := c.postInfo(
		ctx, map[string]string{
			"type": "clearinghouseState",
			"user": c.wallet,
		}, &env,
	); err != nil {
		return 0, err
	}
	return atof(env.MarginSummary.AccountValue), nil
}

func (c *Client) postInfo(ctx context.Context, payload any, into any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/info", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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
