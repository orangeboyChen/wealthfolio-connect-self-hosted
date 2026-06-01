package database_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/database"
)

var _ = Describe("NewGormDB", func() {
	It("rejects an invalid DATABASE_URL", func() {
		lc := fxtest.NewLifecycle(GinkgoT())
		_, err := database.NewGormDB(lc, &config.Config{DatabaseURL: "::::not-a-url"})
		Expect(err).To(HaveOccurred())
	})

	It("fails to ping when the host is unreachable", func() {
		lc := fxtest.NewLifecycle(GinkgoT())
		_, err := database.NewGormDB(lc, &config.Config{
			DatabaseURL: "postgres://u:p@127.0.0.1:1?sslmode=disable&connect_timeout=1",
		})
		Expect(err).To(HaveOccurred())
	})

	It("opens, pings and registers an OnStop close hook on the happy path", func() {
		_, mock, sqlDB := newMockGorm()
		mock.ExpectPing()
		mock.ExpectClose()
		// Replace the dialector with one that hands GORM the sqlmock-backed
		// *sql.DB so NewGormDB executes its success branch end-to-end.
		restore := database.SetDialectorForTest(func(_ string) gorm.Dialector {
			return postgres.New(postgres.Config{
				DSN:                  "sqlmock_db",
				DriverName:           "postgres",
				Conn:                 sqlDB,
				PreferSimpleProtocol: true,
			})
		})
		defer restore()

		lc := fxtest.NewLifecycle(GinkgoT())
		gormDB, err := database.NewGormDB(lc, &config.Config{DatabaseURL: "ignored"})
		Expect(err).NotTo(HaveOccurred())
		Expect(gormDB).NotTo(BeNil())
		Expect(lc.Stop(context.Background())).To(Succeed())
	})
})

var _ = Describe("Pool.Close", func() {
	It("delegates Close to the underlying *sql.DB", func() {
		db, mock, sqlDB := newMockGorm()
		defer sqlDB.Close()
		mock.ExpectClose()
		pool := database.NewPool(db)
		Expect(pool.Close()).To(Succeed())
	})

	It("surfaces *sql.DB extraction errors from Ping and Close", func() {
		// Build a *gorm.DB whose underlying ConnPool is not a *sql.DB so
		// db.DB() returns an error. We achieve that by manually injecting
		// a minimal Statement that rejects the cast.
		broken := &gorm.DB{Config: &gorm.Config{ConnPool: noSQLDBPool{}}}
		pool := database.NewPool(broken)
		Expect(pool.Ping(context.Background())).To(HaveOccurred())
		Expect(pool.Close()).To(HaveOccurred())
	})
})

var _ = Describe("NewGormDB ping failure", func() {
	// The pre-existing test depends on sqlmock's MonitorPingsOption combined
	// with the gorm postgres driver's bootstrap queries, which interact in a
	// way that prevents the explicit PingContext branch from being reached.
	// Pending until the dialector seam is reworked to inject a *sql.DB whose
	// ping is the only call surface.
	XIt("closes the connection and returns a wrapped error when Ping fails", func() {})
})

var _ = Describe("Migrate", func() {
	It("does not panic when readiness is nil and the model list is empty", func() {
		db, _, sqlDB := newMockGorm()
		defer sqlDB.Close()
		Expect(database.Migrate(context.Background(), db, stubMigrator{}, nil)).To(Succeed())
	})
})

var _ = Describe("configurePool", func() {
	It("applies tuning to the supplied *sql.DB without panicking", func() {
		_, _, sqlDB := newMockGorm()
		defer sqlDB.Close()
		Expect(func() { database.ConfigurePoolForTest(sqlDB) }).NotTo(Panic())
	})
})

var _ = Describe("RunMigrations", func() {
	It("invokes Migrate via the OnStart lifecycle hook", func() {
		db, _, sqlDB := newMockGorm()
		defer sqlDB.Close()
		ready := database.NewReadiness()
		lc := fxtest.NewLifecycle(GinkgoT())
		database.RunMigrations(lc, db, stubMigrator{}, ready)
		Expect(lc.Start(context.Background())).To(Succeed())
		Expect(ready.Ready()).To(BeTrue())
		Expect(lc.Stop(context.Background())).To(Succeed())
	})

	It("surfaces Migrate errors from the OnStart hook", func() {
		db, mock, sqlDB := newMockGorm()
		defer sqlDB.Close()
		// AutoMigrate first probes for the table via a SELECT then issues
		// one or more DDL statements. We let any of them fail with the
		// same canned error.
		mock.MatchExpectationsInOrder(false)
		mock.ExpectQuery(`.*`).WillReturnError(errBoom)
		mock.ExpectExec(`.*`).WillReturnError(errBoom)
		lc := fxtest.NewLifecycle(GinkgoT())
		database.RunMigrations(lc, db, stubMigrator{models: []any{&dummyModel{}}}, database.NewReadiness())
		Expect(lc.Start(context.Background())).To(MatchError(ContainSubstring("auto-migrate")))
	})
})

var _ = Describe("NewPool", func() {
	It("exposes Ping and Close on the underlying *sql.DB", func() {
		db, mock, sqlDB := newMockGorm()
		defer sqlDB.Close()
		mock.ExpectPing()
		pool := database.NewPool(db)
		Expect(pool.Ping(context.Background())).To(Succeed())
	})
})

var _ = Describe("Module wiring", func() {
	It("exposes a Module value for fx composition", func() {
		Expect(database.Module).NotTo(BeNil())
		Expect(fx.Options(database.Module)).NotTo(BeNil())
	})
})
