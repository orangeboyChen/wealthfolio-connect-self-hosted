// Package brokerage contains application services orchestrating broker
// connection and account read flows. Each service is a thin policy layer
// in front of the repository ports defined in domain/repository.
package brokerage

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/fx"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
)

// Module exposes the application brokerage services via fx.
var Module = fx.Module("application.brokerage",
	fx.Provide(
		NewConnectionService,
		NewAccountService,
		NewActivityService,
		NewHoldingService,
	),
)

// ConnectionService lists broker connections for the authenticated user.
type ConnectionService struct {
	repo repository.ConnectionRepository
}

// NewConnectionService wires the ConnectionService.
func NewConnectionService(r repository.ConnectionRepository) *ConnectionService {
	return &ConnectionService{repo: r}
}

// List returns every persisted connection.
func (s *ConnectionService) List(ctx context.Context) ([]brokerage.Connection, error) {
	conns, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("connections: list: %w", err)
	}
	if conns == nil {
		conns = []brokerage.Connection{}
	}
	return conns, nil
}

// AccountService lists and looks up broker accounts.
type AccountService struct {
	repo repository.AccountRepository
}

// NewAccountService wires the AccountService.
func NewAccountService(r repository.AccountRepository) *AccountService {
	return &AccountService{repo: r}
}

// List returns every persisted account.
func (s *AccountService) List(ctx context.Context) ([]brokerage.Account, error) {
	accs, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("accounts: list: %w", err)
	}
	if accs == nil {
		accs = []brokerage.Account{}
	}
	return accs, nil
}

// ErrAccountNotFound is returned when AccountService.Get cannot find the
// requested account.
var ErrAccountNotFound = errors.New("account not found")

// Get returns the account identified by id or ErrAccountNotFound.
func (s *AccountService) Get(ctx context.Context, id string) (brokerage.Account, error) {
	acc, err := s.repo.Get(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return brokerage.Account{}, ErrAccountNotFound
	}
	if err != nil {
		return brokerage.Account{}, fmt.Errorf("accounts: get: %w", err)
	}
	return acc, nil
}

// SetSyncEnabled toggles the sync_enabled flag for one account. Returns
// ErrAccountNotFound when the account does not exist.
func (s *AccountService) SetSyncEnabled(ctx context.Context, id string, enabled bool) (brokerage.Account, error) {
	if err := s.repo.SetSyncEnabled(ctx, id, enabled); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return brokerage.Account{}, ErrAccountNotFound
		}
		return brokerage.Account{}, fmt.Errorf("accounts: set sync_enabled: %w", err)
	}
	return s.Get(ctx, id)
}
