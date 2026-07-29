package snaptrade

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

const (
	authModePersonal     = "personal"
	authModeCommercial   = "commercial"
	ibkrBrokerageSlug    = "IBKR"
	ibkrInstitutionName  = "Interactive Brokers"
	instrumentKindCash   = "cash"
	activityTypeDividend = "DIVIDEND"
)

type decimal struct {
	Value float64
	Valid bool
}

// UnmarshalJSON accepts the number and numeric-string forms returned by SnapTrade.
func (d *decimal) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte(`""`)) {
		return nil
	}
	var number json.Number
	if len(data) > 0 && data[0] == '"' {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		if raw == "" {
			return nil
		}
		number = json.Number(raw)
	} else {
		number = json.Number(string(data))
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil {
		return fmt.Errorf("invalid decimal: %w", err)
	}
	d.Value = value
	d.Valid = true
	return nil
}

type rawBrokerage struct {
	ID              string `json:"id"`
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	DisplayName     string `json:"display_name"`
	LogoURL         string `json:"aws_s3_logo_url"`
	SquareLogoURL   string `json:"aws_s3_square_logo_url"`
	Enabled         bool   `json:"enabled"`
	MaintenanceMode bool   `json:"maintenance_mode"`
}

type rawConnection struct {
	ID          string       `json:"id"`
	CreatedDate string       `json:"created_date"`
	Brokerage   rawBrokerage `json:"brokerage"`
	Name        string       `json:"name"`
	Disabled    bool         `json:"disabled"`
	UpdatedDate string       `json:"updated_date"`
}

type rawMoney struct {
	Amount   decimal `json:"amount"`
	Currency string  `json:"currency"`
}

type rawSyncPart struct {
	InitialSyncCompleted bool    `json:"initial_sync_completed"`
	LastSuccessfulSync   *string `json:"last_successful_sync"`
	FirstTransactionDate *string `json:"first_transaction_date"`
	HoldingsUnavailable  bool    `json:"holdings_unavailable"`
}

type rawSyncStatus struct {
	Transactions rawSyncPart `json:"transactions"`
	Holdings     rawSyncPart `json:"holdings"`
}

type rawAccount struct {
	ID                     string        `json:"id"`
	BrokerageAuthorization string        `json:"brokerage_authorization"`
	Name                   string        `json:"name"`
	Number                 string        `json:"number"`
	InstitutionAccountID   string        `json:"institution_account_id"`
	InstitutionName        string        `json:"institution_name"`
	CreatedDate            string        `json:"created_date"`
	OpeningDate            *string       `json:"opening_date"`
	SyncStatus             rawSyncStatus `json:"sync_status"`
	Balance                struct {
		Total *rawMoney `json:"total"`
	} `json:"balance"`
	Status          string `json:"status"`
	RawType         string `json:"raw_type"`
	AccountCategory string `json:"account_category"`
	IsPaper         bool   `json:"is_paper"`
}

type rawCurrency struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

type rawExchange struct {
	Code    string `json:"code"`
	MICCode string `json:"mic_code"`
	Name    string `json:"name"`
	Suffix  string `json:"suffix"`
}

type rawSecurityType struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

type rawFIGI struct {
	FIGICode string `json:"figi_code"`
}

type rawUniversalSymbol struct {
	ID             string          `json:"id"`
	Symbol         string          `json:"symbol"`
	RawSymbol      string          `json:"raw_symbol"`
	Description    string          `json:"description"`
	Currency       rawCurrency     `json:"currency"`
	Exchange       rawExchange     `json:"exchange"`
	Type           rawSecurityType `json:"type"`
	FIGICode       string          `json:"figi_code"`
	FIGIInstrument *rawFIGI        `json:"figi_instrument"`
}

type rawOptionSymbol struct {
	ID               string              `json:"id"`
	Ticker           string              `json:"ticker"`
	OptionType       string              `json:"option_type"`
	StrikePrice      decimal             `json:"strike_price"`
	ExpirationDate   string              `json:"expiration_date"`
	IsMiniOption     bool                `json:"is_mini_option"`
	UnderlyingSymbol *rawUniversalSymbol `json:"underlying_symbol"`
}

type rawBalance struct {
	Currency    rawCurrency `json:"currency"`
	Cash        decimal     `json:"cash"`
	BuyingPower decimal     `json:"buying_power"`
}

type rawInstrument struct {
	Kind             string              `json:"kind"`
	ID               string              `json:"id"`
	Symbol           string              `json:"symbol"`
	RawSymbol        string              `json:"raw_symbol"`
	Description      string              `json:"description"`
	Currency         string              `json:"currency"`
	Exchange         string              `json:"exchange"`
	FIGIInstrument   *rawFIGI            `json:"figi_instrument"`
	Ticker           string              `json:"ticker"`
	OptionType       string              `json:"option_type"`
	StrikePrice      decimal             `json:"strike_price"`
	ExpirationDate   string              `json:"expiration_date"`
	IsMiniOption     bool                `json:"is_mini_option"`
	UnderlyingSymbol *rawUniversalSymbol `json:"underlying_symbol"`
}

type rawPosition struct {
	Instrument     rawInstrument `json:"instrument"`
	Units          decimal       `json:"units"`
	Price          decimal       `json:"price"`
	CostBasis      decimal       `json:"cost_basis"`
	Currency       string        `json:"currency"`
	CashEquivalent bool          `json:"cash_equivalent"`
}

type rawPositionsResponse struct {
	Results []rawPosition `json:"results"`
}

type rawActivity struct {
	ID                      string              `json:"id"`
	Symbol                  *rawUniversalSymbol `json:"symbol"`
	CurrencyUniversalSymbol *rawUniversalSymbol `json:"currency_universal_symbol"`
	OptionSymbol            *rawOptionSymbol    `json:"option_symbol"`
	Price                   decimal             `json:"price"`
	Units                   decimal             `json:"units"`
	Amount                  decimal             `json:"amount"`
	Currency                *rawCurrency        `json:"currency"`
	Type                    string              `json:"type"`
	OptionType              string              `json:"option_type"`
	Description             string              `json:"description"`
	TradeDate               *string             `json:"trade_date"`
	SettlementDate          *string             `json:"settlement_date"`
	Fee                     decimal             `json:"fee"`
	FXRate                  decimal             `json:"fx_rate"`
	Institution             string              `json:"institution"`
	ExternalReferenceID     string              `json:"external_reference_id"`
}

type rawPagination struct {
	Offset int  `json:"offset"`
	Limit  int  `json:"limit"`
	Total  *int `json:"total"`
}

type rawActivityPage struct {
	Data       []rawActivity `json:"data"`
	Pagination rawPagination `json:"pagination"`
}
