package persistence_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/persistence"
)

// connectionRow is the column order used by GORM when SELECTing from the
// connections table. It mirrors the field order in ConnectionPO so AddRow
// arguments stay legible.
var connectionCols = []string{
	"id", "authorization_id", "brokerage_name", "brokerage_slug",
	"display_name", "logo_url", "square_logo_url", "disabled",
	"name", "status", "updated_at",
}

func connectionRow(id string, t time.Time) []driver.Value {
	return []driver.Value{id, "auth-" + id, "Futu", "futu", "Futu", "logo", "sq", false, "Acct", "active", t}
}

var _ = Describe("ConnectionRepository", func() {
	var (
		ctx  context.Context
		mock sqlmock.Sqlmock
		repo repository.ConnectionRepository
		now  time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, m, _, err := newMockDB()
		Expect(err).NotTo(HaveOccurred())
		mock = m
		repo = persistence.NewConnectionRepository(db)
		now = time.Now().UTC().Truncate(time.Second)
	})

	It("lists connections", func() {
		mock.ExpectQuery(rx(`FROM "connections"`)).
			WillReturnRows(sqlmock.NewRows(connectionCols).AddRow(connectionRow("c1", now)...))

		out, err := repo.List(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HaveLen(1))
		Expect(out[0].ID).To(Equal("c1"))
		Expect(out[0].BrokerageSlug).To(Equal("futu"))
		Expect(out[0].Status).To(Equal(brokerage.ConnectionActive))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("propagates list query errors", func() {
		mock.ExpectQuery(rx(`FROM "connections"`)).WillReturnError(errors.New("boom"))
		_, err := repo.List(ctx)
		Expect(err).To(MatchError(ContainSubstring("boom")))
	})

	It("upserts a connection, defaulting UpdatedAt and Status", func() {
		mock.ExpectExec(rx(`INSERT INTO "connections"`)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		Expect(repo.Upsert(ctx, brokerage.Connection{ID: "c1", BrokerageSlug: "futu"})).To(Succeed())
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("propagates upsert errors", func() {
		mock.ExpectExec(rx(`INSERT INTO "connections"`)).WillReturnError(errors.New("dup"))
		err := repo.Upsert(ctx, brokerage.Connection{ID: "c1", UpdatedAt: time.Now()})
		Expect(err).To(MatchError(ContainSubstring("dup")))
	})
})
