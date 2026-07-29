// Activity persistence-object: GORM mapping for the activities table.

package persistence

import (
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
)

// ActivityPO is the GORM mapping for the activities table. Symbol and option
// metadata is flattened into individual columns to match the original schema.
type ActivityPO struct {
	ID             string `gorm:"column:id;primaryKey;type:text"`
	AccountID      string `gorm:"column:account_id;type:text;not null;uniqueIndex:activities_account_source_uk,priority:1;index:activities_account_idx,priority:1;index:activities_account_fingerprint_idx,priority:1"`
	SourceRecordID string `gorm:"column:source_record_id;type:text;not null;uniqueIndex:activities_account_source_uk,priority:2"`

	SymbolTicker         string `gorm:"column:symbol_ticker;type:text;not null;default:''"`
	SymbolRaw            string `gorm:"column:symbol_raw;type:text;not null;default:''"`
	SymbolDescription    string `gorm:"column:symbol_description;type:text;not null;default:''"`
	SymbolName           string `gorm:"column:symbol_name;type:text;not null;default:''"`
	SymbolTypeCode       string `gorm:"column:symbol_type_code;type:text;not null;default:''"`
	SymbolTypeDesc       string `gorm:"column:symbol_type_desc;type:text;not null;default:''"`
	SymbolTypeSupported  bool   `gorm:"column:symbol_type_supported;not null;default:false"`
	SymbolExchangeCode   string `gorm:"column:symbol_exchange_code;type:text;not null;default:''"`
	SymbolExchangeMIC    string `gorm:"column:symbol_exchange_mic;type:text;not null;default:''"`
	SymbolExchangeName   string `gorm:"column:symbol_exchange_name;type:text;not null;default:''"`
	SymbolExchangeSuffix string `gorm:"column:symbol_exchange_suffix;type:text;not null;default:''"`
	SymbolCurrencyCode   string `gorm:"column:symbol_currency_code;type:text;not null;default:''"`
	SymbolCurrencyName   string `gorm:"column:symbol_currency_name;type:text;not null;default:''"`
	SymbolFIGI           string `gorm:"column:symbol_figi;type:text;not null;default:''"`

	CurrencySymbolTicker       string `gorm:"column:currency_symbol_ticker;type:text;not null;default:''"`
	CurrencySymbolRaw          string `gorm:"column:currency_symbol_raw;type:text;not null;default:''"`
	CurrencySymbolDescription  string `gorm:"column:currency_symbol_description;type:text;not null;default:''"`
	CurrencySymbolName         string `gorm:"column:currency_symbol_name;type:text;not null;default:''"`
	CurrencySymbolTypeCode     string `gorm:"column:currency_symbol_type_code;type:text;not null;default:''"`
	CurrencySymbolTypeDesc     string `gorm:"column:currency_symbol_type_desc;type:text;not null;default:''"`
	CurrencySymbolSupported    bool   `gorm:"column:currency_symbol_supported;not null;default:false"`
	CurrencySymbolExchangeCode string `gorm:"column:currency_symbol_exchange_code;type:text;not null;default:''"`
	CurrencySymbolExchangeMIC  string `gorm:"column:currency_symbol_exchange_mic;type:text;not null;default:''"`
	CurrencySymbolExchangeName string `gorm:"column:currency_symbol_exchange_name;type:text;not null;default:''"`
	CurrencySymbolExchangeSfx  string `gorm:"column:currency_symbol_exchange_suffix;type:text;not null;default:''"`
	CurrencySymbolCurrencyCode string `gorm:"column:currency_symbol_currency_code;type:text;not null;default:''"`
	CurrencySymbolCurrencyName string `gorm:"column:currency_symbol_currency_name;type:text;not null;default:''"`
	CurrencySymbolFIGI         string `gorm:"column:currency_symbol_figi;type:text;not null;default:''"`

	OptionTicker               string     `gorm:"column:option_ticker;type:text;not null;default:''"`
	OptionSide                 string     `gorm:"column:option_side;type:text;not null;default:''"`
	OptionStrikePrice          float64    `gorm:"column:option_strike_price;not null;default:0"`
	OptionExpirationDate       *time.Time `gorm:"column:option_expiration_date"`
	OptionIsMini               bool       `gorm:"column:option_is_mini;not null;default:false"`
	OptionUnderlying           string     `gorm:"column:option_underlying;type:text;not null;default:''"`
	OptionUnderlyingRaw        string     `gorm:"column:option_underlying_raw;type:text;not null;default:''"`
	OptionUnderlyingDesc       string     `gorm:"column:option_underlying_description;type:text;not null;default:''"`
	OptionUnderlyingType       string     `gorm:"column:option_underlying_type;type:text;not null;default:''"`
	OptionUnderlyingTypeDesc   string     `gorm:"column:option_underlying_type_description;type:text;not null;default:''"`
	OptionUnderlyingSupported  bool       `gorm:"column:option_underlying_supported;not null;default:false"`
	OptionUnderlyingExch       string     `gorm:"column:option_underlying_exchange;type:text;not null;default:''"`
	OptionUnderlyingMIC        string     `gorm:"column:option_underlying_mic;type:text;not null;default:''"`
	OptionUnderlyingExchName   string     `gorm:"column:option_underlying_exchange_name;type:text;not null;default:''"`
	OptionUnderlyingExchSuffix string     `gorm:"column:option_underlying_exchange_suffix;type:text;not null;default:''"`
	OptionUnderlyingCurr       string     `gorm:"column:option_underlying_currency;type:text;not null;default:''"`
	OptionUnderlyingCurrName   string     `gorm:"column:option_underlying_currency_name;type:text;not null;default:''"`
	OptionUnderlyingFIGI       string     `gorm:"column:option_underlying_figi;type:text;not null;default:''"`

	Price        float64 `gorm:"column:price;not null;default:0"`
	Units        float64 `gorm:"column:units;not null;default:0"`
	Amount       float64 `gorm:"column:amount;not null;default:0"`
	CurrencyCode string  `gorm:"column:currency_code;type:text;not null;default:''"`
	CurrencyName string  `gorm:"column:currency_name;type:text;not null;default:''"`

	Type        string `gorm:"column:type;type:text;not null"`
	Subtype     string `gorm:"column:subtype;type:text;not null;default:''"`
	RawType     string `gorm:"column:raw_type;type:text;not null;default:''"`
	OptionType  string `gorm:"column:option_type;type:text;not null;default:''"`
	Description string `gorm:"column:description;type:text;not null;default:''"`

	TradeDate      time.Time  `gorm:"column:trade_date;not null;index:activities_account_idx,priority:2,sort:desc"`
	SettlementDate *time.Time `gorm:"column:settlement_date"`
	Fee            float64    `gorm:"column:fee;not null;default:0"`
	FxRate         *float64   `gorm:"column:fx_rate"`

	Institution         string `gorm:"column:institution;type:text;not null;default:''"`
	ExternalReferenceID string `gorm:"column:external_reference_id;type:text;not null;default:''"`
	ProviderType        string `gorm:"column:provider_type;type:text;not null;default:'CUSTOM'"`
	SourceSystem        string `gorm:"column:source_system;type:text;not null;default:'CUSTOM'"`
	SourceGroupID       string `gorm:"column:source_group_id;type:text;not null;default:''"`
	SourceFingerprint   string `gorm:"column:source_fingerprint;type:text;not null;default:'';index:activities_account_fingerprint_idx,priority:2"`
	NeedsReview         bool   `gorm:"column:needs_review;not null;default:false"`
}

