// Package sync orchestrates periodic synchronization between every
// configured BrokerClient and the persistence layer. The Service is
// designed to be wired through fx.Lifecycle: OnStart kicks off a goroutine
// running the configured interval; OnStop signals it to stop.
package sync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.uber.org/fx"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
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
	mu          sync.RWMutex
	lastRun     time.Time
	runningMu   sync.Mutex
	running     map[string]bool
	workers     sync.WaitGroup
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
		running:     make(map[string]bool),
	}
}

// SetInterval overrides the default 4h cadence (only used by tests).
func (s *Service) SetInterval(d time.Duration) { s.interval = d }

// LastRun returns the timestamp of the most recent successful run.
func (s *Service) LastRun() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastRun
}

// RunOnce starts each configured client independently and waits for this set
// of runs to finish. A slow client therefore cannot delay another client's
// start, and duplicate runs of the same client are suppressed.
func (s *Service) RunOnce(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, c := range s.clients {
		client := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runClient(ctx, client)
		}()
	}
	wg.Wait()
	s.mu.Lock()
	s.lastRun = time.Now().UTC()
	s.mu.Unlock()
	return nil
}

func (s *Service) runClient(ctx context.Context, c domainsync.BrokerClient) {
	if !s.beginClient(c.ID()) {
		s.log.Debug().Str("client", c.ID()).Msg("upstream sync already running; skipping overlapping run")
		return
	}
	defer s.endClient(c.ID())
	if err := s.syncOne(ctx, c); err != nil && ctx.Err() == nil {
		s.log.Error().Err(err).Str("client", c.ID()).Msg("upstream sync failed")
	}
	s.mu.Lock()
	s.lastRun = time.Now().UTC()
	s.mu.Unlock()
}

func (s *Service) beginClient(id string) bool {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	if s.running[id] {
		return false
	}
	s.running[id] = true
	return true
}

func (s *Service) endClient(id string) {
	s.runningMu.Lock()
	delete(s.running, id)
	s.runningMu.Unlock()
}

func (s *Service) syncOne(ctx context.Context, c domainsync.BrokerClient) error {
	var (
		snap     domainsync.BrokerSnapshot
		fetchErr error
	)
	paged, streamsActivities := c.(domainsync.PagedActivityClient)
	if streamsActivities {
		snap, fetchErr = paged.FetchAccountSnapshot(ctx)
	} else {
		snap, fetchErr = c.Fetch(ctx)
	}
	if fetchErr != nil && !hasSnapshotData(snap) {
		return fmt.Errorf("fetch: %w", fetchErr)
	}
	if err := s.persistSnapshot(ctx, snap); err != nil {
		return errors.Join(fetchErr, err)
	}
	if !streamsActivities {
		return fetchErr
	}

	states := make([]domainsync.ActivitySyncState, 0, len(snap.Accounts))
	for _, acc := range snap.Accounts {
		stored, err := s.accounts.Get(ctx, acc.ID)
		if err != nil {
			return errors.Join(fetchErr, fmt.Errorf("load activity sync state for %s: %w", acc.ID, err))
		}
		if !stored.SyncEnabled {
			continue
		}
		states = append(states, domainsync.ActivitySyncState{
			AccountID: stored.ID, InitialSyncCompleted: stored.InitialTxSyncDone,
			LastSuccessfulSync: stored.LastTxSync, NextOffset: stored.ActivitySyncOffset,
		})
	}
	streamErr := paged.StreamActivities(ctx, states, s.persistActivityPage)
	return errors.Join(fetchErr, streamErr)
}

func hasSnapshotData(snap domainsync.BrokerSnapshot) bool {
	return snap.Connection.ID != "" || len(snap.Connections) > 0 || len(snap.Accounts) > 0 ||
		len(snap.Holdings) > 0 || len(snap.Activities) > 0
}

func (s *Service) persistSnapshot(ctx context.Context, snap domainsync.BrokerSnapshot) error {
	connections := snap.Connections
	if len(connections) == 0 && snap.Connection.ID != "" {
		connections = []brokerage.Connection{snap.Connection}
	}
	for _, conn := range connections {
		if err := s.connections.Upsert(ctx, conn); err != nil {
			return fmt.Errorf("upsert connection: %w", err)
		}
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

func (s *Service) persistActivityPage(ctx context.Context, page domainsync.ActivityPage) error {
	if len(page.Items) > 0 {
		if err := s.activities.UpsertBatch(ctx, page.AccountID, page.Items); err != nil {
			return fmt.Errorf("upsert activity page: %w", err)
		}
	}
	progress := repository.ActivitySyncProgress{
		NextOffset: page.NextOffset, FirstTransactionDate: page.FirstTransactionDate,
	}
	if page.Complete {
		completedAt := time.Now().UTC()
		progress.CompletedAt = &completedAt
	}
	if err := s.accounts.UpdateActivitySyncProgress(ctx, page.AccountID, progress); err != nil {
		return fmt.Errorf("update activity sync checkpoint: %w", err)
	}
	return nil
}

// StartSync wires the lifecycle so the goroutine is spawned at OnStart and
// canceled at OnStop.
func StartSync(lc fx.Lifecycle, s *Service) {
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			s.start(ctx)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			return s.wait(stopCtx)
		},
	})
}

func (s *Service) start(ctx context.Context) {
	for _, c := range s.clients {
		client := c
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			s.loopClient(ctx, client)
		}()
	}
}

func (s *Service) loopClient(ctx context.Context, c domainsync.BrokerClient) {
	s.runClient(ctx, c)
	interval := s.interval
	if scheduled, ok := c.(domainsync.ScheduledBrokerClient); ok && scheduled.SyncInterval() > 0 {
		interval = scheduled.SyncInterval()
	}
	if interval <= 0 {
		interval = 4 * time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runClient(ctx, c)
		}
	}
}

func (s *Service) wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AsBrokerClient is the fx group helper for registering a BrokerClient.
func AsBrokerClient(f any) any {
	return fx.Annotate(f,
		fx.As(new(domainsync.BrokerClient)),
		fx.ResultTags(`group:"broker_clients"`),
	)
}
