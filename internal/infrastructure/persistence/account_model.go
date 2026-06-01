// Account persistence-object: GORM mapping for the accounts table.

package persistence

import (
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
)

// AccountPO is the GORM mapping for the accounts table.
type AccountPO struct {
	ID                     string     `gorm:"column:id;primaryKey;type:text"`
	Name                   string     `gorm:"column:name;type:text;not null"`
	AccountNumber          string     `gorm:"column:account_number;type:text;not null;default:''"`
	Type                   string     `gorm:"column:type;type:text;not null"`
	RawType                string     `gorm:"column:raw_type;type:text;not null;default:''"`
	Currency               string     `gorm:"column:currency;type:text;not null"`
	BalanceTotal           float64    `gorm:"column:balance_total;not null;default:0"`
	BalanceCurrency        string     `gorm:"column:balance_currency;type:text;not null;default:''"`
	BrokerageAuthorization string     `gorm:"column:brokerage_authorization;type:text;not null;index:accounts_authorization_idx"`
	InstitutionName        string     `gorm:"column:institution_name;type:text;not null;default:''"`
	SyncEnabled            bool       `gorm:"column:sync_enabled;not null;default:true"`
	SharedWithHousehold    bool       `gorm:"column:shared_with_household;not null;default:false"`
	IsPaper                bool       `gorm:"column:is_paper;not null;default:false"`
	Status                 string     `gorm:"column:status;type:text;not null;default:'open'"`
	CreatedDate            time.Time  `gorm:"column:created_date;not null"`
	LastTxSync             *time.Time `gorm:"column:last_tx_sync"`
	LastHoldingsSync       *time.Time `gorm:"column:last_holdings_sync"`
	FirstTxDate            *time.Time `gorm:"column:first_tx_date"`
	InitialTxSyncDone      bool       `gorm:"column:initial_tx_sync_done;not null;default:false"`
	InitialHoldingsDone    bool       `gorm:"column:initial_holdings_done;not null;default:false"`
	OwnerUserID            string     `gorm:"column:owner_user_id;type:text;not null;default:''"`
	OwnerFullName          string     `gorm:"column:owner_full_name;type:text;not null;default:''"`
	OwnerEmail             string     `gorm:"column:owner_email;type:text;not null;default:''"`
}

// TableName pins the GORM-derived table name.
func (AccountPO) TableName() string { return "accounts" }

// ToDomain converts a PO into its domain entity counterpart.
func (p AccountPO) ToDomain() brokerage.Account {
	return brokerage.Account{
		ID:                     p.ID,
		Name:                   p.Name,
		AccountNumber:          p.AccountNumber,
		Type:                   brokerage.AccountType(p.Type),
		RawType:                p.RawType,
		Currency:               p.Currency,
		BalanceTotal:           p.BalanceTotal,
		BalanceCurrency:        p.BalanceCurrency,
		BrokerageAuthorization: p.BrokerageAuthorization,
		InstitutionName:        p.InstitutionName,
		SyncEnabled:            p.SyncEnabled,
		SharedWithHousehold:    p.SharedWithHousehold,
		IsPaper:                p.IsPaper,
		Status:                 p.Status,
		CreatedDate:            p.CreatedDate,
		LastTxSync:             p.LastTxSync,
		LastHoldingsSync:       p.LastHoldingsSync,
		FirstTxDate:            p.FirstTxDate,
		InitialTxSyncDone:      p.InitialTxSyncDone,
		InitialHoldingsDone:    p.InitialHoldingsDone,
		OwnerUserID:            p.OwnerUserID,
		OwnerFullName:          p.OwnerFullName,
		OwnerEmail:             p.OwnerEmail,
	}
}

// accountFromDomain converts a domain entity into a PO ready for upsert.
func accountFromDomain(a brokerage.Account) AccountPO {
	if a.CreatedDate.IsZero() {
		a.CreatedDate = time.Now().UTC()
	}
	if a.Status == "" {
		a.Status = "open"
	}
	return AccountPO{
		ID:                     a.ID,
		Name:                   a.Name,
		AccountNumber:          a.AccountNumber,
		Type:                   string(a.Type),
		RawType:                a.RawType,
		Currency:               a.Currency,
		BalanceTotal:           a.BalanceTotal,
		BalanceCurrency:        a.BalanceCurrency,
		BrokerageAuthorization: a.BrokerageAuthorization,
		InstitutionName:        a.InstitutionName,
		SyncEnabled:            a.SyncEnabled,
		SharedWithHousehold:    a.SharedWithHousehold,
		IsPaper:                a.IsPaper,
		Status:                 a.Status,
		CreatedDate:            a.CreatedDate,
		LastTxSync:             a.LastTxSync,
		LastHoldingsSync:       a.LastHoldingsSync,
		FirstTxDate:            a.FirstTxDate,
		InitialTxSyncDone:      a.InitialTxSyncDone,
		InitialHoldingsDone:    a.InitialHoldingsDone,
		OwnerUserID:            a.OwnerUserID,
		OwnerFullName:          a.OwnerFullName,
		OwnerEmail:             a.OwnerEmail,
	}
}
