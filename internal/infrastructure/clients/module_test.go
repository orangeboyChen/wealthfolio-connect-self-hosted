package clients_test

import (
	"testing"

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
			Host: "127.0.0.1", Port: 11111,
			TradePassword: "secret", ConnectionID: "wftest",
		},
		IBKR: config.IBKRConfig{
			Host: "127.0.0.1", Port: 4002, ClientID: 17,
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
})
