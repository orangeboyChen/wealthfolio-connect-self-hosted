// Package persistence contains the PostgreSQL implementations of the
// repository ports declared in domain/repository, backed by GORM.
package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/fx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/database"
)

// Module exposes every repository implementation as a domain interface, plus
// the Migrator that drives database.RunMigrations.
var Module = fx.Module("persistence",
	fx.Provide(
		fx.Annotate(NewMigrator, fx.As(new(database.Migrator))),
		fx.Annotate(NewConnectionRepository, fx.As(new(repository.ConnectionRepository))),
		fx.Annotate(NewAccountRepository, fx.As(new(repository.AccountRepository))),
		fx.Annotate(NewActivityRepository, fx.As(new(repository.ActivityRepository))),
		fx.Annotate(NewHoldingRepository, fx.As(new(repository.HoldingRepository))),
		fx.Annotate(NewTokenRepository, fx.As(new(repository.TokenRepository))),
	),
)

// NewMigrator returns the Migrator pulled in by RunMigrations.
func NewMigrator() Migrator { return Migrator{} }

// ─── Connections ──────────────────────────────────────────────────────────

type connectionRepo struct{ db *gorm.DB }

// NewConnectionRepository wires a ConnectionRepository backed by GORM.
func NewConnectionRepository(db *gorm.DB) repository.ConnectionRepository {
	return &connectionRepo{db: db}
}

// List returns every connection ordered by brokerage_slug.
func (r *connectionRepo) List(ctx context.Context) ([]brokerage.Connection, error) {
	var pos []ConnectionPO
	if err := r.db.WithContext(ctx).Order("brokerage_slug").Find(&pos).Error; err != nil {
		return nil, fmt.Errorf("connections list: %w", err)
	}
	out := make([]brokerage.Connection, 0, len(pos))
	for _, p := range pos {
		out = append(out, p.ToDomain())
	}
	return out, nil
}

// Upsert inserts or updates the supplied connection.
func (r *connectionRepo) Upsert(ctx context.Context, c brokerage.Connection) error {
	po := connectionFromDomain(c)
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"authorization_id", "brokerage_name", "brokerage_slug",
			"display_name", "logo_url", "square_logo_url",
			"disabled", "name", "status", "updated_at",
		}),
	}).Create(&po).Error
	if err != nil {
		return fmt.Errorf("connection upsert: %w", err)
	}
	return nil
}

// ─── Accounts ─────────────────────────────────────────────────────────────

type accountRepo struct{ db *gorm.DB }

// NewAccountRepository wires an AccountRepository backed by GORM.
func NewAccountRepository(db *gorm.DB) repository.AccountRepository {
	return &accountRepo{db: db}
}

// List returns every account ordered by id.
func (r *accountRepo) List(ctx context.Context) ([]brokerage.Account, error) {
	var pos []AccountPO
	if err := r.db.WithContext(ctx).Order("id").Find(&pos).Error; err != nil {
		return nil, fmt.Errorf("accounts list: %w", err)
	}
	out := make([]brokerage.Account, 0, len(pos))
	for _, p := range pos {
		out = append(out, p.ToDomain())
	}
	return out, nil
}

// Get returns one account by id, or repository.ErrNotFound when missing.
func (r *accountRepo) Get(ctx context.Context, id string) (brokerage.Account, error) {
	var po AccountPO
	err := r.db.WithContext(ctx).First(&po, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return brokerage.Account{}, repository.ErrNotFound
	}
	if err != nil {
		return brokerage.Account{}, fmt.Errorf("accounts get: %w", err)
	}
	return po.ToDomain(), nil
}

// Upsert inserts or updates the supplied account.
func (r *accountRepo) Upsert(ctx context.Context, a brokerage.Account) error {
	po := accountFromDomain(a)
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "account_number", "type", "raw_type", "currency",
			"balance_total", "balance_currency", "brokerage_authorization",
			"institution_name", "sync_enabled", "shared_with_household",
			"is_paper", "status", "owner_user_id", "owner_full_name", "owner_email",
		}),
	}).Create(&po).Error
	if err != nil {
		return fmt.Errorf("account upsert: %w", err)
	}
	return nil
}

// UpdateSyncStatus mirrors the previous SQL semantics: a non-nil timestamp
// argument bumps the corresponding column AND flips the "initial done" flag.
// Nil arguments leave the existing values untouched.
func (r *accountRepo) UpdateSyncStatus(ctx context.Context, accountID string, txSync, holdingsSync *time.Time) error {
	updates := map[string]any{}
	if txSync != nil {
		updates["last_tx_sync"] = *txSync
		updates["initial_tx_sync_done"] = true
	}
	if holdingsSync != nil {
		updates["last_holdings_sync"] = *holdingsSync
		updates["initial_holdings_done"] = true
	}
	if len(updates) == 0 {
		return nil
	}
	err := r.db.WithContext(ctx).
		Model(&AccountPO{}).
		Where("id = ?", accountID).
		Updates(updates).Error
	if err != nil {
		return fmt.Errorf("account sync status: %w", err)
	}
	return nil
}

// SetSyncEnabled toggles the sync_enabled column.
func (r *accountRepo) SetSyncEnabled(ctx context.Context, accountID string, enabled bool) error {
	res := r.db.WithContext(ctx).
		Model(&AccountPO{}).
		Where("id = ?", accountID).
		Update("sync_enabled", enabled)
	if res.Error != nil {
		return fmt.Errorf("account set sync_enabled: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return repository.ErrNotFound
	}
	return nil
}
