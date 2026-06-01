package persistence_test

import (
	"context"
	"errors"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/persistence"
)

var holdingsCols = []string{"account_id", "captured_at", "balances", "positions", "options"}

var _ = Describe("HoldingRepository", func() {
	var (
		ctx  context.Context
		mock sqlmock.Sqlmock
		repo repository.HoldingRepository
		now  time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, m, _, err := newMockDB()
		Expect(err).NotTo(HaveOccurred())
		mock = m
		repo = persistence.NewHoldingRepository(db)
		now = time.Now().UTC().Truncate(time.Second)
	})

	It("returns ErrNotFound when no snapshot exists", func() {
		mock.ExpectQuery(rx(`FROM "holdings_snapshot"`)).
			WillReturnRows(sqlmock.NewRows(holdingsCols))
		_, err := repo.GetLatest(ctx, "acc")
		Expect(errors.Is(err, repository.ErrNotFound)).To(BeTrue())
	})

	It("decodes JSON columns", func() {
		mock.ExpectQuery(rx(`FROM "holdings_snapshot"`)).
			WillReturnRows(sqlmock.NewRows(holdingsCols).AddRow(
				"acc", now,
				[]byte(`[{"Currency":{"Code":"USD"},"Cash":1.5,"BuyingPower":2.5}]`),
				[]byte(`[]`),
				[]byte(`[]`),
			))
		h, err := repo.GetLatest(ctx, "acc")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.Balances).To(HaveLen(1))
		Expect(h.Balances[0].Cash).To(Equal(1.5))
	})

	It("propagates JSON decode errors", func() {
		mock.ExpectQuery(rx(`FROM "holdings_snapshot"`)).
			WillReturnRows(sqlmock.NewRows(holdingsCols).AddRow(
				"acc", now, []byte(`bad`), []byte(`[]`), []byte(`[]`),
			))
		_, err := repo.GetLatest(ctx, "acc")
		Expect(err).To(MatchError(ContainSubstring("balances decode")))
	})

	It("propagates non-NotFound query errors", func() {
		mock.ExpectQuery(rx(`FROM "holdings_snapshot"`)).WillReturnError(errors.New("boom"))
		_, err := repo.GetLatest(ctx, "acc")
		Expect(err).To(MatchError(ContainSubstring("boom")))
	})

	It("replaces a snapshot", func() {
		// GORM emits INSERT ... RETURNING because the PO has []byte columns,
		// so the driver-level call goes through Query rather than Exec.
		mock.ExpectQuery(rx(`INSERT INTO "holdings_snapshot"`)).
			WillReturnRows(sqlmock.NewRows([]string{"balances", "positions", "options"}).
				AddRow([]byte(`[]`), []byte(`[]`), []byte(`[]`)))
		Expect(repo.Replace(ctx, brokerage.Holdings{AccountID: "acc"})).To(Succeed())
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("propagates replace errors", func() {
		mock.ExpectQuery(rx(`INSERT INTO "holdings_snapshot"`)).WillReturnError(errors.New("nope"))
		err := repo.Replace(ctx, brokerage.Holdings{AccountID: "acc", CapturedAt: now})
		Expect(err).To(MatchError(ContainSubstring("nope")))
	})
})

var _ = Describe("TokenRepository", func() {
	It("inserts a token", func() {
		db, mock, _, err := newMockDB()
		Expect(err).NotTo(HaveOccurred())
		repo := persistence.NewTokenRepository(db)
		mock.ExpectExec(rx(`INSERT INTO "tokens"`)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		err = repo.Insert(context.Background(), repository.TokenMetadata{
			TokenID: "t", Subject: "u",
			IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})

	It("propagates errors", func() {
		db, mock, _, err := newMockDB()
		Expect(err).NotTo(HaveOccurred())
		repo := persistence.NewTokenRepository(db)
		mock.ExpectExec(rx(`INSERT INTO "tokens"`)).WillReturnError(errors.New("dup"))
		err = repo.Insert(context.Background(), repository.TokenMetadata{TokenID: "t"})
		Expect(err).To(MatchError(ContainSubstring("dup")))
	})
})
