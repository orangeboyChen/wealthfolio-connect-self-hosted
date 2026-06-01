package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
)

// ─── Activities ───────────────────────────────────────────────────────────

type activityRepo struct{ db *gorm.DB }

// NewActivityRepository wires an ActivityRepository backed by GORM.
func NewActivityRepository(db *gorm.DB) repository.ActivityRepository {
	return &activityRepo{db: db}
}

// List returns activities matching the filter, paginated, plus the total count.
func (r *activityRepo) List(ctx context.Context, f repository.ActivityFilter) ([]brokerage.Activity, int, error) {
	if f.Limit <= 0 {
		f.Limit = 1000
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	q := r.db.WithContext(ctx).Model(&ActivityPO{}).Where("account_id = ?", f.AccountID)
	if f.StartDate != nil {
		q = q.Where("trade_date >= ?", *f.StartDate)
	}
	if f.EndDate != nil {
		q = q.Where("trade_date <= ?", *f.EndDate)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("activities count: %w", err)
	}

	var pos []ActivityPO
	err := q.Order("trade_date DESC").Order("id").
		Limit(f.Limit).Offset(f.Offset).
		Find(&pos).Error
	if err != nil {
		return nil, 0, fmt.Errorf("activities list: %w", err)
	}
	out := make([]brokerage.Activity, 0, len(pos))
	for _, p := range pos {
		out = append(out, p.ToDomain())
	}
	return out, int(total), nil
}

// UpsertBatch deduplicates by (account_id, source_record_id). The conflict
// target maps to the activities_account_source_uk unique index defined on
// ActivityPO.
func (r *activityRepo) UpsertBatch(ctx context.Context, accountID string, items []brokerage.Activity) error {
	if len(items) == 0 {
		return nil
	}
	pos := make([]ActivityPO, 0, len(items))
	for _, it := range items {
		pos = append(pos, activityFromDomain(accountID, it))
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "account_id"}, {Name: "source_record_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"price", "units", "amount", "type",
			"trade_date", "settlement_date", "fee", "description",
		}),
	}).Create(&pos).Error
	if err != nil {
		return fmt.Errorf("activity upsert: %w", err)
	}
	return nil
}

// ─── Holdings ─────────────────────────────────────────────────────────────

type holdingRepo struct{ db *gorm.DB }

// NewHoldingRepository wires a HoldingRepository backed by GORM.
func NewHoldingRepository(db *gorm.DB) repository.HoldingRepository {
	return &holdingRepo{db: db}
}

// GetLatest returns the most recent holdings snapshot for the given account.
func (r *holdingRepo) GetLatest(ctx context.Context, accountID string) (brokerage.Holdings, error) {
	var po HoldingsSnapshotPO
	err := r.db.WithContext(ctx).First(&po, "account_id = ?", accountID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return brokerage.Holdings{}, repository.ErrNotFound
	}
	if err != nil {
		return brokerage.Holdings{}, fmt.Errorf("holdings get: %w", err)
	}
	return po.ToDomain()
}

// Replace overwrites the snapshot for the account in the given Holdings.
func (r *holdingRepo) Replace(ctx context.Context, h brokerage.Holdings) error {
	if h.CapturedAt.IsZero() {
		h.CapturedAt = time.Now().UTC()
	}
	po, err := holdingsFromDomain(h)
	if err != nil {
		return err
	}
	err = r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "account_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"captured_at", "balances", "positions", "options",
		}),
	}).Create(&po).Error
	if err != nil {
		return fmt.Errorf("holdings upsert: %w", err)
	}
	return nil
}

// ─── Tokens ───────────────────────────────────────────────────────────────

type tokenRepo struct{ db *gorm.DB }

// NewTokenRepository wires a TokenRepository backed by GORM.
func NewTokenRepository(db *gorm.DB) repository.TokenRepository {
	return &tokenRepo{db: db}
}

// Insert appends a token-issuance audit row.
func (r *tokenRepo) Insert(ctx context.Context, t repository.TokenMetadata) error {
	po := tokenFromDomain(t)
	// ON CONFLICT (token_id) DO NOTHING — keep audit rows immutable.
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&po).Error
	if err != nil {
		return fmt.Errorf("token insert: %w", err)
	}
	return nil
}