// TableName pins the GORM-derived table name.
func (ActivityPO) TableName() string { return "activities" }

// ToDomain converts an activity PO into its domain entity counterpart.
func (p ActivityPO) ToDomain() brokerage.Activity {
	a := brokerage.Activity{
		ID:                  p.ID,
		AccountID:           p.AccountID,
		Price:               p.Price,
		Units:               p.Units,
		Amount:              p.Amount,
		Currency:            brokerage.Currency{Code: p.CurrencyCode, Name: p.CurrencyName},
		Type:                brokerage.ActivityType(p.Type),
		Subtype:             p.Subtype,
		RawType:             p.RawType,
		OptionType:          p.OptionType,
		Description:         p.Description,
		TradeDate:           p.TradeDate,
		SettlementDate:      p.SettlementDate,
		Fee:                 p.Fee,
		FxRate:              p.FxRate,
		Institution:         p.Institution,
		ExternalReferenceID: p.ExternalReferenceID,
		ProviderType:        p.ProviderType,
		SourceSystem:        p.SourceSystem,
		SourceRecordID:      p.SourceRecordID,
		SourceGroupID:       p.SourceGroupID,
		SourceFingerprint:   p.SourceFingerprint,
		NeedsReview:         p.NeedsReview,
	}
	if p.SymbolTicker != "" || p.SymbolRaw != "" {
		a.Symbol = &brokerage.Symbol{
			Symbol:      p.SymbolTicker,
			RawSymbol:   p.SymbolRaw,
			Description: p.SymbolDescription,
			Name:        p.SymbolName,
			Type: brokerage.SymbolType{
				Code:        p.SymbolTypeCode,
				Description: p.SymbolTypeDesc,
				IsSupported: p.SymbolTypeSupported,
			},
			Exchange: brokerage.Exchange{
				Code:    p.SymbolExchangeCode,
				MICCode: p.SymbolExchangeMIC,
				Name:    p.SymbolExchangeName,
				Suffix:  p.SymbolExchangeSuffix,
			},
			Currency: brokerage.Currency{Code: p.SymbolCurrencyCode, Name: p.SymbolCurrencyName},
			FIGICode: p.SymbolFIGI,
		}
	}
	if p.CurrencySymbolTicker != "" || p.CurrencySymbolRaw != "" {
		a.CurrencySymbol = &brokerage.Symbol{
			Symbol: p.CurrencySymbolTicker, RawSymbol: p.CurrencySymbolRaw,
			Description: p.CurrencySymbolDescription, Name: p.CurrencySymbolName,
			Type:     brokerage.SymbolType{Code: p.CurrencySymbolTypeCode, Description: p.CurrencySymbolTypeDesc, IsSupported: p.CurrencySymbolSupported},
			Exchange: brokerage.Exchange{Code: p.CurrencySymbolExchangeCode, MICCode: p.CurrencySymbolExchangeMIC, Name: p.CurrencySymbolExchangeName, Suffix: p.CurrencySymbolExchangeSfx},
			Currency: brokerage.Currency{Code: p.CurrencySymbolCurrencyCode, Name: p.CurrencySymbolCurrencyName},
			FIGICode: p.CurrencySymbolFIGI,
		}
	}
	if p.OptionTicker != "" {
		expiration := time.Time{}
		if p.OptionExpirationDate != nil {
			expiration = *p.OptionExpirationDate
		}
		a.OptionSymbol = &brokerage.OptionSymbol{
			Ticker: p.OptionTicker, OptionType: brokerage.OptionSide(p.OptionSide),
			StrikePrice: p.OptionStrikePrice, ExpirationDate: expiration, IsMiniOption: p.OptionIsMini,
			Underlying: brokerage.Symbol{
				Symbol: p.OptionUnderlying, RawSymbol: p.OptionUnderlyingRaw,
				Description: p.OptionUnderlyingDesc,
				Type:        brokerage.SymbolType{Code: p.OptionUnderlyingType, Description: p.OptionUnderlyingTypeDesc, IsSupported: p.OptionUnderlyingSupported},
				Exchange:    brokerage.Exchange{Code: p.OptionUnderlyingExch, MICCode: p.OptionUnderlyingMIC, Name: p.OptionUnderlyingExchName, Suffix: p.OptionUnderlyingExchSuffix},
				Currency:    brokerage.Currency{Code: p.OptionUnderlyingCurr, Name: p.OptionUnderlyingCurrName}, FIGICode: p.OptionUnderlyingFIGI,
			},
		}
	}
	return a
}

