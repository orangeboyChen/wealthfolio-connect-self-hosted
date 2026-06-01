package config_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

func mapLoader(m map[string]string) config.Loader {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

var _ = Describe("Config.LoadFrom", func() {
	var base map[string]string

	BeforeEach(func() {
		base = map[string]string{
			"DATABASE_URL":                 "postgres://localhost/x",
			"JWT_SECRET":                   "secret",
			"CONNECT_AUTH_PUBLISHABLE_KEY": "publishable",
			"ALLOWED_EMAILS":               "me@example.com",
		}
	})

	Context("with all required vars present", func() {
		It("returns a Config populated with required values and sane defaults", func() {
			cfg, err := config.LoadFrom(mapLoader(base))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.DatabaseURL).To(Equal("postgres://localhost/x"))
			Expect(cfg.JWTSecret).To(Equal("secret"))
			Expect(cfg.ConnectAuthPublishableKey).To(Equal("publishable"))
			Expect(cfg.ServerPort).To(Equal(8080))
			Expect(cfg.LogLevel).To(Equal("info"))
			Expect(cfg.CORSOrigins).To(Equal([]string{"*"}))
			Expect(cfg.StaticTokenMode).To(BeFalse())
			Expect(cfg.TokenTTL).To(Equal(time.Hour))
			Expect(cfg.SyncInterval).To(Equal(60 * time.Minute))
			Expect(cfg.Futu.Host).To(Equal("127.0.0.1"))
			Expect(cfg.Futu.Port).To(Equal(11111))
			Expect(cfg.IBKR.Host).To(Equal("127.0.0.1"))
			Expect(cfg.IBKR.Port).To(Equal(4001))
			Expect(cfg.IBKR.ClientID).To(Equal(int64(17)))
			Expect(cfg.DefiWallets).To(BeEmpty())
		})
	})

	Context("when a required variable is missing", func() {
		It("fails with an error listing all missing variables", func() {
			_, err := config.LoadFrom(mapLoader(map[string]string{}))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("DATABASE_URL"))
			Expect(err.Error()).To(ContainSubstring("JWT_SECRET"))
			Expect(err.Error()).To(ContainSubstring("CONNECT_AUTH_PUBLISHABLE_KEY"))
			Expect(err.Error()).To(ContainSubstring("ALLOWED_EMAILS"))
		})
	})

	Context("with the email allow-list", func() {
		It("parses ALLOWED_EMAILS as a comma-separated list and lower-cases entries", func() {
			env := mergeMaps(base, map[string]string{
				"ALLOWED_EMAILS": "Alice@Example.com, BOB@example.com",
			})
			cfg, err := config.LoadFrom(mapLoader(env))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.SelfHostedUserEmails).To(Equal([]string{"alice@example.com", "bob@example.com"}))
		})

		It("falls back to legacy SELF_HOSTED_USER_EMAIL when ALLOWED_EMAILS is unset", func() {
			env := mergeMaps(base, map[string]string{
				"ALLOWED_EMAILS":         "",
				"SELF_HOSTED_USER_EMAIL": "Solo@Example.com",
			})
			cfg, err := config.LoadFrom(mapLoader(env))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.SelfHostedUserEmails).To(Equal([]string{"solo@example.com"}))
		})

		It("reads STATIC_OTP when configured", func() {
			env := mergeMaps(base, map[string]string{"STATIC_OTP": " 123456 "})
			cfg, err := config.LoadFrom(mapLoader(env))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.StaticOTP).To(Equal("123456"))
		})
	})

	Context("with a nil loader", func() {
		It("returns an explicit error", func() {
			_, err := config.LoadFrom(nil)
			Expect(err).To(MatchError(ContainSubstring("loader function is nil")))
		})
	})

	Context("with optional values overridden", func() {
		It("parses ints, bools and CSV correctly", func() {
			env := mergeMaps(base, map[string]string{
				"SERVER_PORT":           "9090",
				"LOG_LEVEL":             "debug",
				"CORS_ORIGINS":          "https://a.com, https://b.com ,",
				"STATIC_TOKEN_MODE":     "true",
				"TOKEN_TTL_SECONDS":     "60",
				"SYNC_INTERVAL_MINUTES": "5",
				"FUTU_HOST":             "opend.local",
				"FUTU_PORT":             "11112",
				"FUTU_TRADE_PASSWORD":   "hunter2",
				"IBKR_HOST":             "ibgw.local",
				"IBKR_PORT":             "4002",
				"IBKR_CLIENT_ID":        "42",
				"OKX_API_KEY":           "k",
				"OKX_PASSPHRASE":        "pp",
				"BINANCE_API_KEY":       "kk",
				"BITGET_PASSPHRASE":     "bgpp",
				"HYPERLIQUID_WALLET":    "0xabc",
				"OKX_WEB3_API_KEY":      "wk",
			})
			cfg, err := config.LoadFrom(mapLoader(env))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.ServerPort).To(Equal(9090))
			Expect(cfg.LogLevel).To(Equal("debug"))
			Expect(cfg.CORSOrigins).To(Equal([]string{"https://a.com", "https://b.com"}))
			Expect(cfg.StaticTokenMode).To(BeTrue())
			Expect(cfg.TokenTTL).To(Equal(60 * time.Second))
			Expect(cfg.SyncInterval).To(Equal(5 * time.Minute))
			Expect(cfg.Futu.Host).To(Equal("opend.local"))
			Expect(cfg.Futu.Port).To(Equal(11112))
			Expect(cfg.Futu.TradePassword).To(Equal("hunter2"))
			Expect(cfg.IBKR.Host).To(Equal("ibgw.local"))
			Expect(cfg.IBKR.Port).To(Equal(4002))
			Expect(cfg.IBKR.ClientID).To(Equal(int64(42)))
			Expect(cfg.Crypto.OKXAPIKey).To(Equal("k"))
			Expect(cfg.Crypto.OKXPassphrase).To(Equal("pp"))
			Expect(cfg.Crypto.BinanceAPIKey).To(Equal("kk"))
			Expect(cfg.Crypto.BitgetPassphrase).To(Equal("bgpp"))
			Expect(cfg.Crypto.HyperliquidWallet).To(Equal("0xabc"))
			Expect(cfg.Crypto.OKXWeb3APIKey).To(Equal("wk"))
		})

		It("rejects non-integer SERVER_PORT", func() {
			env := mergeMaps(base, map[string]string{"SERVER_PORT": "abc"})
			_, err := config.LoadFrom(mapLoader(env))
			Expect(err).To(MatchError(ContainSubstring("SERVER_PORT")))
		})

		It("rejects non-bool STATIC_TOKEN_MODE", func() {
			env := mergeMaps(base, map[string]string{"STATIC_TOKEN_MODE": "yepp"})
			_, err := config.LoadFrom(mapLoader(env))
			Expect(err).To(MatchError(ContainSubstring("STATIC_TOKEN_MODE")))
		})

		It("rejects non-integer TOKEN_TTL_SECONDS", func() {
			env := mergeMaps(base, map[string]string{"TOKEN_TTL_SECONDS": "x"})
			_, err := config.LoadFrom(mapLoader(env))
			Expect(err).To(MatchError(ContainSubstring("TOKEN_TTL_SECONDS")))
		})

		It("rejects non-integer SYNC_INTERVAL_MINUTES", func() {
			env := mergeMaps(base, map[string]string{"SYNC_INTERVAL_MINUTES": "x"})
			_, err := config.LoadFrom(mapLoader(env))
			Expect(err).To(MatchError(ContainSubstring("SYNC_INTERVAL_MINUTES")))
		})
	})

	Context("with DEFI_WALLETS", func() {
		It("parses a valid JSON array", func() {
			env := mergeMaps(base, map[string]string{
				"DEFI_WALLETS": `[{"name":"Main","address":"0x1","chains":["eth","arbitrum"]}]`,
			})
			cfg, err := config.LoadFrom(mapLoader(env))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.DefiWallets).To(HaveLen(1))
			Expect(cfg.DefiWallets[0].Name).To(Equal("Main"))
			Expect(cfg.DefiWallets[0].Address).To(Equal("0x1"))
			Expect(cfg.DefiWallets[0].Chains).To(Equal([]string{"eth", "arbitrum"}))
		})

		It("rejects malformed JSON", func() {
			env := mergeMaps(base, map[string]string{"DEFI_WALLETS": "not-json"})
			_, err := config.LoadFrom(mapLoader(env))
			Expect(err).To(MatchError(ContainSubstring("DEFI_WALLETS")))
		})

		It("ignores empty value", func() {
			env := mergeMaps(base, map[string]string{"DEFI_WALLETS": "   "})
			cfg, err := config.LoadFrom(mapLoader(env))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.DefiWallets).To(BeEmpty())
		})
	})
})

var _ = Describe("Config.Load (process env)", func() {
	It("delegates to os.LookupEnv and surfaces missing vars when nothing is set", func() {
		GinkgoT().Setenv("DATABASE_URL", "")
		GinkgoT().Setenv("JWT_SECRET", "")
		GinkgoT().Setenv("CONNECT_AUTH_PUBLISHABLE_KEY", "")
		_, err := config.Load()
		Expect(err).To(HaveOccurred())
	})

	It("succeeds when all required vars are set", func() {
		GinkgoT().Setenv("DATABASE_URL", "postgres://x")
		GinkgoT().Setenv("JWT_SECRET", "s")
		GinkgoT().Setenv("CONNECT_AUTH_PUBLISHABLE_KEY", "p")
		GinkgoT().Setenv("ALLOWED_EMAILS", "me@example.com")
		cfg, err := config.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
	})
})

func mergeMaps(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
