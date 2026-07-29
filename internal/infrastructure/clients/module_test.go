package clients_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

func TestClientsModule(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Clients Module Suite")
}

// sampleCfg builds a config with every integration's credentials populated
// just enough to drive the constructors. Real network calls are never made
// from these tests — they only verify the wiring is consistent.
func sampleCfg() *config.Config {
	return &config.Config{
		Futu: config.FutuConfig{
			Enabled: true, Host: "127.0.0.1", Port: 11111,
			TradePassword: "secret", ConnectionID: "wftest",
		},
		IBKR: config.IBKRConfig{
			Enabled: true, Host: "127.0.0.1", Port: 4002, ClientID: 17,
		},
		Crypto: config.CryptoConfig{
			BinanceAPIKey: "bk", BinanceSecret: "bs",
			OKXAPIKey: "ok", OKXSecret: "os", OKXPassphrase: "op",
			BitgetAPIKey: "gk", BitgetSecret: "gs", BitgetPassphrase: "gp",
			HyperliquidWallet: "0x1111",
			OKXWeb3APIKey:     "wk", OKXWeb3Secret: "ws", OKXWeb3Passphrase: "wp",
		},
		DefiWallets: []config.DefiWallet{
			{Name: "Main", Address: "0xabc", Chains: []string{"1", "42161"}},
			{Name: "Cold", Address: "0xdef", Chains: []string{"137"}},
		},
	}
}

var _ = Describe("Client constructors", func() {
	cfg := sampleCfg()

	It("Futu client returns slug futu", func() {
		Expect(clients.NewFutu(cfg, zerolog.Nop()).ID()).To(Equal("futu"))
	})
	It("IBKR client returns slug ibkr", func() {
		Expect(clients.NewIBKR(cfg).ID()).To(Equal("ibkr"))
	})
	It("OKX CEX client returns slug okx", func() {
		Expect(clients.NewOKXCEX(cfg).ID()).To(Equal("okx"))
	})
	It("Binance client returns slug binance", func() {
		Expect(clients.NewBinance(cfg).ID()).To(Equal("binance"))
	})
	It("Bitget client returns slug bitget", func() {
		Expect(clients.NewBitget(cfg).ID()).To(Equal("bitget"))
	})
	It("Hyperliquid client returns slug hyperliquid", func() {
		Expect(clients.NewHyperliquid(cfg).ID()).To(Equal("hyperliquid"))
	})
	It("OKX Web3 client returns slug okx_web3", func() {
		Expect(clients.NewOKXWeb3(cfg, zerolog.Nop()).ID()).To(Equal("okx_web3"))
	})
	It("Module is non-nil", func() {
		Expect(clients.Module).NotTo(BeNil())
	})
	It("omits every unconfigured direct integration", func() {
		out := clients.NewConfiguredClients(&config.Config{}, zerolog.Nop())
		Expect(out.Clients).To(BeEmpty())
	})
	It("registers every fully configured direct integration", func() {
		out := clients.NewConfiguredClients(cfg, zerolog.Nop())
		Expect(out.Clients).To(HaveLen(7))
		ids := make([]string, 0, len(out.Clients))
		for _, client := range out.Clients {
			ids = append(ids, client.ID())
		}
		Expect(ids).To(ConsistOf("futu", "ibkr", "binance", "okx", "bitget", "hyperliquid", "okx_web3"))
	})
	It("omits SnapTrade cleanly when disabled", func() {
		out, err := clients.NewSnapTrade(cfg, zerolog.Nop())
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Clients).To(BeEmpty())
	})
	It("registers SnapTrade only when enabled", func() {
		enabled := *cfg
		enabled.SnapTrade = config.SnapTradeConfig{
			Enabled: true, AuthMode: "personal", Package: "personal", ClientID: "client", ConsumerKey: "consumer",
			BaseURL: "https://api.snaptrade.com", SyncInterval: time.Hour, RequestInterval: time.Minute,
			RequestTimeout: time.Second, SafetyPercent: 80, RetryBaseDelay: time.Second, RetryMaxDelay: time.Minute,
		}
		out, err := clients.NewSnapTrade(&enabled, zerolog.Nop())
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Clients).To(HaveLen(1))
		Expect(out.Clients[0].ID()).To(Equal("snaptrade"))
	})
})
