// Token persistence-object: GORM mapping for the tokens audit table.

package persistence

import (
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
)

// TokenPO is the GORM mapping for the tokens audit table.
type TokenPO struct {
	TokenID   string    `gorm:"column:token_id;primaryKey;type:text"`
	Subject   string    `gorm:"column:subject;type:text;not null;index:tokens_subject_idx,priority:1"`
	IssuedAt  time.Time `gorm:"column:issued_at;not null;index:tokens_subject_idx,priority:2,sort:desc"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null"`
}

// TableName pins the GORM-derived table name.
func (TokenPO) TableName() string { return "tokens" }

// tokenFromDomain converts a TokenMetadata into its PO counterpart.
func tokenFromDomain(t repository.TokenMetadata) TokenPO {
	return TokenPO{
		TokenID:   t.TokenID,
		Subject:   t.Subject,
		IssuedAt:  t.IssuedAt,
		ExpiresAt: t.ExpiresAt,
	}
}
