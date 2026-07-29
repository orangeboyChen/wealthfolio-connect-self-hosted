package sync_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
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

type blockingClient struct {
	id      string
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (f *blockingClient) ID() string { return f.id }
func (f *blockingClient) Fetch(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	f.calls.Add(1)
	f.once.Do(func() { close(f.started) })
	select {
	case <-f.release:
		return domainsync.BrokerSnapshot{}, errors.New("released")
	case <-ctx.Done():
		return domainsync.BrokerSnapshot{}, ctx.Err()
	}
}

type pagedClient struct {
	snap  domainsync.BrokerSnapshot
	pages []domainsync.ActivityPage
}

func (f *pagedClient) ID() string { return "paged" }
func (f *pagedClient) Fetch(context.Context) (domainsync.BrokerSnapshot, error) {
	return f.snap, nil
}
func (f *pagedClient) FetchAccountSnapshot(context.Context) (domainsync.BrokerSnapshot, error) {
	return f.snap, nil
}
func (f *pagedClient) StreamActivities(ctx context.Context, _ []domainsync.ActivitySyncState, sink domainsync.ActivitySink) error {
	for _, page := range f.pages {
		if err := sink(ctx, page); err != nil {
			return err
		}
	}
	return nil
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

	It("starts unrelated clients without waiting for a throttled client", func() {
		slow := &blockingClient{id: "slow", started: make(chan struct{}), release: make(chan struct{})}
		fastStarted := make(chan struct{})
		fast := &fakeClient{id: "fast", err: errors.New("fast failure")}
		wrappedFast := &notifyingClient{BrokerClient: fast, started: fastStarted}
		s := build(slow, wrappedFast)
		done := make(chan struct{})
		go func() {
			_ = s.RunOnce(context.Background())
			close(done)
		}()
		Eventually(fastStarted, time.Second).Should(BeClosed())
		Consistently(done, 20*time.Millisecond).ShouldNot(BeClosed())
		close(slow.release)
		Eventually(done, time.Second).Should(BeClosed())
	})

	It("suppresses overlapping runs of the same client", func() {
		client := &blockingClient{id: "one", started: make(chan struct{}), release: make(chan struct{})}
		s := build(client)
		firstDone := make(chan struct{})
		go func() { _ = s.RunOnce(context.Background()); close(firstDone) }()
		Eventually(client.started, time.Second).Should(BeClosed())
		secondDone := make(chan struct{})
		go func() { _ = s.RunOnce(context.Background()); close(secondDone) }()
		Eventually(secondDone, time.Second).Should(BeClosed())
		Expect(client.calls.Load()).To(Equal(int32(1)))
		close(client.release)
		Eventually(firstDone, time.Second).Should(BeClosed())
	})

	It("persists paged activities and advances the checkpoint after the page", func() {
		snap := domainsync.BrokerSnapshot{
			Connection: brokerage.Connection{ID: "c"},
			Accounts:   []brokerage.Account{{ID: "a", SyncEnabled: true}},
		}
		conns.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
		accs.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
		accs.EXPECT().Get(gomock.Any(), "a").Return(brokerage.Account{ID: "a", SyncEnabled: true}, nil)
		acts.EXPECT().UpsertBatch(gomock.Any(), "a", gomock.Any()).Return(nil)
		accs.EXPECT().UpdateActivitySyncProgress(gomock.Any(), "a", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, progress repository.ActivitySyncProgress) error {
				Expect(progress.CompletedAt).NotTo(BeNil())
				return nil
			},
		)
		s := build(&pagedClient{snap: snap, pages: []domainsync.ActivityPage{{
			AccountID: "a", Items: []brokerage.Activity{{ID: "activity"}}, Complete: true,
		}}})
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

type notifyingClient struct {
	domainsync.BrokerClient
	started chan struct{}
	once    sync.Once
}

func (c *notifyingClient) Fetch(ctx context.Context) (domainsync.BrokerSnapshot, error) {
	c.once.Do(func() { close(c.started) })
	return c.BrokerClient.Fetch(ctx)
}

var _ = Describe("AsBrokerClient", func() {
	It("annotates a constructor", func() {
		opt := appsync.AsBrokerClient(func() domainsync.BrokerClient { return &fakeClient{id: "x"} })
		Expect(opt).NotTo(BeNil())
	})
})
