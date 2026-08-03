// Package persistence — GORM persistence-objects (POs).
//
// These structs are the only place in the codebase that depends on GORM
// tags. They are deliberately separated from the domain entities in
// domain/brokerage so the domain stays infrastructure-free, in line with the
// project DDD constraints declared in AGENTS.md.
//
// Each PO lives in its own file (one aggregate ↔ one model file):
//
//	connection_model.go  → ConnectionPO
//	account_model.go     → AccountPO
//	activity_model.go    → ActivityPO
//	holdings_model.go    → HoldingsSnapshotPO
//	token_model.go       → TokenPO
//
// This file only owns the Migrator registry and JSON helpers shared across
// PO files.
package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Migrator lists every GORM model that AutoMigrate must converge. The
// database.RunMigrations fx hook calls Models() on the Migrator value
// provided by the persistence module.
type Migrator struct{}

// Models returns the slice of model pointers passed to gorm.AutoMigrate.
// Append new aggregates here when adding a new *_model.go file.
func (Migrator) Models() []any {
	return []any{
		&ConnectionPO{},
		&AccountPO{},
		&ActivityPO{},
		&HoldingsSnapshotPO{},
		&TokenPO{},
	}
}

// MigrateData applies idempotent repairs to rows written by older mapper
// versions after AutoMigrate has ensured that every referenced column exists.
func (Migrator) MigrateData(ctx context.Context, db *gorm.DB) error {
	activities := func() *gorm.DB {
		return db.WithContext(ctx).Model(&ActivityPO{}).Where("source_system = ?", "ibkr-flex")
	}
	legacyFXAccounts := activities().
		Select("DISTINCT account_id").
		Where("type = ? AND subtype = ? AND fee > 0", "TRANSFER_IN", "FXEXCHANGE")
	if err := db.WithContext(ctx).Model(&AccountPO{}).
		Where("id IN (?)", legacyFXAccounts).
		Updates(map[string]any{
			"initial_tx_sync_done": false,
			"last_tx_sync":         nil,
			"first_tx_date":        nil,
			"activity_sync_offset": 0,
		}).Error; err != nil {
		return fmt.Errorf("reset legacy IBKR Flex activity import: %w", err)
	}
	if err := activities().Where("type IN ?", []string{"BUY", "SELL", "OPTION_BUY", "OPTION_SELL"}).
		Update("fx_rate", nil).Error; err != nil {
		return fmt.Errorf("repair IBKR Flex native-currency trades: %w", err)
	}
	if err := activities().Where("type = ? AND amount > 0", "FEE").
		Updates(map[string]any{"type": "CREDIT", "subtype": "FEE_REFUND"}).Error; err != nil {
		return fmt.Errorf("repair IBKR Flex fee refunds: %w", err)
	}

	// Older Flex payloads exposed IBKR venue names in the exchange-code field
	// without an ISO 10383 MIC. Wealthfolio treats that code as a MIC fallback,
	// which prevents reliable market-history resolution (for example MCD/NYSE).
	venueRepairs := []struct{ code, mic string }{
		{code: "BVB", mic: "XBSE"},
		{code: "IBIS", mic: "XETR"},
		{code: "IBIS2", mic: "XETR"},
		{code: "NASDAQ", mic: "XNAS"},
		{code: "NYSE", mic: "XNYS"},
		{code: "XETRA", mic: "XETR"},
	}
	for _, repair := range venueRepairs {
		if err := activities().Where("symbol_exchange_code = ?", repair.code).
			Update("symbol_exchange_mic", repair.mic).Error; err != nil {
			return fmt.Errorf("repair IBKR Flex exchange %s: %w", repair.code, err)
		}
	}
	return nil
}

// nonNilJSON returns a `[]` literal when b is empty so json.Unmarshal never
// trips on a NULL JSONB column.
func nonNilJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

// orEmpty makes sure marshaled slices serialize as `[]` instead of `null`.
func orEmpty[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}
