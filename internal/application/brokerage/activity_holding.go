// Package brokerage contains application services orchestrating broker
// connection, account, activity and holding read flows.
package brokerage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
)

// ActivityQuery is the user-facing query for the activities endpoint.
type ActivityQuery struct {
	AccountID string
	StartDate *time.Time
	EndDate   *time.Time
	Offset    int
	Limit     int
}

// PaginatedActivities is the result of a successful query.
type PaginatedActivities struct {
	Items   []brokerage.Activity
	Total   int
	Offset  int
	Limit   int
	HasMore bool
}

// ActivityService orchestrates paginated activity lookups.
type ActivityService struct {
	repo repository.ActivityRepository
	acc  repository.AccountRepository
}

// NewActivityService wires ActivityService.
func NewActivityService(repo repository.ActivityRepository, acc repository.AccountRepository) *ActivityService {
	return &ActivityService{repo: repo, acc: acc}
}

// DefaultActivityLimit is applied when no explicit limit is supplied.
const DefaultActivityLimit = 1000

// MaxActivityLimit caps the per-request size to prevent runaway loads.
const MaxActivityLimit = 5000

// List returns activities matching the query, ensuring the account exists.
func (s *ActivityService) List(ctx context.Context, q ActivityQuery) (PaginatedActivities, error) {
	if _, err := s.acc.Get(ctx, q.AccountID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return PaginatedActivities{}, ErrAccountNotFound
		}
		return PaginatedActivities{}, fmt.Errorf("activities: account lookup: %w", err)
	}
	if q.Limit <= 0 {
		q.Limit = DefaultActivityLimit
	}
	if q.Limit > MaxActivityLimit {
		q.Limit = MaxActivityLimit
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	items, total, err := s.repo.List(ctx, repository.ActivityFilter{
		AccountID: q.AccountID,
		StartDate: q.StartDate,
		EndDate:   q.EndDate,
		Offset:    q.Offset,
		Limit:     q.Limit,
	})
	if err != nil {
		return PaginatedActivities{}, fmt.Errorf("activities: list: %w", err)
	}
	if items == nil {
		items = []brokerage.Activity{}
	}
	return PaginatedActivities{
		Items:   items,
		Total:   total,
		Offset:  q.Offset,
		Limit:   q.Limit,
		HasMore: q.Offset+len(items) < total,
	}, nil
}

// HoldingService returns current snapshots for an account.
type HoldingService struct {
	repo repository.HoldingRepository
	acc  repository.AccountRepository
}

// NewHoldingService wires HoldingService.
func NewHoldingService(repo repository.HoldingRepository, acc repository.AccountRepository) *HoldingService {
	return &HoldingService{repo: repo, acc: acc}
}

// HoldingsResult bundles the latest snapshot with the owning account.
type HoldingsResult struct {
	Account  brokerage.Account
	Holdings brokerage.Holdings
}

// Get fetches the latest snapshot for the supplied account id.
func (s *HoldingService) Get(ctx context.Context, accountID string) (HoldingsResult, error) {
	acc, err := s.acc.Get(ctx, accountID)
	if errors.Is(err, repository.ErrNotFound) {
		return HoldingsResult{}, ErrAccountNotFound
	}
	if err != nil {
		return HoldingsResult{}, fmt.Errorf("holdings: account lookup: %w", err)
	}
	h, err := s.repo.GetLatest(ctx, accountID)
	if errors.Is(err, repository.ErrNotFound) {
		// Empty snapshot but valid account: return an empty Holdings.
		h = brokerage.Holdings{AccountID: accountID, CapturedAt: time.Now().UTC()}
	} else if err != nil {
		return HoldingsResult{}, fmt.Errorf("holdings: get: %w", err)
	}
	return HoldingsResult{Account: acc, Holdings: h}, nil
}
