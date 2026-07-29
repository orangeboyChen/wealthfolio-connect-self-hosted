package snaptrade

import (
	"context"
	"net/http"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

func advancingSleeper(clk *fakeClock, waits *[]time.Duration, mu *sync.Mutex) sleeper {
	return func(ctx context.Context, duration time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		mu.Lock()
		*waits = append(*waits, duration)
		mu.Unlock()
		clk.advance(duration)
		return nil
	}
}

var _ = Describe("rateLimiter", func() {
	var (
		clk   *fakeClock
		waits []time.Duration
		mu    sync.Mutex
		cfg   config.SnapTradeConfig
	)

	BeforeEach(func() {
		clk = &fakeClock{now: time.Unix(1700000000, 0)}
		waits = nil
		cfg = config.SnapTradeConfig{AuthMode: "personal", RequestInterval: time.Minute, SafetyPercent: 80}
	})

	It("enforces the conservative one-request-per-minute default without real sleeping", func() {
		limiter := newRateLimiter(cfg, clk, advancingSleeper(clk, &waits, &mu))
		Expect(limiter.wait(context.Background(), "account-a")).To(Succeed())
		Expect(limiter.wait(context.Background(), "account-a")).To(Succeed())
		Expect(waits).To(ContainElement(time.Minute))
	})

	It("preserves the server-reported safety reserve for global and account quotas", func() {
		cfg.RequestInterval = 0
		limiter := newRateLimiter(cfg, clk, advancingSleeper(clk, &waits, &mu))
		Expect(limiter.wait(context.Background(), "account-a")).To(Succeed())
		headers := http.Header{
			"X-Ratelimit-Limit":             {"10"},
			"X-Ratelimit-Remaining":         {"2"},
			"X-Ratelimit-Reset":             {"30"},
			"X-Ratelimit-Account-Limit":     {"5"},
			"X-Ratelimit-Account-Remaining": {"1"},
			"X-Ratelimit-Account-Reset":     {"45"},
		}
		limiter.observe(headers, "account-a")
		Expect(limiter.wait(context.Background(), "account-a")).To(Succeed())
		Expect(waits).To(ContainElement(45 * time.Second))
	})

	It("keeps account quotas independent", func() {
		cfg.RequestInterval = 0
		cfg.AccountRequestsPM = 1
		limiter := newRateLimiter(cfg, clk, advancingSleeper(clk, &waits, &mu))
		Expect(limiter.wait(context.Background(), "account-a")).To(Succeed())
		Expect(limiter.wait(context.Background(), "account-b")).To(Succeed())
		Expect(waits).To(BeEmpty())
		Expect(limiter.wait(context.Background(), "account-a")).To(Succeed())
		Expect(waits).To(ContainElement(time.Minute))
	})

	It("respects cancellation during a wait", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		limiter := newRateLimiter(cfg, clk, advancingSleeper(clk, &waits, &mu))
		Expect(limiter.wait(context.Background(), "account-a")).To(Succeed())
		Expect(limiter.wait(ctx, "account-a")).To(MatchError(context.Canceled))
	})

	It("serializes concurrent callers against the configured interval", func() {
		cfg.RequestInterval = time.Second
		limiter := newRateLimiter(cfg, clk, advancingSleeper(clk, &waits, &mu))
		var wg sync.WaitGroup
		errs := make(chan error, 5)
		for range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- limiter.wait(context.Background(), "account-a")
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(clk.Now().Sub(time.Unix(1700000000, 0))).To(BeNumerically(">=", 4*time.Second))
	})

	It("parses quota headers defensively and prunes expired windows", func() {
		now := clk.Now()
		state, ok := quotaFromHeaders(http.Header{
			"X-Test-Limit":     []string{"20"},
			"X-Test-Remaining": []string{"5"},
			"X-Test-Reset":     []string{"30"},
		}, "X-Test-", now)
		Expect(ok).To(BeTrue())
		Expect(state.limit).To(Equal(20))
		Expect(state.resetAt).To(Equal(now.Add(30 * time.Second)))
		_, ok = quotaFromHeaders(http.Header{}, "X-Test-", now)
		Expect(ok).To(BeFalse())
		_, ok = parseHeaderInt("-1")
		Expect(ok).To(BeFalse())
		_, ok = parseHeaderInt("not-a-number")
		Expect(ok).To(BeFalse())
		Expect(pruneWindow([]time.Time{now.Add(-2 * time.Minute), now.Add(-30 * time.Second)}, now)).To(Equal([]time.Time{now.Add(-30 * time.Second)}))
		Expect(safeCapacity(1, 1)).To(Equal(1))
	})
})
