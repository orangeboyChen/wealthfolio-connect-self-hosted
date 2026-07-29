package snaptrade

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

type sleeper func(context.Context, time.Duration) error

func contextSleep(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type quotaState struct {
	limit     int
	remaining int
	resetAt   time.Time
}

type rateLimiter struct {
	mu              sync.Mutex
	clock           clock
	sleep           sleeper
	minimumInterval time.Duration
	safetyPercent   int
	globalLimit     int
	accountLimit    int
	lastRequest     time.Time
	globalRequests  []time.Time
	accountRequests map[string][]time.Time
	globalServer    quotaState
	accountServer   map[string]quotaState
}

func newRateLimiter(cfg config.SnapTradeConfig, clk clock, sleep sleeper) *rateLimiter {
	globalLimit := 250
	if cfg.RequestsPerMinute > 0 && cfg.RequestsPerMinute < globalLimit {
		globalLimit = cfg.RequestsPerMinute
	}
	accountLimit := 0
	if cfg.AuthMode == authModePersonal {
		accountLimit = 10
	}
	if cfg.AccountRequestsPM > 0 && (accountLimit == 0 || cfg.AccountRequestsPM < accountLimit) {
		accountLimit = cfg.AccountRequestsPM
	}
	return &rateLimiter{
		clock: clk, sleep: sleep, minimumInterval: cfg.RequestInterval,
		safetyPercent: cfg.SafetyPercent, globalLimit: globalLimit, accountLimit: accountLimit,
		accountRequests: make(map[string][]time.Time), accountServer: make(map[string]quotaState),
	}
}

func (l *rateLimiter) wait(ctx context.Context, accountID string) error {
	for {
		l.mu.Lock()
		now := l.clock.Now()
		l.globalRequests = pruneWindow(l.globalRequests, now)
		if accountID != "" {
			l.accountRequests[accountID] = pruneWindow(l.accountRequests[accountID], now)
		}

		wait := time.Duration(0)
		if !l.lastRequest.IsZero() {
			wait = maxDuration(wait, l.minimumInterval-now.Sub(l.lastRequest))
		}
		globalCapacity := l.effectiveCapacity(l.globalLimit, l.globalServer.limit)
		wait = maxDuration(wait, localWindowWait(l.globalRequests, globalCapacity, now))
		wait = maxDuration(wait, serverQuotaWait(l.globalServer, l.safetyPercent, now))

		if accountID != "" {
			accountState := l.accountServer[accountID]
			accountCapacity := l.effectiveCapacity(l.accountLimit, accountState.limit)
			if accountCapacity > 0 {
				wait = maxDuration(wait, localWindowWait(l.accountRequests[accountID], accountCapacity, now))
			}
			wait = maxDuration(wait, serverQuotaWait(accountState, l.safetyPercent, now))
		}
		if wait <= 0 {
			l.lastRequest = now
			l.globalRequests = append(l.globalRequests, now)
			if accountID != "" && (l.accountLimit > 0 || l.accountServer[accountID].limit > 0) {
				l.accountRequests[accountID] = append(l.accountRequests[accountID], now)
			}
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()
		if err := l.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (l *rateLimiter) effectiveCapacity(local, server int) int {
	limit := local
	if server > 0 && (limit == 0 || server < limit) {
		limit = server
	}
	if limit <= 0 {
		return 0
	}
	return safeCapacity(limit, l.safetyPercent)
}

func safeCapacity(limit, safetyPercent int) int {
	capacity := int(math.Floor(float64(limit*safetyPercent) / 100.0))
	if capacity < 1 {
		return 1
	}
	return capacity
}

func localWindowWait(requests []time.Time, capacity int, now time.Time) time.Duration {
	if capacity <= 0 || len(requests) < capacity {
		return 0
	}
	return requests[len(requests)-capacity].Add(time.Minute).Sub(now)
}

func serverQuotaWait(state quotaState, safetyPercent int, now time.Time) time.Duration {
	if state.limit <= 0 || state.resetAt.IsZero() || !state.resetAt.After(now) {
		return 0
	}
	reserve := state.limit - safeCapacity(state.limit, safetyPercent)
	if state.remaining > reserve {
		return 0
	}
	return state.resetAt.Sub(now)
}

func pruneWindow(in []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-time.Minute)
	first := 0
	for first < len(in) && !in[first].After(cutoff) {
		first++
	}
	if first == 0 {
		return in
	}
	return append([]time.Time(nil), in[first:]...)
}

func (l *rateLimiter) observe(headers http.Header, accountID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if state, ok := quotaFromHeaders(headers, "X-RateLimit-", now); ok {
		l.globalServer = state
	}
	if accountID != "" {
		if state, ok := quotaFromHeaders(headers, "X-RateLimit-Account-", now); ok {
			l.accountServer[accountID] = state
		}
	}
}

func quotaFromHeaders(headers http.Header, prefix string, now time.Time) (quotaState, bool) {
	limit, limitOK := parseHeaderInt(headers.Get(prefix + "Limit"))
	remaining, remainingOK := parseHeaderInt(headers.Get(prefix + "Remaining"))
	resetSeconds, resetOK := parseHeaderInt(headers.Get(prefix + "Reset"))
	if !limitOK && !remainingOK && !resetOK {
		return quotaState{}, false
	}
	state := quotaState{limit: limit, remaining: remaining}
	if resetOK && resetSeconds > 0 {
		state.resetAt = now.Add(time.Duration(resetSeconds) * time.Second)
	}
	return state, true
}

func parseHeaderInt(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

func maxDuration(a, b time.Duration) time.Duration {
	if b > a {
		return b
	}
	return a
}
