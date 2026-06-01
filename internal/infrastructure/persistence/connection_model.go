// Connection persistence-object: GORM mapping for the connections table.

package persistence

import (
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
)

// ConnectionPO is the GORM mapping for the connections table.
type ConnectionPO struct {
	ID              string    `gorm:"column:id;primaryKey;type:text"`
	AuthorizationID string    `gorm:"column:authorization_id;type:text;not null"`
	BrokerageName   string    `gorm:"column:brokerage_name;type:text;not null"`
	BrokerageSlug   string    `gorm:"column:brokerage_slug;type:text;not null;index:connections_slug_idx"`
	DisplayName     string    `gorm:"column:display_name;type:text;not null;default:''"`
	LogoURL         string    `gorm:"column:logo_url;type:text;not null;default:''"`
	SquareLogoURL   string    `gorm:"column:square_logo_url;type:text;not null;default:''"`
	Disabled        bool      `gorm:"column:disabled;not null;default:false"`
	Name            string    `gorm:"column:name;type:text;not null;default:''"`
	Status          string    `gorm:"column:status;type:text;not null;default:'active'"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null;autoUpdateTime"`
}

// TableName pins the GORM-derived table name.
func (ConnectionPO) TableName() string { return "connections" }

// ToDomain converts a PO into its domain entity counterpart.
func (p ConnectionPO) ToDomain() brokerage.Connection {
	return brokerage.Connection{
		ID:              p.ID,
		AuthorizationID: p.AuthorizationID,
		BrokerageName:   p.BrokerageName,
		BrokerageSlug:   p.BrokerageSlug,
		DisplayName:     p.DisplayName,
		LogoURL:         p.LogoURL,
		SquareLogoURL:   p.SquareLogoURL,
		Disabled:        p.Disabled,
		Name:            p.Name,
		Status:          brokerage.ConnectionStatus(p.Status),
		UpdatedAt:       p.UpdatedAt,
	}
}

// connectionFromDomain converts a domain entity into a PO ready for upsert.
func connectionFromDomain(c brokerage.Connection) ConnectionPO {
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now().UTC()
	}
	if c.Status == "" {
		c.Status = brokerage.ConnectionActive
	}
	return ConnectionPO{
		ID:              c.ID,
		AuthorizationID: c.AuthorizationID,
		BrokerageName:   c.BrokerageName,
		BrokerageSlug:   c.BrokerageSlug,
		DisplayName:     c.DisplayName,
		LogoURL:         c.LogoURL,
		SquareLogoURL:   c.SquareLogoURL,
		Disabled:        c.Disabled,
		Name:            c.Name,
		Status:          string(c.Status),
		UpdatedAt:       c.UpdatedAt,
	}
}