// activityFromDomain converts a domain Activity into a PO ready for upsert.
// accountID is supplied by the caller because UpsertBatch is account-scoped.
func activityFromDomain(accountID string, a brokerage.Activity) ActivityPO {
	var s brokerage.Symbol
	if a.Symbol != nil {
		s = *a.Symbol
	}
	var currencySymbol brokerage.Symbol
	if a.CurrencySymbol != nil {
		currencySymbol = *a.CurrencySymbol
	}
	var option brokerage.OptionSymbol
	var optionExpiration *time.Time
	if a.OptionSymbol != nil {
		option = *a.OptionSymbol
		if !option.ExpirationDate.IsZero() {
			exp := option.ExpirationDate
			optionExpiration = &exp
		}
	}
	if a.ProviderType == "" {
		a.ProviderType = "CUSTOM"
	}
	if a.SourceSystem == "" {
		a.SourceSystem = "CUSTOM"
	}
	return ActivityPO{
		ID:                         a.ID,
		AccountID:                  accountID,
		SourceRecordID:             a.SourceRecordID,
		SymbolTicker:               s.Symbol,
		SymbolRaw:                  s.RawSymbol,
		SymbolDescription:          s.Description,
		SymbolName:                 s.Name,
		SymbolTypeCode:             s.Type.Code,
		SymbolTypeDesc:             s.Type.Description,
		SymbolTypeSupported:        s.Type.IsSupported,
		SymbolExchangeCode:         s.Exchange.Code,
		SymbolExchangeMIC:          s.Exchange.MICCode,
		SymbolExchangeName:         s.Exchange.Name,
		SymbolExchangeSuffix:       s.Exchange.Suffix,
		SymbolCurrencyCode:         s.Currency.Code,
		SymbolCurrencyName:         s.Currency.Name,
		SymbolFIGI:                 s.FIGICode,
		CurrencySymbolTicker:       currencySymbol.Symbol,
		CurrencySymbolRaw:          currencySymbol.RawSymbol,
		CurrencySymbolDescription:  currencySymbol.Description,
		CurrencySymbolName:         currencySymbol.Name,
		CurrencySymbolTypeCode:     currencySymbol.Type.Code,
		CurrencySymbolTypeDesc:     currencySymbol.Type.Description,
		CurrencySymbolSupported:    currencySymbol.Type.IsSupported,
		CurrencySymbolExchangeCode: currencySymbol.Exchange.Code,
		CurrencySymbolExchangeMIC:  currencySymbol.Exchange.MICCode,
		CurrencySymbolExchangeName: currencySymbol.Exchange.Name,
		CurrencySymbolExchangeSfx:  currencySymbol.Exchange.Suffix,
		CurrencySymbolCurrencyCode: currencySymbol.Currency.Code,
		CurrencySymbolCurrencyName: currencySymbol.Currency.Name,
		CurrencySymbolFIGI:         currencySymbol.FIGICode,
		OptionTicker:               option.Ticker,
		OptionSide:                 string(option.OptionType),
		OptionStrikePrice:          option.StrikePrice,
		OptionExpirationDate:       optionExpiration,
		OptionIsMini:               option.IsMiniOption,
		OptionUnderlying:           option.Underlying.Symbol,
		OptionUnderlyingRaw:        option.Underlying.RawSymbol,
		OptionUnderlyingDesc:       option.Underlying.Description,
		OptionUnderlyingType:       option.Underlying.Type.Code,
		OptionUnderlyingTypeDesc:   option.Underlying.Type.Description,
		OptionUnderlyingSupported:  option.Underlying.Type.IsSupported,
		OptionUnderlyingExch:       option.Underlying.Exchange.Code,
		OptionUnderlyingMIC:        option.Underlying.Exchange.MICCode,
		OptionUnderlyingExchName:   option.Underlying.Exchange.Name,
		OptionUnderlyingExchSuffix: option.Underlying.Exchange.Suffix,
		OptionUnderlyingCurr:       option.Underlying.Currency.Code,
		OptionUnderlyingCurrName:   option.Underlying.Currency.Name,
		OptionUnderlyingFIGI:       option.Underlying.FIGICode,
		Price:                      a.Price,
		Units:                      a.Units,
		Amount:                     a.Amount,
		CurrencyCode:               a.Currency.Code,
		CurrencyName:               a.Currency.Name,
		Type:                       string(a.Type),
		Subtype:                    a.Subtype,
		RawType:                    a.RawType,
		OptionType:                 a.OptionType,
		Description:                a.Description,
		TradeDate:                  a.TradeDate,
		SettlementDate:             a.SettlementDate,
		Fee:                        a.Fee,
		FxRate:                     a.FxRate,
		Institution:                a.Institution,
		ExternalReferenceID:        a.ExternalReferenceID,
		ProviderType:               a.ProviderType,
		SourceSystem:               a.SourceSystem,
		SourceGroupID:              a.SourceGroupID,
		SourceFingerprint:          a.SourceFingerprint,
		NeedsReview:                a.NeedsReview,
	}
}
