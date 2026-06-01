// Package database wires the GORM-managed PostgreSQL connection and the
// AutoMigrate-based schema runner into the fx graph.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	"go.uber.org/fx"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

// Module exposes the database building blocks.
var Module = fx.Module("database",
	fx.Provide(
		NewPool,
		func(p Pool) Pinger { return p },
		NewGormDB,
		NewReadiness,
	),
	fx.Invoke(RunMigrations),
)

// Pinger reports whether the underlying database is reachable. The HTTP
// healthcheck handler depends on it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Pool is the union of capabilities that the rest of the codebase needs from
// the database connection: pinging for healthchecks and being closable at
// shutdown. It is intentionally tiny so it can be faked in tests.
type Pool interface {
	Pinger
	Close() error
}

// gormPool adapts *gorm.DB to the Pool interface.
type gormPool struct{ db *gorm.DB }

// Ping verifies the database connection is alive.
func (g *gormPool) Ping(ctx context.Context) error {
	sqlDB, err := g.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// Close releases the underlying database connection pool.
func (g *gormPool) Close() error {
	sqlDB, err := g.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// dialectorFromURL builds the GORM dialector for the supplied DSN. It is
// stored in a package-level variable so unit tests can swap in a sqlmock-
// backed dialector without spinning up real PostgreSQL.
var dialectorFromURL = postgres.Open

// NewGormDB opens a GORM connection using cfg.DatabaseURL, verifies it with a
// Ping, and registers an OnStop hook that closes the underlying *sql.DB. It
// is the single source of truth for a *gorm.DB across the application.
func NewGormDB(lc fx.Lifecycle, cfg *config.Config) (*gorm.DB, error) {
	gormCfg := &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Warn),
		DisableForeignKeyConstraintWhenMigrating: true,
	}
	db, err := gorm.Open(dialectorFromURL(cfg.DatabaseURL), gormCfg)
	if err != nil {
		return nil, fmt.Errorf("database: opening gorm: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("database: extracting *sql.DB: %w", err)
	}
	configurePool(sqlDB)
	if err := sqlDB.PingContext(context.Background()); err != nil {
		_ = sqlDB.Close() //nolint:errcheck // primary error already wraps the cause; cleanup failure is unactionable
		return nil, fmt.Errorf("database: ping: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(_ context.Context) error {
		return sqlDB.Close()
	}})
	return db, nil
}

func configurePool(sqlDB *sql.DB) {
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
}

// NewPool returns a Pool wrapper around the *gorm.DB. The wrapper exists so
// that the HTTP health handler can stay decoupled from GORM specifics.
func NewPool(db *gorm.DB) Pool { return &gormPool{db: db} }

// Readiness reports whether the migration runner has finished. It is used by
// the HTTP /readyz handler.
type Readiness struct {
	done atomic.Bool
}

// NewReadiness returns a fresh tracker.
func NewReadiness() *Readiness { return &Readiness{} }

// MarkReady flips the flag.
func (r *Readiness) MarkReady() { r.done.Store(true) }

// Ready reports the current state.
func (r *Readiness) Ready() bool { return r.done.Load() }

// Migrator is the slice of GORM models that AutoMigrate must converge.
// infrastructure/persistence supplies the actual list via fx; we only depend
// on the interface so the database package stays decoupled from PO layout.
type Migrator interface {
	Models() []any
}

// RunMigrations is the fx hook entry point that drives AutoMigrate at
// startup.
func RunMigrations(lc fx.Lifecycle, db *gorm.DB, m Migrator, ready *Readiness) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return Migrate(ctx, db, m, ready)
		},
	})
}

// Migrate runs GORM AutoMigrate against the supplied models. Exposed so unit
// tests can drive it against an in-memory or mocked *gorm.DB.
func Migrate(ctx context.Context, db *gorm.DB, m Migrator, ready *Readiness) error {
	models := m.Models()
	if len(models) == 0 {
		if ready != nil {
			ready.MarkReady()
		}
		return nil
	}
	if err := db.WithContext(ctx).AutoMigrate(models...); err != nil {
		return fmt.Errorf("database: auto-migrate: %w", err)
	}
	if ready != nil {
		ready.MarkReady()
	}
	return nil
}
