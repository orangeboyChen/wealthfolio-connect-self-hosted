// Package sync defines the ports (interfaces) used by the sync engine to
// reach out to upstream broker / exchange APIs and persist normalized
// snapshots into the domain repositories.
package sync

import (
	"context"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
)

// BrokerSnapshot bundles a single sync result for one upstream broker.
type BrokerSnapshot struct {
	Connection brokerage.Connection
	Accounts   []brokerage.Account
	Holdings   []brokerage.Holdings
	Activities map[string][]brokerage.Activity // keyed by Account ID
}

// BrokerClient is implemented by every concrete upstream integration
// (Futu, IBKR, OKX, Binance, Bitget, Hyperliquid, EVM DeFi, ...).
type BrokerClient interface {
	// ID is a stable slug used in logging and connection rows.
	ID() string
	// Fetch reaches out to the upstream service and returns a fully
	// translated BrokerSnapshot. Fetch should be safe to call concurrently
	// with other clients but does not need to be reentrant for itself.
	Fetch(ctx context.Context) (BrokerSnapshot, error)
}
