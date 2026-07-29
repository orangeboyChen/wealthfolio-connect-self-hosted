// Package clients wires every concrete BrokerClient (Futu, IBKR, Binance,
// OKX-CEX, OKX-Web3, Bitget, Hyperliquid) into the broker_clients fx group.
// Individual clients live under infrastructure/clients/<name>; this file
// is the composition root.
package clients

import (
	"os"

	"github.com/rs/zerolog"
	"go.uber.org/fx"

	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/binance"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/bitget"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/futu"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/hyperliquid"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/ibkr"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/okx"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/snaptrade"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

// Module registers every BrokerClient into the broker_clients fx group.
//
// Configured clients are contributed as a flattened group so disabled
// integrations never enter the scheduler.
var Module = fx.Module("infrastructure.clients",
	fx.Provide(
		NewSnapTrade,
		NewConfiguredClients,
	),
)

// ConfiguredClientsOut contributes only integrations with complete opt-in
// configuration to the shared broker client group.
type ConfiguredClientsOut struct {
	fx.Out
	Clients []domainsync.BrokerClient `group:"broker_clients,flatten"`
}

// NewConfiguredClients constructs the enabled direct-broker and exchange
// clients. Missing credentials disable an integration without noisy retries.
func NewConfiguredClients(cfg *config.Config, log zerolog.Logger) ConfiguredClientsOut {
	if cfg == nil {
		return ConfiguredClientsOut{}
	}
	configured := make([]domainsync.BrokerClient, 0, 7)
	if cfg.Futu.Enabled {
		configured = append(configured, NewFutu(cfg, log))
	}
	if cfg.IBKR.Enabled {
		configured = append(configured, NewIBKR(cfg))
	}
	if allConfigured(cfg.Crypto.BinanceAPIKey, cfg.Crypto.BinanceSecret) {
		configured = append(configured, NewBinance(cfg))
	}
	if allConfigured(cfg.Crypto.OKXAPIKey, cfg.Crypto.OKXSecret, cfg.Crypto.OKXPassphrase) {
		configured = append(configured, NewOKXCEX(cfg))
	}
	if allConfigured(cfg.Crypto.BitgetAPIKey, cfg.Crypto.BitgetSecret, cfg.Crypto.BitgetPassphrase) {
		configured = append(configured, NewBitget(cfg))
	}
	if cfg.Crypto.HyperliquidWallet != "" {
		configured = append(configured, NewHyperliquid(cfg))
	}
	if len(cfg.DefiWallets) > 0 && allConfigured(
		cfg.Crypto.OKXWeb3APIKey,
		cfg.Crypto.OKXWeb3Secret,
		cfg.Crypto.OKXWeb3Passphrase,
	) {
		configured = append(configured, NewOKXWeb3(cfg, log))
	}
	return ConfiguredClientsOut{Clients: configured}
}

func allConfigured(values ...string) bool {
	for _, value := range values {
		if value == "" {
			return false
		}
	}
	return true
}

// SnapTradeOut conditionally contributes the enabled SnapTrade client to the
// shared broker_clients group. An empty slice cleanly disables the integration.
type SnapTradeOut struct {
	fx.Out
	Clients []domainsync.BrokerClient `group:"broker_clients,flatten"`
}

// NewSnapTrade builds the optional SnapTrade client from validated config.
func NewSnapTrade(cfg *config.Config, log zerolog.Logger) (SnapTradeOut, error) {
	if cfg == nil || !cfg.SnapTrade.Enabled {
		return SnapTradeOut{}, nil
	}
	client, err := snaptrade.New(cfg.SnapTrade, log.With().Str("client", "snaptrade").Logger(), nil)
	if err != nil {
		return SnapTradeOut{}, err
	}
	return SnapTradeOut{Clients: []domainsync.BrokerClient{client}}, nil
}

// NewFutu builds the Futu BrokerClient from config.
func NewFutu(cfg *config.Config, log zerolog.Logger) *futu.Client {
	var rsaKey []byte
	if cfg.Futu.RSAKeyFile != "" {
		data, err := os.ReadFile(cfg.Futu.RSAKeyFile)
		if err == nil {
			rsaKey = data
		}
	}
	c := futu.New(cfg.Futu.Host, cfg.Futu.Port, cfg.Futu.TradePassword, cfg.Futu.ConnectionID, rsaKey, nil)
	c.SetLogger(log.With().Str("client", "futu").Logger())
	return c
}

// NewIBKR builds the IBKR BrokerClient from config.
func NewIBKR(cfg *config.Config) *ibkr.Client {
	return ibkr.New(cfg.IBKR.Host, cfg.IBKR.Port, cfg.IBKR.ClientID, cfg.IBKR.AccountID, nil)
}

// NewBinance builds the Binance Spot BrokerClient.
func NewBinance(cfg *config.Config) *binance.Client {
	return binance.New(cfg.Crypto.BinanceAPIKey, cfg.Crypto.BinanceSecret, nil)
}

// NewOKXCEX builds the OKX CEX BrokerClient.
func NewOKXCEX(cfg *config.Config) *okx.CEXClient {
	return okx.NewCEX(okx.Credentials{
		APIKey:     cfg.Crypto.OKXAPIKey,
		Secret:     cfg.Crypto.OKXSecret,
		Passphrase: cfg.Crypto.OKXPassphrase,
	}, "", nil)
}

// NewOKXWeb3 builds the OKX Web3 BrokerClient that fans out across every
// configured wallet. The structured logger is injected so per-wallet fetch
// failures (typically transient OKX outages or chain RPC blips) surface in
// the application log instead of being silently dropped.
func NewOKXWeb3(cfg *config.Config, log zerolog.Logger) *okx.Web3Client {
	wallets := make([]okx.Wallet, 0, len(cfg.DefiWallets))
	for _, w := range cfg.DefiWallets {
		wallets = append(wallets, okx.Wallet{
			Address: w.Address,
			Chains:  w.Chains,
			Label:   w.Name,
		})
	}
	c := okx.NewWeb3(okx.Credentials{
		APIKey:     cfg.Crypto.OKXWeb3APIKey,
		Secret:     cfg.Crypto.OKXWeb3Secret,
		Passphrase: cfg.Crypto.OKXWeb3Passphrase,
	}, wallets, "", nil)
	c.SetLogger(log.With().Str("component", "okx_web3").Logger())
	return c
}

// NewBitget builds the Bitget Spot BrokerClient.
func NewBitget(cfg *config.Config) *bitget.Client {
	return bitget.New(
		cfg.Crypto.BitgetAPIKey,
		cfg.Crypto.BitgetSecret,
		cfg.Crypto.BitgetPassphrase,
		"", nil,
	)
}

// NewHyperliquid builds the Hyperliquid BrokerClient.
func NewHyperliquid(cfg *config.Config) *hyperliquid.Client {
	return hyperliquid.New(cfg.Crypto.HyperliquidWallet, "", nil)
}
