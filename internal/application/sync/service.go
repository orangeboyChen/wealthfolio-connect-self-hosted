// Package sync orchestrates periodic synchronization between every
// configured BrokerClient and the persistence layer. The Service is
// designed to be wired through fx.Lifecycle: OnStart kicks off a goroutine
// running the configured interval; OnStop signals it to stop.
package sync

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.uber.org/fx"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

// Module exposes the sync engine and its registry to fx.
var Module = fx.Module("application.sync",
	fx.Provide(NewService),
	fx.Invoke(StartSync),
)

// Params bundle every dependency the Service needs.
type Params struct {
	fx.In
	Logger      zerolog.Logger
	Config      *config.Config
	Connections repository.ConnectionRepository
	Accounts    repository.AccountRepository
	Activities  repository.ActivityRepository
	Holdings    repository.HoldingRepository
	Clients     []domainsync.BrokerClient `group:"broker_clients"`
}

// Service drives all configured upstream clients on a fixed schedule.
type Service struct {
	log         zerolog.Logger
	connections repository.ConnectionRepository
	accounts    repository.AccountRepository
	activities  repository.ActivityRepository
	holdings    repository.HoldingRepository
	clients     []domainsync.BrokerClient
	interval    time.Duration
	mu          sync.Mutex
	lastRun     time.Time
}

// NewService constructs a Service from fx-injected params. The cadence
// honors cfg.SyncInterval (SYNC_INTERVAL_MINUTES); zero or negative values
// fall back to a safe 4h default.
func NewService(p Params) *Service {
	interval := 4 * time.Hour
	if p.Config != nil && p.Config.SyncInterval > 0 {
		interval = p.Config.SyncInterval
	}
	return &Service{
		log:         p.Logger,
		connections: p.Connections,
		accounts:    p.Accounts,
		activities:  p.Activities,
		holdings:    p.Holdings,
		clients:     p.Clients,
		interval:    interval,
	}
}

// SetInterval overrides the default 4h cadence (only used by tests).
func (s *Service) SetInterval(d time.Duration) { s.interval = d }

// LastRun returns the timestamp of the most recent successful run.
func (s *Service) LastRun() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRun
}

// RunOnce executes every client sequentially, persisting partial results so
// a slow/broken upstream does not stall the others.
func (s *Service) RunOnce(ctx context.Context) error {
	for _, c := range s.clients {
		if err := s.syncOne(ctx, c); err != nil {
			s.log.Error().Err(err).Str("client", c.ID()).Msg("upstream sync failed")
		}
	}
	s.mu.Lock()
	s.lastRun = time.Now().UTC()
	s.mu.Unlock()
	return nil
}

func (s *Service) syncOne(ctx context.Context, c domainsync.BrokerClient) error {
	snap, err := c.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if err := s.connections.Upsert(ctx, snap.Connection); err != nil {
		return fmt.Errorf("upsert connection: %w", err)
	}
	for _, acc := range snap.Accounts {
		if err := s.accounts.Upsert(ctx, acc); err != nil {
			return fmt.Errorf("upsert account: %w", err)
		}
	}
	for _, h := range snap.Holdings {
		if err := s.holdings.Replace(ctx, h); err != nil {
			return fmt.Errorf("replace holdings: %w", err)
		}
	}
	for accID, items := range snap.Activities {
		if len(items) == 0 {
			continue
		}
		if err := s.activities.UpsertBatch(ctx, accID, items); err != nil {
			return fmt.Errorf("upsert activities: %w", err)
		}
	}
	return nil
}

// StartSync wires the lifecycle so the goroutine is spawned at OnStart and
// canceled at OnStop.
func StartSync(lc fx.Lifecycle, s *Service) {
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			go s.loop(ctx)
			return nil
		},
		OnStop: func(_ context.Context) error {
			cancel()
			return nil
		},
	})
}

func (s *Service) loop(ctx context.Context) {
	if err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
		s.log.Error().Err(err).Msg("initial sync run failed")
	}
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
				s.log.Error().Err(err).Msg("scheduled sync run failed")
			}
		}
	}
}

// AsBrokerClient is the fx group helper for registering a BrokerClient.
func AsBrokerClient(f any) any {
	return fx.Annotate(f,
		fx.As(new(domainsync.BrokerClient)),
		fx.ResultTags(`group:"broker_clients"`),
	)
}
