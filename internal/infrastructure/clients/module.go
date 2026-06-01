// Package clients wires every concrete BrokerClient (Futu, IBKR, Binance,
// OKX-CEX, OKX-Web3, Bitget, Hyperliquid) into the broker_clients fx group.
// Individual clients live under infrastructure/clients/<name>; this file
// is the composition root.
package clients

import (
	"os"

	"github.com/rs/zerolog"
	"go.uber.org/fx"

	appsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/binance"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/bitget"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/futu"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/hyperliquid"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/ibkr"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients/okx"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

// Module registers every BrokerClient into the broker_clients fx group.
//
// Every constructor below is wrapped in appsync.AsBrokerClient so it lands
// in the same fx group consumed by application/sync.Service. Constructors
// always return a *Client so unit tests can also reach the concrete type.
var Module = fx.Module("infrastructure.clients",
	fx.Provide(
		appsync.AsBrokerClient(NewFutu),
		appsync.AsBrokerClient(NewIBKR),
		appsync.AsBrokerClient(NewBinance),
		appsync.AsBrokerClient(NewOKXCEX),
		appsync.AsBrokerClient(NewBitget),
		appsync.AsBrokerClient(NewHyperliquid),
		appsync.AsBrokerClient(NewOKXWeb3),
	),
)

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
