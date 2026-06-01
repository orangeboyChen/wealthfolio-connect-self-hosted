// Package binance implements a BrokerClient that reads spot account
// balances from Binance via the official adshao/go-binance/v2 SDK.
//
// To compute USD valuation we hit the public /api/v3/ticker/price endpoint
// once per non-stable asset using its USDT pair (BTCUSDT, ETHUSDT, ...).
// Stablecoins skip the price lookup and are folded into the cash balance.
package binance

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	binsdk "github.com/adshao/go-binance/v2"

	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/cexcommon"
)

// Fetcher abstracts the Binance SDK so tests can plug in a fake.
type Fetcher interface {
	Account(ctx context.Context) ([]RawBalance, error)
	Prices(ctx context.Context) (map[string]float64, error) // map["BTCUSDT"] = price
}

// RawBalance is the per-asset payload a Fetcher returns. Public so tests
// can construct it.
type RawBalance struct {
	Asset  string
	Free   float64
	Locked float64
}

// Client is the Binance BrokerClient.
type Client struct {
	apiKey, secret string
	fetcher        Fetcher
}

// New builds a client. Pass nil fetcher to use the real SDK.
func New(apiKey, secret string, f Fetcher) *Client {
	if f == nil {
		f = &realFetcher{client: binsdk.NewClient(apiKey, secret)}
	}
	return &Client{apiKey: apiKey, secret: secret, fetcher: f}
}

// ID returns the slug used by sync orchestration.
func (c *Client) ID() string { return "binance" }

// Fetch pulls account balances + USDT-quoted prices and returns a snapshot.
func (c *Client) Fetch(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	if c.apiKey == "" || c.secret == "" {
		return domainsync.BrokerSnapshot{}, errors.New("binance: api key/secret not configured")
	}
	balances, err := c.fetcher.Account(ctx)
	if err != nil {
		return domainsync.BrokerSnapshot{}, fmt.Errorf("binance: account: %w", err)
	}
	prices, err := c.fetcher.Prices(ctx)
	if err != nil {
		// Prices are best-effort: without them positions just have zero
		// USD valuation and most will be filtered out by the dust filter,
		// but cash stablecoins still flow through.
		prices = map[string]float64{}
	}
	return cexcommon.Translate("binance", "Binance", buildSnapshot(balances, prices)), nil
}

// buildSnapshot is exposed via BuildSnapshotForTest so external tests can
// drive the mapping pipeline.
func buildSnapshot(balances []RawBalance, prices map[string]float64) cexcommon.Snapshot {
	out := cexcommon.Snapshot{}
	for _, b := range balances {
		qty := b.Free + b.Locked
		if qty == 0 {
			continue
		}
		asset := strings.ToUpper(b.Asset)
		var price, usd float64
		if cexcommon.IsStablecoin(asset) {
			price = 1
			usd = qty
		} else {
			price = prices[asset+"USDT"]
			usd = price * qty
		}
		out.Balances = append(out.Balances, cexcommon.Balance{
			Asset:    asset,
			Quantity: qty,
			PriceUSD: price,
			USDValue: usd,
		})
	}
	return out
}

// ===================== real Binance SDK fetcher =====================

type realFetcher struct {
	client *binsdk.Client
}

// Account fetches all non-zero balances from the user's Binance spot account.
func (f *realFetcher) Account(ctx context.Context) ([]RawBalance, error) {
	acc, err := f.client.NewGetAccountService().OmitZeroBalances(true).Do(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RawBalance, 0, len(acc.Balances))
	for _, b := range acc.Balances {
		free, _ := strconv.ParseFloat(b.Free, 64)     //nolint:errcheck // SDK returns well-formed numeric strings; treat unparsable values as zero
		locked, _ := strconv.ParseFloat(b.Locked, 64) //nolint:errcheck // SDK returns well-formed numeric strings; treat unparsable values as zero
		if free == 0 && locked == 0 {
			continue
		}
		out = append(out, RawBalance{Asset: b.Asset, Free: free, Locked: locked})
	}
	return out, nil
}

// Prices returns a snapshot of every symbol's last traded price keyed by symbol.
func (f *realFetcher) Prices(ctx context.Context) (map[string]float64, error) {
	prices, err := f.client.NewListPricesService().Do(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(prices))
	for _, p := range prices {
		v, err := strconv.ParseFloat(p.Price, 64)
		if err == nil {
			out[p.Symbol] = v
		}
	}
	return out, nil
}
