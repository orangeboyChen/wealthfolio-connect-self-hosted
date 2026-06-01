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
