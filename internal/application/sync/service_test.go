package sync_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/mock/gomock"

	appsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/sync"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/brokerage"
	repomocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository/mocks"
	domainsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/sync"
)

func TestSync(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sync Service Suite")
}

// fakeClient implements domainsync.BrokerClient.
type fakeClient struct {
	id   string
	snap domainsync.BrokerSnapshot
	err  error
}

func (f *fakeClient) ID() string { return f.id }
func (f *fakeClient) Fetch(_ context.Context) (domainsync.BrokerSnapshot, error) {
	return f.snap, f.err
}

func sampleSnap() domainsync.BrokerSnapshot {
	return domainsync.BrokerSnapshot{
		Connection: brokerage.Connection{ID: "c", BrokerageSlug: "x"},
		Accounts:   []brokerage.Account{{ID: "a"}},
		Holdings:   []brokerage.Holdings{{AccountID: "a"}},
		Activities: map[string][]brokerage.Activity{
			"a": {{ID: "t1"}},
			"b": {},
		},
	}
}

var _ = Describe("sync.Service", func() {
	var (
		ctrl  *gomock.Controller
		conns *repomocks.MockConnectionRepository
		accs  *repomocks.MockAccountRepository
		acts  *repomocks.MockActivityRepository
		hlds  *repomocks.MockHoldingRepository
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		conns = repomocks.NewMockConnectionRepository(ctrl)
		accs = repomocks.NewMockAccountRepository(ctrl)
		acts = repomocks.NewMockActivityRepository(ctrl)
		hlds = repomocks.NewMockHoldingRepository(ctrl)
	})
	AfterEach(func() { ctrl.Finish() })

	build := func(clients ...domainsync.BrokerClient) *appsync.Service {
		return appsync.NewService(appsync.Params{
			Logger:      zerolog.Nop(),
			Connections: conns,
			Accounts:    accs,
			Activities:  acts,
			Holdings:    hlds,
			Clients:     clients,
		})
	}

	It("RunOnce persists everything from a snapshot", func() {
		conns.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
		accs.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
		hlds.EXPECT().Replace(gomock.Any(), gomock.Any()).Return(nil)
		acts.EXPECT().UpsertBatch(gomock.Any(), "a", gomock.Any()).Return(nil)
		s := build(&fakeClient{id: "x", snap: sampleSnap()})
		Expect(s.RunOnce(context.Background())).To(Succeed())
		Expect(s.LastRun()).NotTo(BeZero())
	})

	It("logs and continues when a client fails", func() {
		s := build(&fakeClient{id: "broken", err: errors.New("boom")})
		Expect(s.RunOnce(context.Background())).To(Succeed())
	})

	It("propagates persistence errors as logs (RunOnce never errors)", func() {
		conns.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(errors.New("db"))
		s := build(&fakeClient{id: "x", snap: sampleSnap()})
		Expect(s.RunOnce(context.Background())).To(Succeed())
	})

	It("propagates account upsert errors", func() {
		conns.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
		accs.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(errors.New("db"))
		s := build(&fakeClient{id: "x", snap: sampleSnap()})
		Expect(s.RunOnce(context.Background())).To(Succeed())
	})

	It("propagates holdings replace errors", func() {
		conns.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
		accs.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
		hlds.EXPECT().Replace(gomock.Any(), gomock.Any()).Return(errors.New("db"))
		s := build(&fakeClient{id: "x", snap: sampleSnap()})
		Expect(s.RunOnce(context.Background())).To(Succeed())
	})

	It("propagates activities upsert errors", func() {
		conns.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
		accs.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
		hlds.EXPECT().Replace(gomock.Any(), gomock.Any()).Return(nil)
		acts.EXPECT().UpsertBatch(gomock.Any(), "a", gomock.Any()).Return(errors.New("db"))
		s := build(&fakeClient{id: "x", snap: sampleSnap()})
		Expect(s.RunOnce(context.Background())).To(Succeed())
	})

	It("StartSync wires lifecycle and triggers immediate run", func() {
		conns.EXPECT().Upsert(gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
		accs.EXPECT().Upsert(gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
		hlds.EXPECT().Replace(gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
		acts.EXPECT().UpsertBatch(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)

		s := build(&fakeClient{id: "x", snap: sampleSnap()})
		s.SetInterval(10 * time.Millisecond)

		app := fxtest.New(GinkgoT(),
			fx.Supply(s),
			fx.Invoke(appsync.StartSync),
		)
		Expect(app.Start(context.Background())).To(Succeed())
		Eventually(s.LastRun, time.Second).ShouldNot(BeZero())
		Expect(app.Stop(context.Background())).To(Succeed())
	})
})

var _ = Describe("AsBrokerClient", func() {
	It("annotates a constructor", func() {
		opt := appsync.AsBrokerClient(func() domainsync.BrokerClient { return &fakeClient{id: "x"} })
		Expect(opt).NotTo(BeNil())
	})
})
