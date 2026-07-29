// Package config loads the entire application configuration from environment
// variables. There are no config files. A populated *Config is provided into
// the fx graph at startup and consumed by every other layer.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	Enabled       bool
	Host          string // OpenD host, e.g. 127.0.0.1
	Port          int    // OpenD port, default 11111
	TradePassword string // MD5 hex of the trade password (user pre-computes)
	ConnectionID  string // any unique client identifier
	RSAKeyFile    string // path to RSA private key PEM (matches OpenD's RSA_PrivateKey)
}

// IBKRConfig groups everything needed to talk to a local IB Gateway / TWS.
type IBKRConfig struct {
	Enabled   bool
	Host      string
	Port      int    // 4001 (gateway live), 4002 (gateway paper), 7496 (TWS live), 7497 (TWS paper)
	ClientID  int64  // any positive integer unique per connection
	AccountID string // optional, used to filter positions when the gateway has multiple
}

// SnapTradeConfig groups the read-only SnapTrade importer settings.
type SnapTradeConfig struct {
	Enabled               bool
	AuthMode              string
	Package               string
	ClientID              string
	ConsumerKey           string
	UserID                string
	UserSecret            string
	BaseURL               string
	AccountIDs            []string
	HistoryStartDate      time.Time
	SyncInterval          time.Duration
	RequestInterval       time.Duration
	RequestsPerMinute     int
	AccountRequestsPM     int
	SafetyPercent         int
	PageSize              int
	MaxRetries            int
	RetryBaseDelay        time.Duration
	RetryMaxDelay         time.Duration
	IncrementalOverlap    time.Duration
	RequestTimeout        time.Duration
	AllowManualRefresh    bool
	AllowTransactionSync  bool
	ManualRefreshCooldown time.Duration
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
	Futu      FutuConfig
	IBKR      IBKRConfig
	SnapTrade SnapTradeConfig

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
	cfg.StaticTokenMode, err = getBool(get, "STATIC_TOKEN_MODE")
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
	futuEnabled, err := getBool(get, "FUTU_ENABLED")
	if err != nil {
		return nil, err
	}
	futuPort, err := getInt(get, "FUTU_PORT", 11111)
	if err != nil {
		return nil, err
	}
	cfg.Futu = FutuConfig{
		Enabled:       futuEnabled,
		Host:          getString(get, "FUTU_HOST", "127.0.0.1"),
		Port:          futuPort,
		TradePassword: getString(get, "FUTU_TRADE_PASSWORD", ""),
		ConnectionID:  getString(get, "FUTU_CONNECTION_ID", "wealthfolio-connect"),
		RSAKeyFile:    getString(get, "FUTU_RSA_KEY_FILE", ""),
	}

	// IBKR Gateway / TWS
	ibkrEnabled, err := getBool(get, "IBKR_ENABLED")
	if err != nil {
		return nil, err
	}
	ibkrPort, err := getInt(get, "IBKR_PORT", 4001)
	if err != nil {
		return nil, err
	}
	ibkrClientID, err := getInt(get, "IBKR_CLIENT_ID", 17)
	if err != nil {
		return nil, err
	}
	cfg.IBKR = IBKRConfig{
		Enabled:   ibkrEnabled,
		Host:      getString(get, "IBKR_HOST", "127.0.0.1"),
		Port:      ibkrPort,
		ClientID:  int64(ibkrClientID),
		AccountID: getString(get, "IBKR_ACCOUNT_ID", ""),
	}

	// SnapTrade (read-only access to accounts connected outside this service).
	cfg.SnapTrade, err = loadSnapTrade(get)
	if err != nil {
		return nil, err
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

func loadSnapTrade(get Loader) (SnapTradeConfig, error) {
	var cfg SnapTradeConfig
	var err error
	cfg.Enabled, err = getBool(get, "SNAPTRADE_ENABLED")
	if err != nil {
		return cfg, err
	}
	cfg.AuthMode = strings.ToLower(strings.TrimSpace(getString(get, "SNAPTRADE_AUTH_MODE", "auto")))
	cfg.Package = strings.ToLower(strings.TrimSpace(getString(get, "SNAPTRADE_PACKAGE", "personal")))
	cfg.ClientID = strings.TrimSpace(getString(get, "SNAPTRADE_CLIENT_ID", ""))
	cfg.ConsumerKey = strings.TrimSpace(getString(get, "SNAPTRADE_CONSUMER_KEY", ""))
	cfg.UserID = strings.TrimSpace(getString(get, "SNAPTRADE_USER_ID", ""))
	cfg.UserSecret = strings.TrimSpace(getString(get, "SNAPTRADE_USER_SECRET", ""))
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(getString(get, "SNAPTRADE_BASE_URL", "https://api.snaptrade.com")), "/")
	cfg.AccountIDs = splitAndTrim(getString(get, "SNAPTRADE_ACCOUNT_IDS", ""), ",")

	cfg.HistoryStartDate, err = parseSnapTradeDate(getString(get, "SNAPTRADE_HISTORY_START_DATE", "01.01.2022"))
	if err != nil {
		return cfg, err
	}
	syncMinutes, err := getInt(get, "SNAPTRADE_SYNC_INTERVAL_MINUTES", 240)
	if err != nil {
		return cfg, err
	}
	requestSeconds, err := getInt(get, "SNAPTRADE_REQUEST_INTERVAL_SECONDS", 60)
	if err != nil {
		return cfg, err
	}
	cfg.RequestsPerMinute, err = getInt(get, "SNAPTRADE_REQUESTS_PER_MINUTE", 0)
	if err != nil {
		return cfg, err
	}
	cfg.AccountRequestsPM, err = getInt(get, "SNAPTRADE_ACCOUNT_REQUESTS_PER_MINUTE", 0)
	if err != nil {
		return cfg, err
	}
	cfg.SafetyPercent, err = getInt(get, "SNAPTRADE_RATE_LIMIT_SAFETY_PERCENT", 80)
	if err != nil {
		return cfg, err
	}
	cfg.PageSize, err = getInt(get, "SNAPTRADE_ACTIVITY_PAGE_SIZE", 1000)
	if err != nil {
		return cfg, err
	}
	cfg.MaxRetries, err = getInt(get, "SNAPTRADE_MAX_RETRIES", 5)
	if err != nil {
		return cfg, err
	}
	retryBaseSeconds, err := getInt(get, "SNAPTRADE_RETRY_BASE_SECONDS", 5)
	if err != nil {
		return cfg, err
	}
	retryMaxSeconds, err := getInt(get, "SNAPTRADE_RETRY_MAX_SECONDS", 300)
	if err != nil {
		return cfg, err
	}
	overlapDays, err := getInt(get, "SNAPTRADE_INCREMENTAL_OVERLAP_DAYS", 7)
	if err != nil {
		return cfg, err
	}
	timeoutSeconds, err := getInt(get, "SNAPTRADE_REQUEST_TIMEOUT_SECONDS", 30)
	if err != nil {
		return cfg, err
	}
	cooldownHours, err := getInt(get, "SNAPTRADE_MANUAL_REFRESH_COOLDOWN_HOURS", 24)
	if err != nil {
		return cfg, err
	}
	cfg.AllowManualRefresh, err = getBool(get, "SNAPTRADE_ALLOW_MANUAL_REFRESH")
	if err != nil {
		return cfg, err
	}
	cfg.AllowTransactionSync, err = getBool(get, "SNAPTRADE_ALLOW_TRANSACTION_SYNC")
	if err != nil {
		return cfg, err
	}

	cfg.SyncInterval = time.Duration(syncMinutes) * time.Minute
	cfg.RequestInterval = time.Duration(requestSeconds) * time.Second
	cfg.RetryBaseDelay = time.Duration(retryBaseSeconds) * time.Second
	cfg.RetryMaxDelay = time.Duration(retryMaxSeconds) * time.Second
	cfg.IncrementalOverlap = time.Duration(overlapDays) * 24 * time.Hour
	cfg.RequestTimeout = time.Duration(timeoutSeconds) * time.Second
	cfg.ManualRefreshCooldown = time.Duration(cooldownHours) * time.Hour

	if err := validateSnapTrade(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func validateSnapTrade(cfg *SnapTradeConfig) error {
	if cfg == nil {
		return errors.New("config: SnapTrade configuration is nil")
	}
	if cfg.AuthMode != "auto" && cfg.AuthMode != "personal" && cfg.AuthMode != "commercial" {
		return fmt.Errorf("config: SNAPTRADE_AUTH_MODE must be auto, personal, or commercial")
	}
	if cfg.Package != "personal" && cfg.Package != "free" && cfg.Package != "payg_realtime" &&
		cfg.Package != "payg_daily" && cfg.Package != "custom" {
		return fmt.Errorf("config: SNAPTRADE_PACKAGE is invalid")
	}
	hasUserID, hasUserSecret := cfg.UserID != "", cfg.UserSecret != ""
	if hasUserID != hasUserSecret {
		return errors.New("config: SNAPTRADE_USER_ID and SNAPTRADE_USER_SECRET must be configured together")
	}
	if cfg.AuthMode == "auto" {
		if hasUserID && hasUserSecret {
			cfg.AuthMode = "commercial"
		} else {
			cfg.AuthMode = "personal"
		}
	}
	if cfg.Enabled {
		if cfg.ClientID == "" {
			return errors.New("config: SNAPTRADE_CLIENT_ID is required when SnapTrade is enabled")
		}
		if cfg.ConsumerKey == "" {
			return errors.New("config: SNAPTRADE_CONSUMER_KEY is required when SnapTrade is enabled")
		}
		if cfg.AuthMode == "commercial" && (!hasUserID || !hasUserSecret) {
			return errors.New("config: commercial SnapTrade authentication requires SNAPTRADE_USER_ID and SNAPTRADE_USER_SECRET")
		}
	}
	parsedURL, err := url.Parse(cfg.BaseURL)
	if err != nil || parsedURL.Host == "" {
		return errors.New("config: SNAPTRADE_BASE_URL must be an absolute URL")
	}
	if parsedURL.Scheme != "https" && (parsedURL.Scheme != "http" || !isLoopbackHost(parsedURL.Hostname())) {
		return errors.New("config: SNAPTRADE_BASE_URL must use HTTPS (HTTP is allowed only for loopback tests)")
	}
	if cfg.SyncInterval < time.Hour {
		return errors.New("config: SNAPTRADE_SYNC_INTERVAL_MINUTES must be at least 60")
	}
	if cfg.RequestInterval < 0 {
		return errors.New("config: SNAPTRADE_REQUEST_INTERVAL_SECONDS cannot be negative")
	}
	if cfg.RequestsPerMinute < 0 || cfg.AccountRequestsPM < 0 {
		return errors.New("config: SnapTrade request limits cannot be negative")
	}
	if cfg.SafetyPercent < 1 || cfg.SafetyPercent > 95 {
		return errors.New("config: SNAPTRADE_RATE_LIMIT_SAFETY_PERCENT must be between 1 and 95")
	}
	if cfg.PageSize < 1 || cfg.PageSize > 1000 {
		return errors.New("config: SNAPTRADE_ACTIVITY_PAGE_SIZE must be between 1 and 1000")
	}
	if cfg.MaxRetries < 0 || cfg.RetryBaseDelay <= 0 || cfg.RetryMaxDelay < cfg.RetryBaseDelay {
		return errors.New("config: SnapTrade retry settings are invalid")
	}
	if cfg.IncrementalOverlap < 0 || cfg.RequestTimeout <= 0 {
		return errors.New("config: SnapTrade overlap and timeout settings are invalid")
	}
	if cfg.ManualRefreshCooldown < time.Hour {
		return errors.New("config: SNAPTRADE_MANUAL_REFRESH_COOLDOWN_HOURS must be at least 1")
	}
	return nil
}

func parseSnapTradeDate(raw string) (time.Time, error) {
	for _, layout := range []string{"02.01.2006", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, strings.TrimSpace(raw), time.UTC); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("config: SNAPTRADE_HISTORY_START_DATE must use DD.MM.YYYY or YYYY-MM-DD")
}

func isLoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
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

func getBool(get Loader, key string) (bool, error) {
	v, ok := get(key)
	if !ok || v == "" {
		return false, nil
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
