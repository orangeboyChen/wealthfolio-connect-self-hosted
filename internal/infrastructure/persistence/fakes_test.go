package persistence_test

import (
	"database/sql"
	"regexp"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newMockDB builds a *gorm.DB backed by go-sqlmock so repository tests can
// assert SQL behavior without spinning up a real PostgreSQL instance.
//
// QueryMatcherRegexp is used so test expectations can match on substrings
// rather than the exact statements GORM produces.
func newMockDB() (*gorm.DB, sqlmock.Sqlmock, *sql.DB, error) {
	sqlDB, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp),
	)
	if err != nil {
		return nil, nil, nil, err
	}
	dialector := postgres.New(postgres.Config{
		DSN:                  "sqlmock_db",
		DriverName:           "postgres",
		Conn:                 sqlDB,
		PreferSimpleProtocol: true,
	})
	gormDB, err := gorm.Open(dialector, &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
		SkipDefaultTransaction:                   true,
	})
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, nil, err
	}
	return gormDB, mock, sqlDB, nil
}

// rx wraps a literal SQL substring as a case-insensitive regex anchored on
// any whitespace boundary. It hides regexp.QuoteMeta noise from the specs.
func rx(literal string) string { return "(?i)" + regexp.QuoteMeta(literal) }
