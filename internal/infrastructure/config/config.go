// Package config loads the entire application configuration from environment
// variables. There are no config files. A populated *Config is provided into
// the fx graph at startup and consumed by every other layer.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/fx"
)

// Module exposes the configuration provider to the fx graph.
var Module = fx.Module("config",
	fx.Provide(Load),
)

// DefiWallet describes a single multi-chain EVM wallet handed to the OKX Web3
// DEX integration. Each entry expands into one BrokerSnapshot account per
// chain.
type DefiWallet struct {
	Name    string   `json:"name"`
	Address string   `json:"address"`
	Chains  []string `json:"chains"` // OKX chainIndex strings, e.g. "1" (eth), "56" (bsc)
}

// FutuConfig groups everything needed to talk to a local OpenD daemon.
type FutuConfig struct {
	Host          string // OpenD host, e.g. 127.0.0.1
	Port          int    // OpenD port, default 11111
	TradePassword string // MD5 hex of the trade password (user pre-computes)
	ConnectionID  string // any unique client identifier
	RSAKeyFile    string // path to RSA private key PEM (matches OpenD's RSA_PrivateKey)
}

// IBKRConfig groups everything needed to talk to a local IB Gateway / TWS.
type IBKRConfig struct {
	Host      string
	Port      int    // 4001 (gateway live), 4002 (gateway paper), 7496 (TWS live), 7497 (TWS paper)
	ClientID  int64  // any positive integer unique per connection
	AccountID string // optional, used to filter positions when the gateway has multiple
}

// CryptoConfig groups all exchange-specific credentials. Every field is
// optional: an empty key/secret pair simply disables that integration at
// startup.
type CryptoConfig struct {
	// OKX CEX (api.okx.com /api/v5/account/...)
	OKXAPIKey     string
	OKXSecret     string
	OKXPassphrase string

	// Binance Spot (api.binance.com /api/v3/...)
	BinanceAPIKey string
	BinanceSecret string

	// Bitget Spot (api.bitget.com /api/v2/spot/...)
	BitgetAPIKey     string
	BitgetSecret     string
	BitgetPassphrase string

	// Hyperliquid: read-only, only the wallet address is required because
	// Hyperliquid's /info endpoint is fully public.
	HyperliquidWallet string

	// OKX Web3 / DEX (api.okx.com /api/v5/dex/...): a *separate* set of
	// credentials issued under the OKX Web3 product (see
	// https://www.okx.com/web3/build/docs).
	OKXWeb3APIKey     string
	OKXWeb3Secret     string
	OKXWeb3Passphrase string
}

// Config is the single source of truth for runtime configuration.
type Config struct {
	// Required
	DatabaseURL               string
	JWTSecret                 string
	ConnectAuthPublishableKey string
	// SelfHostedUserEmails is the allow-list of email addresses authorized
	// to log in through the synthetic Supabase-compatible OTP endpoints.
	// Sourced from ALLOWED_EMAILS (preferred, comma-separated) with a
	// fallback to the legacy single-value SELF_HOSTED_USER_EMAIL. All
	// entries are lower-cased and trimmed at load time.
	SelfHostedUserEmails []string

	// StaticOTP, when set, is an additional OTP value accepted by the
	// /auth/v1/verify endpoint. Any 6-digit numeric token is also accepted
	// regardless of this setting; this field exists so operators can
	// configure a fixed memorable code for non-numeric flows. Empty disables
	// the static branch (numeric-only validation remains).
	StaticOTP string

	// HTTP server
	ServerPort  int
	LogLevel    string
	LogFormat   string // "console" (default) or "json"
	CORSOrigins []string

	// Auth modes
	StaticTokenMode bool
	TokenTTL        time.Duration

	// Sync
	SyncInterval time.Duration

	// Upstream brokers (direct connections, no bridge)
	Futu FutuConfig
	IBKR IBKRConfig

	// Crypto exchanges (REST APIs, no bridge)
	Crypto CryptoConfig

	// On-chain wallets fanned out through the OKX Web3 DEX integration.
	DefiWallets []DefiWallet
}

// Loader is the function shape used internally; exposed for tests.
type Loader func(key string) (string, bool)

