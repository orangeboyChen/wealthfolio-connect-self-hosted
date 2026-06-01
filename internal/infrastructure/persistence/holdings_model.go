// Holdings persistence-object: GORM mapping for the holdings_snapshot table.

package persistence

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
)

// HoldingsSnapshotPO is the GORM mapping for the holdings_snapshot table.
// Balances, positions and option positions are stored as JSONB blobs to
// preserve schema parity with the previous SQL-driven version.
type HoldingsSnapshotPO struct {
	AccountID  string    `gorm:"column:account_id;primaryKey;type:text"`
	CapturedAt time.Time `gorm:"column:captured_at;not null"`
	Balances   []byte    `gorm:"column:balances;type:jsonb;not null;default:'[]'"`
	Positions  []byte    `gorm:"column:positions;type:jsonb;not null;default:'[]'"`
	Options    []byte    `gorm:"column:options;type:jsonb;not null;default:'[]'"`
}

// TableName pins the GORM-derived table name.
func (HoldingsSnapshotPO) TableName() string { return "holdings_snapshot" }

// ToDomain decodes the JSONB columns into the domain Holdings entity.
func (p HoldingsSnapshotPO) ToDomain() (brokerage.Holdings, error) {
	h := brokerage.Holdings{AccountID: p.AccountID, CapturedAt: p.CapturedAt}
	if err := json.Unmarshal(nonNilJSON(p.Balances), &h.Balances); err != nil {
		return brokerage.Holdings{}, fmt.Errorf("holdings balances decode: %w", err)
	}
	if err := json.Unmarshal(nonNilJSON(p.Positions), &h.Positions); err != nil {
		return brokerage.Holdings{}, fmt.Errorf("holdings positions decode: %w", err)
	}
	if err := json.Unmarshal(nonNilJSON(p.Options), &h.OptionPositions); err != nil {
		return brokerage.Holdings{}, fmt.Errorf("holdings options decode: %w", err)
	}
	return h, nil
}

// holdingsFromDomain encodes a domain Holdings entity into the PO. Empty
// slices are emitted as `[]` JSON literals so the column is never NULL.
func holdingsFromDomain(h brokerage.Holdings) (HoldingsSnapshotPO, error) {
	if h.CapturedAt.IsZero() {
		h.CapturedAt = time.Now().UTC()
	}
	balances, err := json.Marshal(orEmpty(h.Balances))
	if err != nil {
		return HoldingsSnapshotPO{}, fmt.Errorf("holdings balances encode: %w", err)
	}
	positions, err := json.Marshal(orEmpty(h.Positions))
	if err != nil {
		return HoldingsSnapshotPO{}, fmt.Errorf("holdings positions encode: %w", err)
	}
	options, err := json.Marshal(orEmpty(h.OptionPositions))
	if err != nil {
		return HoldingsSnapshotPO{}, fmt.Errorf("holdings options encode: %w", err)
	}
	return HoldingsSnapshotPO{
		AccountID:  h.AccountID,
		CapturedAt: h.CapturedAt,
		Balances:   balances,
		Positions:  positions,
		Options:    options,
	}, nil
}
