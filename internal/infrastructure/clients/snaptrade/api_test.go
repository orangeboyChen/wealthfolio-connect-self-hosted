package snaptrade

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type testNetError struct {
	timeout   bool
	temporary bool
}

func (e testNetError) Error() string   { return "network failure" }
func (e testNetError) Timeout() bool   { return e.timeout }
func (e testNetError) Temporary() bool { return e.temporary }

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func response(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

var _ = Describe("SnapTrade HTTP behavior", func() {
	It("validates and decodes successful responses", func() {
		var decoded map[string]string
		Expect(decodeSuccess(response(http.StatusOK, "application/problem+json", `{"answer":"yes"}`), "test", &decoded)).To(Succeed())
		Expect(decoded).To(HaveKeyWithValue("answer", "yes"))
		Expect(decodeSuccess(response(http.StatusNoContent, "", "ignored"), "test", &decoded)).To(Succeed())
		Expect(decodeSuccess(response(http.StatusOK, "text/html", "not json"), "test", &decoded)).To(MatchError(ContainSubstring("content type")))
		Expect(decodeSuccess(response(http.StatusOK, "application/json", "{"), "test", &decoded)).To(MatchError(ContainSubstring("decode response")))
	})

	It("parses bounded error details and redacts raw fallback bodies", func() {
		cfg := testConfig("https://example.test")
		detail, err := readErrorDetail(response(http.StatusBadRequest, "text/plain", "consumer secret consumer"), cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(detail).To(Equal("[redacted] secret [redacted]"))
	})

	DescribeTable("classifies response statuses",
		func(status int, expected bool) { Expect(retryableStatus(status)).To(Equal(expected)) },
		Entry("timeout", http.StatusRequestTimeout, true),
		Entry("conflict", http.StatusConflict, true),
		Entry("throttle", http.StatusTooManyRequests, true),
		Entry("server error", http.StatusBadGateway, true),
		Entry("bad request", http.StatusBadRequest, false),
	)

	It("classifies transport failures without retrying permanent network errors", func() {
		Expect(retryableTransport(errors.New("ordinary transport error"))).To(BeTrue())
		Expect(retryableTransport(testNetError{timeout: true})).To(BeTrue())
		Expect(retryableTransport(testNetError{temporary: true})).To(BeTrue())
		Expect(retryableTransport(context.Canceled)).To(BeFalse())
	})

	It("takes the longest retry hint from HTTP-date, reset headers, and detail", func() {
		now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		headers := http.Header{}
		headers.Set("Retry-After", now.Add(7*time.Second).Format(http.TimeFormat))
		headers.Set("X-RateLimit-Reset", "9")
		headers.Set("X-RateLimit-Account-Reset", "invalid")
		Expect(responseRetryDelay(headers, "Expected available in 11 seconds.", now)).To(Equal(11 * time.Second))
	})

	It("caps exponential backoff and bounds jitter", func() {
		cfg := testConfig("https://example.test")
		cfg.RetryBaseDelay = 3 * time.Second
		cfg.RetryMaxDelay = 10 * time.Second
		client, _, _ := testClient(cfg, http.DefaultClient)
		Expect(client.api.backoff(0)).To(Equal(3 * time.Second))
		Expect(client.api.backoff(1)).To(Equal(6 * time.Second))
		Expect(client.api.backoff(4)).To(Equal(10 * time.Second))
		Expect(boundedJitter(0)).To(BeZero())
		Expect(boundedJitter(30 * time.Second)).To(BeNumerically(">=", 0))
		Expect(boundedJitter(30 * time.Second)).To(BeNumerically("<=", time.Second))
	})

	It("formats API errors and masks long identifiers", func() {
		Expect((&APIError{StatusCode: http.StatusTeapot, Endpoint: "tea"}).Error()).To(Equal("snaptrade: tea returned HTTP 418"))
		Expect(maskIdentifier("short")).To(Equal("short"))
		Expect(maskIdentifier("1234567890")).To(Equal("1234…7890"))
	})

	It("returns promptly from context-aware sleeps", func() {
		Expect(contextSleep(context.Background(), 0)).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(contextSleep(ctx, time.Hour)).To(MatchError(context.Canceled))
	})

	It("retries transport failures and accepts empty successful POST responses", func() {
		attempts := 0
		doer := doerFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("connection reset")
			}
			return response(http.StatusNoContent, "", ""), nil
		})
		cfg := testConfig("https://example.test")
		cfg.MaxRetries = 1
		client, _, waits := testClient(cfg, doer)
		Expect(client.api.post(context.Background(), "/test", "test", "")).To(Succeed())
		Expect(attempts).To(Equal(2))
		Expect(*waits).To(ContainElement(time.Second))
	})

	It("stops immediately for cancellation and permanent network failures", func() {
		cfg := testConfig("https://example.test")
		cfg.MaxRetries = 2
		client, _, _ := testClient(cfg, doerFunc(func(*http.Request) (*http.Response, error) {
			return nil, context.Canceled
		}))
		Expect(client.api.get(context.Background(), "/test", "test", "", nil, nil)).To(MatchError(ContainSubstring("transport")))

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client, _, _ = testClient(cfg, doerFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("request stopped")
		}))
		Expect(client.api.get(ctx, "/test", "test", "", nil, nil)).To(MatchError(ContainSubstring("request canceled")))
	})

	It("reports signing failures before issuing a request", func() {
		client, _, _ := testClient(testConfig("https://example.test"), doerFunc(func(*http.Request) (*http.Response, error) {
			Fail("request should not be issued")
			return nil, nil
		}))
		err := client.api.doJSON(context.Background(), http.MethodPost, "/test", "test", "", nil, func() {}, nil)
		Expect(err).To(MatchError(ContainSubstring("signing")))
	})
})