// Load reads the configuration from the process environment using os.LookupEnv.
// It fails when any required variable is missing.
func Load() (*Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom is a testable variant of Load that takes an arbitrary key/value
// resolver instead of relying on the OS environment directly.
func LoadFrom(get Loader) (*Config, error) {
	if get == nil {
		return nil, errors.New("config: loader function is nil")
	}

	cfg := &Config{}
	var missing []string

	// Required
	if v, ok := get("DATABASE_URL"); ok && v != "" {
		cfg.DatabaseURL = v
	} else {
		missing = append(missing, "DATABASE_URL")
	}
	if v, ok := get("JWT_SECRET"); ok && v != "" {
		cfg.JWTSecret = v
	} else {
		missing = append(missing, "JWT_SECRET")
	}
	if v, ok := get("CONNECT_AUTH_PUBLISHABLE_KEY"); ok && v != "" {
		cfg.ConnectAuthPublishableKey = v
	} else {
		missing = append(missing, "CONNECT_AUTH_PUBLISHABLE_KEY")
	}

	// Email allow-list. ALLOWED_EMAILS (comma-separated) is the canonical
	// name; SELF_HOSTED_USER_EMAIL is kept as a single-value alias for
	// backwards compatibility with earlier deployments.
	emails := splitAndTrim(getString(get, "ALLOWED_EMAILS", ""), ",")
	if len(emails) == 0 {
		if v, ok := get("SELF_HOSTED_USER_EMAIL"); ok && strings.TrimSpace(v) != "" {
			emails = []string{strings.TrimSpace(v)}
		}
	}
	for i, e := range emails {
		emails[i] = strings.ToLower(e)
	}
	if len(emails) == 0 {
		missing = append(missing, "ALLOWED_EMAILS")
	}
	cfg.SelfHostedUserEmails = emails

	cfg.StaticOTP = strings.TrimSpace(getString(get, "STATIC_OTP", ""))

	if len(missing) > 0 {
		return nil, fmt.Errorf("config: missing required environment variables: %s",
			strings.Join(missing, ", "))
	}

	// Server
	port, err := getInt(get, "SERVER_PORT", 8080)
	if err != nil {
		return nil, err
	}
	cfg.ServerPort = port
	cfg.LogLevel = getString(get, "LOG_LEVEL", "info")
	cfg.LogFormat = getString(get, "LOG_FORMAT", "console")
	cfg.CORSOrigins = splitAndTrim(getString(get, "CORS_ORIGINS", "*"), ",")

	// Auth modes
	cfg.StaticTokenMode, err = getBool(get, "STATIC_TOKEN_MODE", false)
	if err != nil {
		return nil, err
	}
	ttlSec, err := getInt(get, "TOKEN_TTL_SECONDS", 3600)
	if err != nil {
		return nil, err
	}
	cfg.TokenTTL = time.Duration(ttlSec) * time.Second

	// Sync
	syncMin, err := getInt(get, "SYNC_INTERVAL_MINUTES", 60)
	if err != nil {
		return nil, err
	}
	cfg.SyncInterval = time.Duration(syncMin) * time.Minute

	// Futu OpenD
	futuPort, err := getInt(get, "FUTU_PORT", 11111)
	if err != nil {
		return nil, err
	}
	cfg.Futu = FutuConfig{
		Host:          getString(get, "FUTU_HOST", "127.0.0.1"),
		Port:          futuPort,
		TradePassword: getString(get, "FUTU_TRADE_PASSWORD", ""),
		ConnectionID:  getString(get, "FUTU_CONNECTION_ID", "wealthfolio-connect"),
		RSAKeyFile:    getString(get, "FUTU_RSA_KEY_FILE", ""),
	}

	// IBKR Gateway / TWS
	ibkrPort, err := getInt(get, "IBKR_PORT", 4001)
	if err != nil {
		return nil, err
	}
	ibkrClientID, err := getInt(get, "IBKR_CLIENT_ID", 17)
	if err != nil {
		return nil, err
	}
	cfg.IBKR = IBKRConfig{
		Host:      getString(get, "IBKR_HOST", "127.0.0.1"),
		Port:      ibkrPort,
		ClientID:  int64(ibkrClientID),
		AccountID: getString(get, "IBKR_ACCOUNT_ID", ""),
	}

	// Crypto exchanges (direct REST, no bridge).
	//
	// The README and deploy/k8s manifests document the conventional
	// `*_API_SECRET` names. We treat those as canonical and fall back to
	// the legacy `*_SECRET` aliases for backwards compatibility with
	// existing deployments.
	cfg.Crypto = CryptoConfig{
		OKXAPIKey:         getString(get, "OKX_API_KEY", ""),
		OKXSecret:         firstNonEmpty(get, "OKX_API_SECRET", "OKX_SECRET"),
		OKXPassphrase:     getString(get, "OKX_PASSPHRASE", ""),
		BinanceAPIKey:     getString(get, "BINANCE_API_KEY", ""),
		BinanceSecret:     firstNonEmpty(get, "BINANCE_API_SECRET", "BINANCE_SECRET"),
		BitgetAPIKey:      getString(get, "BITGET_API_KEY", ""),
		BitgetSecret:      firstNonEmpty(get, "BITGET_API_SECRET", "BITGET_SECRET"),
		BitgetPassphrase:  getString(get, "BITGET_PASSPHRASE", ""),
		HyperliquidWallet: getString(get, "HYPERLIQUID_WALLET", ""),
		OKXWeb3APIKey:     getString(get, "OKX_WEB3_API_KEY", ""),
		OKXWeb3Secret:     firstNonEmpty(get, "OKX_WEB3_API_SECRET", "OKX_WEB3_SECRET"),
		OKXWeb3Passphrase: getString(get, "OKX_WEB3_PASSPHRASE", ""),
	}

	// DeFi wallets (consumed by the OKX Web3 integration).
	if raw, ok := get("DEFI_WALLETS"); ok && strings.TrimSpace(raw) != "" {
		var wallets []DefiWallet
		if err := json.Unmarshal([]byte(raw), &wallets); err != nil {
			return nil, fmt.Errorf("config: DEFI_WALLETS is not valid JSON: %w", err)
		}
		cfg.DefiWallets = wallets
	}

	return cfg, nil
}

func getString(get Loader, key, def string) string {
	if v, ok := get(key); ok && v != "" {
		return v
	}
	return def
}

// firstNonEmpty returns the value of the first key that resolves to a
// non-empty string, otherwise the empty string. Used to support both the
// canonical (`*_API_SECRET`) and legacy (`*_SECRET`) credential variable
// names for the crypto exchange integrations.
func firstNonEmpty(get Loader, keys ...string) string {
	for _, k := range keys {
		if v, ok := get(k); ok && v != "" {
			return v
		}
	}
	return ""
}

func getInt(get Loader, key string, def int) (int, error) {
	v, ok := get(key)
	if !ok || v == "" {
		return def, nil
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func getBool(get Loader, key string, def bool) (bool, error) {
	v, ok := get(key)
	if !ok || v == "" {
		return def, nil
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("config: %s must be a boolean: %w", key, err)
	}
	return parsed, nil
}

func splitAndTrim(value, sep string) []string {
	parts := strings.Split(value, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
