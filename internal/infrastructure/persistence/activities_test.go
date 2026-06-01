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

var activityCols = []string{
	"id", "account_id", "source_record_id",
	"symbol_ticker", "symbol_raw", "symbol_description", "symbol_name",
	"symbol_type_code", "symbol_type_desc", "symbol_exchange_code",
	"symbol_exchange_mic", "symbol_exchange_name", "symbol_exchange_suffix",
	"symbol_currency_code", "symbol_currency_name", "symbol_figi",
	"price", "units", "amount", "currency_code", "currency_name",
	"type", "subtype", "raw_type", "option_type", "description",
	"trade_date", "settlement_date", "fee", "fx_rate",
	"institution", "external_reference_id", "provider_type", "source_system",
	"source_group_id", "needs_review",
}

func activityRow(id, accountID string, withSymbol bool, trade time.Time) []driver.Value {
	tick, raw := "", ""
	if withSymbol {
		tick, raw = "AAPL", "AAPL.US"
	}
	return []driver.Value{
		id, accountID, "src-" + id,
		tick, raw, "Apple", "Apple",
		"EQUITY", "Equity", "NASDAQ", "XNAS", "Nasdaq", "",
		"USD", "US Dollar", "FIGI",
		100.5, 10.0, 1005.0, "USD", "US Dollar",
		"BUY", "", "BUY_MARKET", "", "buy 10 AAPL",
		trade, nil, 1.5, nil,
		"Futu", "ext-1", "CUSTOM", "CUSTOM",
		"", false,
	}
}

var _ = Describe("ActivityRepository", func() {
	var (
		ctx  context.Context
		mock sqlmock.Sqlmock
		repo repository.ActivityRepository
		now  time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, m, _, err := newMockDB()
		Expect(err).NotTo(HaveOccurred())
		mock = m
		repo = persistence.NewActivityRepository(db)
		now = time.Now().UTC().Truncate(time.Second)
	})

	It("paginates with default limit and applies date filters", func() {
		mock.ExpectQuery(rx(`SELECT count(*)`)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
		mock.ExpectQuery(rx(`FROM "activities"`)).
			WillReturnRows(sqlmock.NewRows(activityCols).
				AddRow(activityRow("a1", "acc", true, now)...))

		start := now.Add(-24 * time.Hour)
		end := now
		out, total, err := repo.List(ctx, repository.ActivityFilter{
			AccountID: "acc", StartDate: &start, EndDate: &end,
			Offset: -1, Limit: 0,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(Equal(7))
		Expect(out).To(HaveLen(1))
		Expect(out[0].ID).To(Equal("a1"))
		Expect(out[0].Symbol).NotTo(BeNil())
		Expect(out[0].Symbol.Symbol).To(Equal("AAPL"))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("propagates count errors", func() {
		mock.ExpectQuery(rx(`SELECT count(*)`)).WillReturnError(errors.New("count fail"))
		_, _, err := repo.List(ctx, repository.ActivityFilter{AccountID: "acc"})
		Expect(err).To(MatchError(ContainSubstring("count fail")))
	})

	It("propagates list query errors", func() {
		mock.ExpectQuery(rx(`SELECT count(*)`)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery(rx(`FROM "activities"`)).WillReturnError(errors.New("Q fail"))
		_, _, err := repo.List(ctx, repository.ActivityFilter{AccountID: "acc"})
		Expect(err).To(MatchError(ContainSubstring("Q fail")))
	})

	It("returns an empty slice when no activities match", func() {
		mock.ExpectQuery(rx(`SELECT count(*)`)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery(rx(`FROM "activities"`)).
			WillReturnRows(sqlmock.NewRows(activityCols))
		out, total, err := repo.List(ctx, repository.ActivityFilter{AccountID: "acc"})
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(Equal(0))
		Expect(out).To(BeEmpty())
	})

	It("upserts a batch", func() {
		mock.ExpectExec(rx(`INSERT INTO "activities"`)).
			WillReturnResult(sqlmock.NewResult(0, 2))
		err := repo.UpsertBatch(ctx, "acc", []brokerage.Activity{
			{ID: "1", SourceRecordID: "s1", Type: brokerage.ActivityBuy, TradeDate: now,
				Symbol: &brokerage.Symbol{Symbol: "AAPL"}},
			{ID: "2", SourceRecordID: "s2", Type: brokerage.ActivitySell, TradeDate: now},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("is a no-op when the batch is empty", func() {
		Expect(repo.UpsertBatch(ctx, "acc", nil)).To(Succeed())
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("propagates upsert errors", func() {
		mock.ExpectExec(rx(`INSERT INTO "activities"`)).WillReturnError(errors.New("dup"))
		err := repo.UpsertBatch(ctx, "acc", []brokerage.Activity{{ID: "x", SourceRecordID: "y", TradeDate: now}})
		Expect(err).To(MatchError(ContainSubstring("dup")))
	})
})
