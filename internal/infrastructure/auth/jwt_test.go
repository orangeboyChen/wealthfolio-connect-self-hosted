package auth_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	domainauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/auth"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

func TestInfraAuth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Infra Auth Suite")
}

var _ = Describe("JWTSigner", func() {
	var signer *auth.JWTSigner

	BeforeEach(func() {
		signer = auth.NewJWTSigner(&config.Config{JWTSecret: "shh", TokenTTL: time.Hour})
	})

	It("signs and verifies a JWT", func() {
		raw, err := signer.Sign(context.Background(), domainauth.Claims{Subject: "u", TokenID: "t"})
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).NotTo(BeEmpty())
		c, err := signer.Verify(context.Background(), raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Subject).To(Equal("u"))
		Expect(c.TokenID).To(Equal("t"))
	})

	It("uses defaults when no claims dates are provided", func() {
		raw, err := signer.Sign(context.Background(), domainauth.Claims{Subject: "x"})
		Expect(err).NotTo(HaveOccurred())
		c, err := signer.Verify(context.Background(), raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.ExpiresAt).To(BeTemporally(">", time.Now()))
	})

	It("rejects an unparseable token", func() {
		_, err := signer.Verify(context.Background(), "not-a-jwt")
		Expect(err).To(MatchError(domainauth.ErrInvalidToken))
	})

	It("rejects a token signed with a different secret", func() {
		other := auth.NewJWTSigner(&config.Config{JWTSecret: "different", TokenTTL: time.Hour})
		raw, err := other.Sign(context.Background(), domainauth.Claims{Subject: "u"})
		Expect(err).NotTo(HaveOccurred())
		_, err = signer.Verify(context.Background(), raw)
		Expect(err).To(MatchError(domainauth.ErrInvalidToken))
	})

	It("falls back to a 1h TTL when configured TTL is zero", func() {
		s := auth.NewJWTSigner(&config.Config{JWTSecret: "k"})
		raw, err := s.Sign(context.Background(), domainauth.Claims{Subject: "x"})
		Expect(err).NotTo(HaveOccurred())
		c, err := s.Verify(context.Background(), raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.ExpiresAt.Sub(c.IssuedAt)).To(BeNumerically("~", time.Hour, time.Second))
	})
})

var _ = Describe("RefreshStore", func() {
	It("rejects empty tokens", func() {
		s := auth.NewRefreshTokens(&config.Config{})
		_, err := s.Validate(context.Background(), "")
		Expect(err).To(MatchError(domainauth.ErrInvalidRefreshToken))
	})

	It("issues and validates fresh signed tokens", func() {
		s := auth.NewRefreshTokens(&config.Config{JWTSecret: "persistent-secret"})
		token, err := s.Issue(context.Background(), "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(token).NotTo(BeEmpty())
		subject, err := s.Validate(context.Background(), token)
		Expect(err).NotTo(HaveOccurred())
		Expect(subject).To(Equal("alice"))
	})

	It("survives a process restart when the JWT secret is unchanged", func() {
		cfg := &config.Config{JWTSecret: "persistent-secret"}
		beforeRestart := auth.NewRefreshTokens(cfg)
		token, err := beforeRestart.Issue(context.Background(), "alice")
		Expect(err).NotTo(HaveOccurred())

		afterRestart := auth.NewRefreshTokens(cfg)
		subject, err := afterRestart.Validate(context.Background(), token)
		Expect(err).NotTo(HaveOccurred())
		Expect(subject).To(Equal("alice"))
	})

	It("rejects malformed tokens and tokens signed with another secret", func() {
		s := auth.NewRefreshTokens(&config.Config{JWTSecret: "expected-secret"})
		_, err := s.Validate(context.Background(), "unknown")
		Expect(err).To(MatchError(domainauth.ErrInvalidRefreshToken))

		other := auth.NewRefreshTokens(&config.Config{JWTSecret: "other-secret"})
		token, issueErr := other.Issue(context.Background(), "alice")
		Expect(issueErr).NotTo(HaveOccurred())
		_, err = s.Validate(context.Background(), token)
		Expect(err).To(MatchError(domainauth.ErrInvalidRefreshToken))
	})

	It("does not accept an access JWT as a refresh token", func() {
		cfg := &config.Config{JWTSecret: "shared-secret", TokenTTL: time.Hour}
		signer := auth.NewJWTSigner(cfg)
		accessToken, err := signer.Sign(context.Background(), domainauth.Claims{Subject: "alice"})
		Expect(err).NotTo(HaveOccurred())

		refreshes := auth.NewRefreshTokens(cfg)
		_, err = refreshes.Validate(context.Background(), accessToken)
		Expect(err).To(MatchError(domainauth.ErrInvalidRefreshToken))
	})

	It("does not accept a refresh token as an access JWT", func() {
		cfg := &config.Config{JWTSecret: "shared-secret", TokenTTL: time.Hour}
		refreshes := auth.NewRefreshTokens(cfg)
		refreshToken, err := refreshes.Issue(context.Background(), "alice")
		Expect(err).NotTo(HaveOccurred())

		signer := auth.NewJWTSigner(cfg)
		_, err = signer.Verify(context.Background(), refreshToken)
		Expect(err).To(MatchError(domainauth.ErrInvalidToken))
	})

	It("rejects an empty subject when issuing", func() {
		s := auth.NewRefreshTokens(&config.Config{JWTSecret: "persistent-secret"})
		_, err := s.Issue(context.Background(), " ")
		Expect(err).To(HaveOccurred())
	})

	It("accepts any non-empty token in static mode and returns the fixed value on Issue", func() {
		s := auth.NewRefreshTokens(&config.Config{StaticTokenMode: true})
		subject, err := s.Validate(context.Background(), "anything")
		Expect(err).NotTo(HaveOccurred())
		Expect(subject).To(Equal("self-hosted-user"))
		token, err := s.Issue(context.Background(), "ignored")
		Expect(err).NotTo(HaveOccurred())
		Expect(token).To(Equal("static-refresh-token"))
	})
})
