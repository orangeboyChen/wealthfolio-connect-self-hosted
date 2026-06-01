package database

import "gorm.io/gorm"

// SetDialectorForTest is an internal hook that lets unit tests replace the
// dialector used by NewGormDB. The returned function restores the previous
// value when invoked, making it safe to use inside DeferCleanup.
func SetDialectorForTest(d func(string) gorm.Dialector) func() {
	old := dialectorFromURL
	dialectorFromURL = d
	return func() { dialectorFromURL = old }
}

// ConfigurePoolForTest exposes configurePool to the database_test package
// so the connection-pool tuning helper can be exercised without a live DB.
var ConfigurePoolForTest = configurePool
