package brokerage_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	appbrokerage "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
	repomocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository/mocks"
)

func TestApplicationBrokerage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Application Brokerage Suite")
}

var _ = Describe("ConnectionService.List", func() {
	var (
		ctrl *gomock.Controller
		repo *repomocks.MockConnectionRepository
		svc  *appbrokerage.ConnectionService
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		repo = repomocks.NewMockConnectionRepository(ctrl)
		svc = appbrokerage.NewConnectionService(repo)
	})

	AfterEach(func() { ctrl.Finish() })

	It("returns connections from repository", func() {
		repo.EXPECT().List(gomock.Any()).Return([]brokerage.Connection{
			{ID: "c1", BrokerageSlug: "futu"},
		}, nil)
		got, err := svc.List(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].ID).To(Equal("c1"))
	})

	It("returns empty slice when repository returns nil", func() {
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		got, err := svc.List(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(got).To(BeEmpty())
	})

	It("wraps repository errors", func() {
		repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("boom"))
		_, err := svc.List(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("boom"))
	})
})

var _ = Describe("AccountService", func() {
	var (
		ctrl *gomock.Controller
		repo *repomocks.MockAccountRepository
		svc  *appbrokerage.AccountService
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		repo = repomocks.NewMockAccountRepository(ctrl)
		svc = appbrokerage.NewAccountService(repo)
	})

	AfterEach(func() { ctrl.Finish() })

	It("List returns accounts from repository", func() {
		repo.EXPECT().List(gomock.Any()).Return([]brokerage.Account{{ID: "a1"}}, nil)
		got, err := svc.List(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
	})

	It("List returns empty slice when nil", func() {
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		got, err := svc.List(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
	})

	It("List wraps repo error", func() {
		repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db"))
		_, err := svc.List(context.Background())
		Expect(err).To(HaveOccurred())
	})

	It("Get returns account by id", func() {
		repo.EXPECT().Get(gomock.Any(), "a1").Return(brokerage.Account{ID: "a1"}, nil)
		got, err := svc.Get(context.Background(), "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal("a1"))
	})

	It("Get maps ErrNotFound to ErrAccountNotFound", func() {
		repo.EXPECT().Get(gomock.Any(), "missing").Return(brokerage.Account{}, repository.ErrNotFound)
		_, err := svc.Get(context.Background(), "missing")
		Expect(errors.Is(err, appbrokerage.ErrAccountNotFound)).To(BeTrue())
	})

	It("Get wraps unexpected errors", func() {
		repo.EXPECT().Get(gomock.Any(), "x").Return(brokerage.Account{}, errors.New("kaput"))
		_, err := svc.Get(context.Background(), "x")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, appbrokerage.ErrAccountNotFound)).To(BeFalse())
	})

	It("SetSyncEnabled toggles the flag and returns the refreshed account", func() {
		repo.EXPECT().SetSyncEnabled(gomock.Any(), "a1", false).Return(nil)
		repo.EXPECT().Get(gomock.Any(), "a1").Return(brokerage.Account{ID: "a1", SyncEnabled: false}, nil)
		got, err := svc.SetSyncEnabled(context.Background(), "a1", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal("a1"))
		Expect(got.SyncEnabled).To(BeFalse())
	})

	It("SetSyncEnabled maps ErrNotFound to ErrAccountNotFound", func() {
		repo.EXPECT().SetSyncEnabled(gomock.Any(), "missing", true).Return(repository.ErrNotFound)
		_, err := svc.SetSyncEnabled(context.Background(), "missing", true)
		Expect(errors.Is(err, appbrokerage.ErrAccountNotFound)).To(BeTrue())
	})

	It("SetSyncEnabled wraps unexpected errors", func() {
		repo.EXPECT().SetSyncEnabled(gomock.Any(), "a1", true).Return(errors.New("db"))
		_, err := svc.SetSyncEnabled(context.Background(), "a1", true)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, appbrokerage.ErrAccountNotFound)).To(BeFalse())
	})

	It("SetSyncEnabled propagates Get errors after a successful update", func() {
		repo.EXPECT().SetSyncEnabled(gomock.Any(), "a1", true).Return(nil)
		repo.EXPECT().Get(gomock.Any(), "a1").Return(brokerage.Account{}, errors.New("read"))
		_, err := svc.SetSyncEnabled(context.Background(), "a1", true)
		Expect(err).To(HaveOccurred())
	})
})
