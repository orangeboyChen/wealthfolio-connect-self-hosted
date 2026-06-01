// Package brokerage contains the core domain entities used by both API and
// sync layers: Connection, Account, Activity and Holding (with their
// embedded value objects).
package brokerage

import "time"

// Currency is a value object describing a monetary currency.
type Currency struct {
	Code string // ISO 4217, e.g. "HKD"
	Name string
}

// SymbolType classifies an instrument (EQUITY, ETF, CRYPTO, ...).
type SymbolType struct {
	Code        string
	Description string
	IsSupported bool
}

// Exchange identifies a venue (HKEX, NASDAQ, OKX, ...).
type Exchange struct {
	Code    string
	MICCode string
	Name    string
	Suffix  string
}

// Symbol is a fully-qualified instrument descriptor.
type Symbol struct {
	Symbol      string // e.g. "00700"
	RawSymbol   string // e.g. "00700.HK"
	Description string
	Name        string
	Type        SymbolType
	Exchange    Exchange
	Currency    Currency
	FIGICode    string
}

// OptionSide is CALL or PUT.
type OptionSide string

// OptionSide values.
const (
	OptionCall OptionSide = "CALL"
	OptionPut  OptionSide = "PUT"
)

// OptionSymbol describes an option contract.
type OptionSymbol struct {
	Ticker         string
	OptionType     OptionSide
	StrikePrice    float64
	ExpirationDate time.Time
	IsMiniOption   bool
	Underlying     Symbol
}

// ConnectionStatus reflects whether a broker connection is healthy.
type ConnectionStatus string

// ConnectionStatus values.
const (
	ConnectionActive       ConnectionStatus = "active"
	ConnectionDisconnected ConnectionStatus = "disconnected"
	ConnectionError        ConnectionStatus = "error"
)

// Connection is a single brokerage authorization (Futu, IBKR, OKX, ...).
type Connection struct {
	ID              string
	AuthorizationID string
	BrokerageName   string
	BrokerageSlug   string
	DisplayName     string
	LogoURL         string
	SquareLogoURL   string
	Disabled        bool
	Name            string
	Status          ConnectionStatus
	UpdatedAt       time.Time
}

// AccountType is the canonical Wealthfolio account type after mapping.
type AccountType string

// AccountType values.
const (
	AccountTypeSecurities     AccountType = "SECURITIES"
	AccountTypeMargin         AccountType = "MARGIN"
	AccountTypeCash           AccountType = "CASH"
	AccountTypeCryptocurrency AccountType = "CRYPTOCURRENCY"
	AccountTypeCreditCard     AccountType = "CREDIT_CARD"
)

// Account is a single account inside a Connection.
type Account struct {
	ID                     string
	Name                   string
	AccountNumber          string
	Type                   AccountType
	RawType                string
	Currency               string
	BalanceTotal           float64
	BalanceCurrency        string
	BrokerageAuthorization string
	InstitutionName        string
	SyncEnabled            bool
	SharedWithHousehold    bool
	IsPaper                bool
	Status                 string
	CreatedDate            time.Time
	LastTxSync             *time.Time
	LastHoldingsSync       *time.Time
	FirstTxDate            *time.Time
	InitialTxSyncDone      bool
	InitialHoldingsDone    bool
	OwnerUserID            string
	OwnerFullName          string
	OwnerEmail             string
}

// ActivityType is the canonical activity type.
type ActivityType string

// ActivityType values.
const (
	ActivityBuy              ActivityType = "BUY"
	ActivitySell             ActivityType = "SELL"
	ActivityDividend         ActivityType = "DIVIDEND"
	ActivityInterest         ActivityType = "INTEREST"
	ActivityDeposit          ActivityType = "DEPOSIT"
	ActivityWithdrawal       ActivityType = "WITHDRAWAL"
	ActivityTransferIn       ActivityType = "TRANSFER_IN"
	ActivityTransferOut      ActivityType = "TRANSFER_OUT"
	ActivityFee              ActivityType = "FEE"
	ActivityTax              ActivityType = "TAX"
	ActivitySplit            ActivityType = "SPLIT"
	ActivityConversion       ActivityType = "CONVERSION"
	ActivityOptionBuy        ActivityType = "OPTION_BUY"
	ActivityOptionSell       ActivityType = "OPTION_SELL"
	ActivityOptionExpiry     ActivityType = "OPTION_EXPIRY"
	ActivityOptionAssignment ActivityType = "OPTION_ASSIGNMENT"
)

// Activity is one line of trade history.
type Activity struct {
	ID                  string
	AccountID           string
	Symbol              *Symbol
	OptionSymbol        *OptionSymbol
	Price               float64
	Units               float64
	Amount              float64
	Currency            Currency
	Type                ActivityType
	Subtype             string
	RawType             string
	OptionType          string
	Description         string
	TradeDate           time.Time
	SettlementDate      *time.Time
	Fee                 float64
	FxRate              *float64
	Institution         string
	ExternalReferenceID string
	ProviderType        string
	SourceSystem        string
	SourceRecordID      string
	SourceGroupID       string
	NeedsReview         bool
}

// Position is one current holding line.
type Position struct {
	Symbol               Symbol
	Units                float64
	Price                float64
	OpenPnL              float64
	AveragePurchasePrice float64
	Currency             Currency
	CashEquivalent       bool
}

// OptionPosition is a derivatives holding.
type OptionPosition struct {
	OptionSymbol         OptionSymbol
	Units                float64
	Price                float64
	AveragePurchasePrice float64
	Currency             Currency
}

// Balance is per-currency cash & buying power.
type Balance struct {
	Currency    Currency
	Cash        float64
	BuyingPower float64
}

// Holdings is the latest snapshot for an Account.
type Holdings struct {
	AccountID       string
	Balances        []Balance
	Positions       []Position
	OptionPositions []OptionPosition
	CapturedAt      time.Time
}
