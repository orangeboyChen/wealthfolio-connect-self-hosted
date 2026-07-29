package snaptrade

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

const (
	maxErrorBody   = 64 << 10
	maxSuccessBody = 32 << 20
	userAgent      = "wealthfolio-connect-self-hosted/snaptrade"
)

var expectedDelayPattern = regexp.MustCompile(`(?i)expected available in\s+(\d+)\s+seconds?`)

// HTTPDoer is the portion of http.Client used by the SnapTrade REST client.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type apiClient struct {
	baseURL *url.URL
	doer    HTTPDoer
	signer  signer
	limiter *rateLimiter
	config  config.SnapTradeConfig
	log     zerolog.Logger
	sleep   sleeper
	jitter  func(time.Duration) time.Duration
}

func (a *apiClient) get(ctx context.Context, endpoint, category, accountID string, query url.Values, out any) error {
	return a.doJSON(ctx, http.MethodGet, endpoint, category, accountID, query, nil, out)
}

func (a *apiClient) post(ctx context.Context, endpoint, category, accountID string) error {
	return a.doJSON(ctx, http.MethodPost, endpoint, category, accountID, nil, nil, nil)
}

func (a *apiClient) doJSON(
	ctx context.Context,
	method, endpoint, category, accountID string,
	query url.Values,
	body any,
	out any,
) error {
	if query == nil {
		query = make(url.Values)
	}
	var lastErr error
	for attempt := 0; attempt <= a.config.MaxRetries; attempt++ {
		if err := a.limiter.wait(ctx, accountID); err != nil {
			return fmt.Errorf("snaptrade %s rate-limit wait: %w", category, err)
		}
		rawQuery, signature, err := a.signer.sign(endpoint, query, body)
		if err != nil {
			return fmt.Errorf("snaptrade %s signing: %w", category, err)
		}
		requestURL := *a.baseURL
		requestURL.Path = strings.TrimRight(a.baseURL.Path, "/") + endpoint
		requestURL.RawQuery = rawQuery
		req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), http.NoBody)
		if err != nil {
			return fmt.Errorf("snaptrade %s build request: %w", category, err)
		}
		req.Header.Set("Signature", signature)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)

		resp, err := a.doer.Do(req)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if ctx.Err() != nil {
				return fmt.Errorf("snaptrade %s request canceled: %w", category, ctx.Err())
			}
			lastErr = fmt.Errorf("snaptrade %s transport: %w", category, err)
			if !retryableTransport(err) {
				return lastErr
			}
			if attempt == a.config.MaxRetries {
				return fmt.Errorf("snaptrade %s failed after %d attempts: %w", category, attempt+1, lastErr)
			}
			if waitErr := a.retryWait(ctx, category, accountID, attempt, a.backoff(attempt)); waitErr != nil {
				return waitErr
			}
			continue
		}

		a.limiter.observe(resp.Header, accountID)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			err = decodeSuccess(resp, category, out)
			if err != nil {
				return err
			}
			return nil
		}

		detail, readErr := readErrorDetail(resp, a.config)
		if readErr != nil {
			detail = "unable to read bounded error response"
		}
		apiErr := &APIError{StatusCode: resp.StatusCode, Endpoint: category, Detail: detail}
		lastErr = apiErr
		if !retryableStatus(resp.StatusCode) {
			return apiErr
		}
		if attempt == a.config.MaxRetries {
			return fmt.Errorf("snaptrade %s failed after %d attempts: %w", category, attempt+1, apiErr)
		}
		delay := a.backoff(attempt)
		if resp.StatusCode == http.StatusTooManyRequests {
			delay = maxDuration(delay, responseRetryDelay(resp.Header, detail, a.signer.clock.Now()))
		}
		if err := a.retryWait(ctx, category, accountID, attempt, delay); err != nil {
			return err
		}
	}
	return fmt.Errorf("snaptrade %s retries exhausted: %w", category, lastErr)
}

func decodeSuccess(resp *http.Response, category string, out any) error {
	defer resp.Body.Close()
	if out == nil || resp.StatusCode == http.StatusNoContent {
		if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody)); err != nil {
			return fmt.Errorf("snaptrade %s drain response: %w", category, err)
		}
		return nil
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "application/json") && !strings.Contains(contentType, "+json") {
		return fmt.Errorf("snaptrade %s returned unexpected content type", category)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxSuccessBody))
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("snaptrade %s decode response: %w", category, err)
	}
	return nil
}

func readErrorDetail(resp *http.Response, cfg config.SnapTradeConfig) (string, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return "", err
	}
	var envelope struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Detail != "" {
		return redact(envelope.Detail, cfg), nil
	}
	return redact(strings.TrimSpace(string(body)), cfg), nil
}

func redact(value string, cfg config.SnapTradeConfig) string {
	for _, secret := range []string{cfg.ConsumerKey, cfg.UserSecret, cfg.UserID, cfg.ClientID} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "clientid=") || strings.Contains(lower, "usersecret=") ||
		strings.Contains(lower, "signature=") {
		return "[redacted SnapTrade error detail]"
	}
	return value
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusConflict ||
		status == http.StatusTooManyRequests || status >= 500
}

func retryableTransport(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return !errors.Is(err, context.Canceled)
}

func (a *apiClient) backoff(attempt int) time.Duration {
	delay := a.config.RetryBaseDelay
	for i := 0; i < attempt && delay < a.config.RetryMaxDelay; i++ {
		if delay > a.config.RetryMaxDelay/2 {
			delay = a.config.RetryMaxDelay
			break
		}
		delay *= 2
	}
	if delay > a.config.RetryMaxDelay {
		return a.config.RetryMaxDelay
	}
	return delay
}

func (a *apiClient) retryWait(ctx context.Context, category, accountID string, attempt int, delay time.Duration) error {
	delay += a.jitter(delay)
	a.log.Warn().
		Str("endpoint", category).
		Str("account", maskIdentifier(accountID)).
		Int("retry_attempt", attempt+1).
		Dur("wait", delay).
		Msg("retrying SnapTrade request")
	if err := a.sleep(ctx, delay); err != nil {
		return fmt.Errorf("snaptrade %s retry wait: %w", category, err)
	}
	return nil
}

func responseRetryDelay(headers http.Header, detail string, now time.Time) time.Duration {
	delay := time.Duration(0)
	if raw := headers.Get("Retry-After"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			delay = time.Duration(seconds) * time.Second
		} else if when, err := http.ParseTime(raw); err == nil {
			delay = maxDuration(delay, when.Sub(now))
		}
	}
	for _, name := range []string{"X-RateLimit-Reset", "X-RateLimit-Account-Reset"} {
		if seconds, ok := parseHeaderInt(headers.Get(name)); ok {
			delay = maxDuration(delay, time.Duration(seconds)*time.Second)
		}
	}
	if match := expectedDelayPattern.FindStringSubmatch(detail); len(match) == 2 {
		if seconds, err := strconv.Atoi(match[1]); err == nil {
			delay = maxDuration(delay, time.Duration(seconds)*time.Second)
		}
	}
	return delay
}

func boundedJitter(delay time.Duration) time.Duration {
	maximum := delay / 10
	if maximum > time.Second {
		maximum = time.Second
	}
	if maximum <= 0 {
		return 0
	}
	var data [8]byte
	if _, err := cryptorand.Read(data[:]); err != nil {
		return 0
	}
	return time.Duration(binary.LittleEndian.Uint64(data[:]) % uint64(maximum+1))
}

func maskIdentifier(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:4] + "…" + id[len(id)-4:]
}
