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

var accountCols = []string{
	"id", "name", "account_number", "type", "raw_type", "currency",
	"balance_total", "balance_currency", "brokerage_authorization",
	"institution_name", "sync_enabled", "shared_with_household", "is_paper",
	"status", "created_date", "last_tx_sync", "last_holdings_sync",
	"first_tx_date", "initial_tx_sync_done", "initial_holdings_done",
	"owner_user_id", "owner_full_name", "owner_email",
}

func accountRow(id string, created time.Time) []driver.Value {
	return []driver.Value{
		id, "Name", "1234", "SECURITIES", "MARGIN", "USD",
		100.0, "USD", "auth-1", "Futu", true, false, false,
		"open", created, nil, nil, nil, false, false,
		"u", "U Name", "u@x.com",
	}
}

var _ = Describe("AccountRepository", func() {
	var (
		ctx  context.Context
		mock sqlmock.Sqlmock
		repo repository.AccountRepository
		now  time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, m, _, err := newMockDB()
		Expect(err).NotTo(HaveOccurred())
		mock = m
		repo = persistence.NewAccountRepository(db)
		now = time.Now().UTC().Truncate(time.Second)
	})

	It("lists accounts", func() {
		mock.ExpectQuery(rx(`FROM "accounts"`)).
			WillReturnRows(sqlmock.NewRows(accountCols).AddRow(accountRow("acc1", now)...))
		out, err := repo.List(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HaveLen(1))
		Expect(out[0].ID).To(Equal("acc1"))
		Expect(out[0].Type).To(Equal(brokerage.AccountTypeSecurities))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("propagates list query errors", func() {
		mock.ExpectQuery(rx(`FROM "accounts"`)).WillReturnError(errors.New("fail"))
		_, err := repo.List(ctx)
		Expect(err).To(MatchError(ContainSubstring("fail")))
	})

	It("returns ErrNotFound when account is missing", func() {
		mock.ExpectQuery(rx(`FROM "accounts"`)).
			WillReturnRows(sqlmock.NewRows(accountCols))
		_, err := repo.Get(ctx, "missing")
		Expect(errors.Is(err, repository.ErrNotFound)).To(BeTrue())
	})

	It("propagates other Get errors", func() {
		mock.ExpectQuery(rx(`FROM "accounts"`)).WillReturnError(errors.New("oops"))
		_, err := repo.Get(ctx, "x")
		Expect(err).To(MatchError(ContainSubstring("oops")))
	})

	It("scans Get successfully", func() {
		mock.ExpectQuery(rx(`FROM "accounts"`)).
			WillReturnRows(sqlmock.NewRows(accountCols).AddRow(accountRow("acc-x", now)...))
		acc, err := repo.Get(ctx, "acc-x")
		Expect(err).NotTo(HaveOccurred())
		Expect(acc.ID).To(Equal("acc-x"))
	})

	It("upserts an account, defaulting CreatedDate", func() {
		mock.ExpectExec(rx(`INSERT INTO "accounts"`)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		Expect(repo.Upsert(ctx, brokerage.Account{ID: "id"})).To(Succeed())
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("propagates upsert errors", func() {
		mock.ExpectExec(rx(`INSERT INTO "accounts"`)).WillReturnError(errors.New("dup"))
		err := repo.Upsert(ctx, brokerage.Account{ID: "id", CreatedDate: time.Now()})
		Expect(err).To(MatchError(ContainSubstring("dup")))
	})

	It("updates sync status when both timestamps are provided", func() {
		mock.ExpectExec(rx(`UPDATE "accounts" SET`)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		t1 := time.Now()
		Expect(repo.UpdateSyncStatus(ctx, "acc", &t1, &t1)).To(Succeed())
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("is a no-op when both timestamps are nil", func() {
		// No SQL is expected at all.
		Expect(repo.UpdateSyncStatus(ctx, "acc", nil, nil)).To(Succeed())
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("propagates sync status errors", func() {
		mock.ExpectExec(rx(`UPDATE "accounts" SET`)).WillReturnError(errors.New("nope"))
		t1 := time.Now()
		err := repo.UpdateSyncStatus(ctx, "acc", &t1, nil)
		Expect(err).To(MatchError(ContainSubstring("nope")))
	})

	It("SetSyncEnabled updates the row", func() {
		mock.ExpectExec(rx(`UPDATE "accounts" SET "sync_enabled"`)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		Expect(repo.SetSyncEnabled(ctx, "acc-1", true)).To(Succeed())
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("SetSyncEnabled returns ErrNotFound when no rows match", func() {
		mock.ExpectExec(rx(`UPDATE "accounts" SET "sync_enabled"`)).
			WillReturnResult(sqlmock.NewResult(0, 0))
		err := repo.SetSyncEnabled(ctx, "missing", false)
		Expect(errors.Is(err, repository.ErrNotFound)).To(BeTrue())
	})

	It("SetSyncEnabled propagates driver errors", func() {
		mock.ExpectExec(rx(`UPDATE "accounts" SET "sync_enabled"`)).
			WillReturnError(errors.New("boom"))
		err := repo.SetSyncEnabled(ctx, "acc-1", true)
		Expect(err).To(MatchError(ContainSubstring("boom")))
	})
})
