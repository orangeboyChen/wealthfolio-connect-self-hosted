package database_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/database"
)

func TestDatabase(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Database Suite")
}

// newMockGorm builds a *gorm.DB backed by go-sqlmock for unit tests in the
// database package itself.
func newMockGorm() (*gorm.DB, sqlmock.Sqlmock, *sql.DB) {
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	Expect(err).NotTo(HaveOccurred())
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "sqlmock_db", DriverName: "postgres",
		Conn: sqlDB, PreferSimpleProtocol: true,
	}), &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
		SkipDefaultTransaction:                   true,
	})
	Expect(err).NotTo(HaveOccurred())
	return db, mock, sqlDB
}

// stubMigrator returns a fixed slice of zero models. The empty-slice path of
// Migrate exercises the early-return branch.
type stubMigrator struct{ models []any }

func (s stubMigrator) Models() []any { return s.models }

var _ = Describe("Readiness", func() {
	It("starts not ready and toggles", func() {
		r := database.NewReadiness()
		Expect(r.Ready()).To(BeFalse())
		r.MarkReady()
		Expect(r.Ready()).To(BeTrue())
	})
})

var _ = Describe("Migrate", func() {
	It("short-circuits on an empty model list and marks readiness", func() {
		db, _, sqlDB := newMockGorm()
		defer sqlDB.Close()
		r := database.NewReadiness()
		Expect(database.Migrate(context.Background(), db, stubMigrator{}, r)).To(Succeed())
		Expect(r.Ready()).To(BeTrue())
	})

	It("propagates AutoMigrate errors", func() {
		db, mock, sqlDB := newMockGorm()
		defer sqlDB.Close()
		// First DDL statement issued by AutoMigrate fails.
		mock.ExpectQuery(`.*`).WillReturnError(errors.New("syntax"))
		err := database.Migrate(context.Background(), db,
			stubMigrator{models: []any{&dummyModel{}}}, nil)
		Expect(err).To(MatchError(ContainSubstring("auto-migrate")))
	})
})

// dummyModel is a tiny GORM model used only to force AutoMigrate to issue
// at least one statement against the mock.
type dummyModel struct {
	ID string `gorm:"primaryKey"`
}

func (dummyModel) TableName() string { return "dummy_models" }

// errBoom is a shared canned error used by the AutoMigrate-failure specs.
var errBoom = errors.New("boom")

// noSQLDBPool implements gorm.ConnPool but is *not* a *sql.DB, so the
// db.DB() cast inside Pool.Ping/Close fails and exercises the error branch.
type noSQLDBPool struct{}

func (noSQLDBPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errBoom
}

func (noSQLDBPool) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, errBoom
}

func (noSQLDBPool) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, errBoom
}

func (noSQLDBPool) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	return nil
}
