package brokerage_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	appbrokerage "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
	repomocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository/mocks"
)

var _ = Describe("ActivityService.List", func() {
	var (
		ctrl    *gomock.Controller
		actRepo *repomocks.MockActivityRepository
		accRepo *repomocks.MockAccountRepository
		svc     *appbrokerage.ActivityService
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		actRepo = repomocks.NewMockActivityRepository(ctrl)
		accRepo = repomocks.NewMockAccountRepository(ctrl)
		svc = appbrokerage.NewActivityService(actRepo, accRepo)
	})

	AfterEach(func() { ctrl.Finish() })

	It("returns ErrAccountNotFound when account is missing", func() {
		accRepo.EXPECT().Get(gomock.Any(), "missing").Return(brokerage.Account{}, repository.ErrNotFound)
		_, err := svc.List(context.Background(), appbrokerage.ActivityQuery{AccountID: "missing"})
		Expect(errors.Is(err, appbrokerage.ErrAccountNotFound)).To(BeTrue())
	})

	It("wraps unexpected account errors", func() {
		accRepo.EXPECT().Get(gomock.Any(), "x").Return(brokerage.Account{}, errors.New("boom"))
		_, err := svc.List(context.Background(), appbrokerage.ActivityQuery{AccountID: "x"})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, appbrokerage.ErrAccountNotFound)).To(BeFalse())
	})

	It("applies default and max limits and clamps offset", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a"}, nil)
		actRepo.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, f repository.ActivityFilter) ([]brokerage.Activity, int, error) {
				Expect(f.Limit).To(Equal(appbrokerage.DefaultActivityLimit))
				Expect(f.Offset).To(Equal(0))
				return nil, 0, nil
			})
		res, err := svc.List(context.Background(), appbrokerage.ActivityQuery{AccountID: "a", Offset: -10})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Items).NotTo(BeNil())
		Expect(res.HasMore).To(BeFalse())
	})

	It("caps oversized limits to max", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a"}, nil)
		actRepo.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, f repository.ActivityFilter) ([]brokerage.Activity, int, error) {
				Expect(f.Limit).To(Equal(appbrokerage.MaxActivityLimit))
				return nil, 0, nil
			})
		_, err := svc.List(context.Background(), appbrokerage.ActivityQuery{AccountID: "a", Limit: 999999})
		Expect(err).NotTo(HaveOccurred())
	})

	It("computes has_more when there are more pages", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a"}, nil)
		actRepo.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]brokerage.Activity{{ID: "x"}}, 5, nil)
		res, err := svc.List(context.Background(), appbrokerage.ActivityQuery{AccountID: "a", Offset: 0, Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.HasMore).To(BeTrue())
		Expect(res.Total).To(Equal(5))
	})

	It("propagates repository errors", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a"}, nil)
		actRepo.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, 0, errors.New("db"))
		_, err := svc.List(context.Background(), appbrokerage.ActivityQuery{AccountID: "a"})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("HoldingService.Get", func() {
	var (
		ctrl    *gomock.Controller
		hldRepo *repomocks.MockHoldingRepository
		accRepo *repomocks.MockAccountRepository
		svc     *appbrokerage.HoldingService
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		hldRepo = repomocks.NewMockHoldingRepository(ctrl)
		accRepo = repomocks.NewMockAccountRepository(ctrl)
		svc = appbrokerage.NewHoldingService(hldRepo, accRepo)
	})

	AfterEach(func() { ctrl.Finish() })

	It("returns ErrAccountNotFound when account missing", func() {
		accRepo.EXPECT().Get(gomock.Any(), "x").Return(brokerage.Account{}, repository.ErrNotFound)
		_, err := svc.Get(context.Background(), "x")
		Expect(errors.Is(err, appbrokerage.ErrAccountNotFound)).To(BeTrue())
	})

	It("wraps unexpected account errors", func() {
		accRepo.EXPECT().Get(gomock.Any(), "x").Return(brokerage.Account{}, errors.New("boom"))
		_, err := svc.Get(context.Background(), "x")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, appbrokerage.ErrAccountNotFound)).To(BeFalse())
	})

	It("returns empty snapshot when not stored yet", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a"}, nil)
		hldRepo.EXPECT().GetLatest(gomock.Any(), "a").Return(brokerage.Holdings{}, repository.ErrNotFound)
		res, err := svc.Get(context.Background(), "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Account.ID).To(Equal("a"))
		Expect(res.Holdings.AccountID).To(Equal("a"))
	})

	It("returns the stored snapshot", func() {
		t := time.Now().UTC()
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a"}, nil)
		hldRepo.EXPECT().GetLatest(gomock.Any(), "a").Return(brokerage.Holdings{AccountID: "a", CapturedAt: t}, nil)
		res, err := svc.Get(context.Background(), "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Holdings.CapturedAt).To(Equal(t))
	})

	It("propagates unexpected repository errors", func() {
		accRepo.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a"}, nil)
		hldRepo.EXPECT().GetLatest(gomock.Any(), "a").Return(brokerage.Holdings{}, errors.New("db"))
		_, err := svc.Get(context.Background(), "a")
		Expect(err).To(HaveOccurred())
	})
})
