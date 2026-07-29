package snaptrade

import (
	"net/url"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) advance(duration time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(duration)
	f.mu.Unlock()
}

var _ = Describe("request signing", func() {
	var clk *fakeClock

	BeforeEach(func() {
		clk = &fakeClock{now: time.Unix(1700000000, 0)}
	})

	It("creates the documented Personal request shape and deterministic HMAC", func() {
		s := signer{auth: config.SnapTradeConfig{
			AuthMode: "personal", ClientID: "client", ConsumerKey: "test-consumer",
		}, clock: clk}
		query, signature, err := s.sign("/api/v1/accounts", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(query).To(Equal("clientId=client&timestamp=1700000000"))
		Expect(query).NotTo(ContainSubstring("userId"))
		canonical, canonicalErr := canonicalSignaturePayload("/api/v1/accounts", query, nil)
		Expect(canonicalErr).NotTo(HaveOccurred())
		Expect(string(canonical)).To(Equal(`{"content":null,"path":"/api/v1/accounts","query":"clientId=client&timestamp=1700000000"}`))
		Expect(signature).To(Equal("AtOoePpmxJpSBZqPALde9hyymnbbMpnZg019g/LiSE8="))
	})

	It("signs Commercial credentials and endpoint query parameters in stable order", func() {
		s := signer{auth: config.SnapTradeConfig{
			AuthMode: "commercial", ClientID: "client", ConsumerKey: "test-consumer",
			UserID: "user", UserSecret: "secret",
		}, clock: clk}
		params := url.Values{"startDate": {"2022-01-01"}, "limit": {"1000"}}
		query, signature, err := s.sign("/api/v1/accounts/id/activities", params, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(query).To(Equal("clientId=client&limit=1000&startDate=2022-01-01&timestamp=1700000000&userId=user&userSecret=secret"))
		Expect(signature).To(Equal("S2aeMDaCzlJgaAMRuNn61Wwvo6RGdFWdzoNrIT90hoM="))
	})
})
