package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	appauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/auth"
	domainauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth"
	authmocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/auth/mocks"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository"
	repomocks "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/domain/repository/mocks"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

func TestAppAuth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Application Auth Suite")
}

var _ = Describe("auth.Service.Refresh", func() {
	var (
		ctrl   *gomock.Controller
		signer *authmocks.MockSigner
		refr   *authmocks.MockRefreshTokens
		tokens *repomocks.MockTokenRepository
		svc    *appauth.Service
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		signer = authmocks.NewMockSigner(ctrl)
		refr = authmocks.NewMockRefreshTokens(ctrl)
		tokens = repomocks.NewMockTokenRepository(ctrl)
		svc = appauth.NewService(signer, refr, tokens, &config.Config{TokenTTL: time.Hour, ConnectAuthPublishableKey: "pk"})
	})

	AfterEach(func() { ctrl.Finish() })

	It("issues a token pair on success", func() {
		ctx := context.Background()
		refr.EXPECT().Validate(ctx, "rt").Return("alice", nil)
		signer.EXPECT().Sign(ctx, gomock.Any()).Return("jwt", nil)
		refr.EXPECT().Issue(ctx, "alice").Return("rt2", nil)
		tokens.EXPECT().Insert(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, m repository.TokenMetadata) error {
			Expect(m.Subject).To(Equal("alice"))
			Expect(m.TokenID).NotTo(BeEmpty())
			return nil
		})
		pair, err := svc.Refresh(ctx, "rt")
		Expect(err).NotTo(HaveOccurred())
		Expect(pair.AccessToken).To(Equal("jwt"))
		Expect(pair.RefreshToken).To(Equal("rt2"))
		Expect(pair.ExpiresIn).To(Equal(3600))
	})

	It("propagates ErrInvalidRefreshToken", func() {
		refr.EXPECT().Validate(gomock.Any(), "bad").Return("", domainauth.ErrInvalidRefreshToken)
		_, err := svc.Refresh(context.Background(), "bad")
		Expect(err).To(MatchError(domainauth.ErrInvalidRefreshToken))
	})

	It("propagates signing errors", func() {
		refr.EXPECT().Validate(gomock.Any(), "rt").Return("u", nil)
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("", errors.New("sign fail"))
		_, err := svc.Refresh(context.Background(), "rt")
		Expect(err).To(MatchError(ContainSubstring("sign fail")))
	})

	It("propagates refresh-issue errors", func() {
		refr.EXPECT().Validate(gomock.Any(), "rt").Return("u", nil)
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("jwt", nil)
		refr.EXPECT().Issue(gomock.Any(), "u").Return("", errors.New("issue fail"))
		_, err := svc.Refresh(context.Background(), "rt")
		Expect(err).To(MatchError(ContainSubstring("issue fail")))
	})

	It("propagates persistence errors", func() {
		refr.EXPECT().Validate(gomock.Any(), "rt").Return("u", nil)
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("jwt", nil)
		refr.EXPECT().Issue(gomock.Any(), "u").Return("rt2", nil)
		tokens.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(errors.New("db fail"))
		_, err := svc.Refresh(context.Background(), "rt")
		Expect(err).To(MatchError(ContainSubstring("db fail")))
	})

	It("falls back to 1h TTL when configured TTL is zero", func() {
		svc = appauth.NewService(signer, refr, tokens, &config.Config{})
		refr.EXPECT().Validate(gomock.Any(), gomock.Any()).Return("u", nil)
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("jwt", nil)
		refr.EXPECT().Issue(gomock.Any(), gomock.Any()).Return("rt", nil)
		tokens.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
		pair, err := svc.Refresh(context.Background(), "rt")
		Expect(err).NotTo(HaveOccurred())
		Expect(pair.ExpiresIn).To(Equal(3600))
	})
})

var _ = Describe("auth.Service.VerifyAccessToken", func() {
	var (
		ctrl   *gomock.Controller
		signer *authmocks.MockSigner
		refr   *authmocks.MockRefreshTokens
		tokens *repomocks.MockTokenRepository
		svc    *appauth.Service
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		signer = authmocks.NewMockSigner(ctrl)
		refr = authmocks.NewMockRefreshTokens(ctrl)
		tokens = repomocks.NewMockTokenRepository(ctrl)
		svc = appauth.NewService(signer, refr, tokens, &config.Config{TokenTTL: time.Hour})
	})

	AfterEach(func() { ctrl.Finish() })

	It("rejects empty tokens", func() {
		_, err := svc.VerifyAccessToken(context.Background(), "")
		Expect(err).To(MatchError(domainauth.ErrInvalidToken))
	})

	It("propagates verification errors", func() {
		signer.EXPECT().Verify(gomock.Any(), "x").Return(domainauth.Claims{}, domainauth.ErrInvalidToken)
		_, err := svc.VerifyAccessToken(context.Background(), "x")
		Expect(err).To(MatchError(domainauth.ErrInvalidToken))
	})

	It("rejects expired claims", func() {
		signer.EXPECT().Verify(gomock.Any(), "x").Return(domainauth.Claims{
			Subject:   "u",
			ExpiresAt: time.Now().Add(-time.Minute),
		}, nil)
		_, err := svc.VerifyAccessToken(context.Background(), "x")
		Expect(err).To(MatchError(domainauth.ErrTokenExpired))
	})

	It("returns valid claims", func() {
		signer.EXPECT().Verify(gomock.Any(), "x").Return(domainauth.Claims{
			Subject:   "u",
			ExpiresAt: time.Now().Add(time.Hour),
		}, nil)
		c, err := svc.VerifyAccessToken(context.Background(), "x")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Subject).To(Equal("u"))
	})
})

var _ = Describe("auth.Service.VerifyAPIKey", func() {
	It("accepts a matching key and rejects others", func() {
		svc := appauth.NewService(nil, nil, nil, &config.Config{ConnectAuthPublishableKey: "pk"})
		Expect(svc.VerifyAPIKey("pk")).To(Succeed())
		Expect(svc.VerifyAPIKey("")).To(MatchError(ContainSubstring("invalid apikey")))
		Expect(svc.VerifyAPIKey("nope")).To(MatchError(ContainSubstring("invalid apikey")))
	})
})

var _ = Describe("auth.Service.IsAllowedEmail", func() {
	It("matches case-insensitively across the configured allow-list", func() {
		svc := appauth.NewService(nil, nil, nil, &config.Config{
			SelfHostedUserEmails: []string{"alice@example.com", "bob@example.com"},
		})
		Expect(svc.IsAllowedEmail("alice@example.com")).To(BeTrue())
		Expect(svc.IsAllowedEmail("  BOB@Example.com  ")).To(BeTrue())
		Expect(svc.IsAllowedEmail("eve@example.com")).To(BeFalse())
		Expect(svc.IsAllowedEmail("")).To(BeFalse())
	})

	It("rejects everything when the allow-list is empty", func() {
		svc := appauth.NewService(nil, nil, nil, &config.Config{})
		Expect(svc.IsAllowedEmail("anyone@example.com")).To(BeFalse())
	})
})

var _ = Describe("auth.Service.VerifyOTP", func() {
	It("accepts any 6-digit numeric token", func() {
		svc := appauth.NewService(nil, nil, nil, &config.Config{})
		Expect(svc.VerifyOTP("123456")).To(Succeed())
		Expect(svc.VerifyOTP("000000")).To(Succeed())
	})

	It("rejects non-numeric or wrong-length tokens when STATIC_OTP is unset", func() {
		svc := appauth.NewService(nil, nil, nil, &config.Config{})
		Expect(svc.VerifyOTP("12345")).To(MatchError(appauth.ErrInvalidOTP))
		Expect(svc.VerifyOTP("1234567")).To(MatchError(appauth.ErrInvalidOTP))
		Expect(svc.VerifyOTP("abcdef")).To(MatchError(appauth.ErrInvalidOTP))
		Expect(svc.VerifyOTP("")).To(MatchError(appauth.ErrInvalidOTP))
	})

	It("also accepts an exact STATIC_OTP match", func() {
		svc := appauth.NewService(nil, nil, nil, &config.Config{StaticOTP: "letmein"})
		Expect(svc.VerifyOTP("letmein")).To(Succeed())
		Expect(svc.VerifyOTP("LETMEIN")).To(MatchError(appauth.ErrInvalidOTP))
	})
})

var _ = Describe("auth.SubjectFromEmail", func() {
	It("returns a stable UUID-shaped subject normalised case-insensitively", func() {
		a := appauth.SubjectFromEmail("Alice@Example.com")
		b := appauth.SubjectFromEmail("  alice@example.com ")
		Expect(a).To(Equal(b))
		Expect(a).To(MatchRegexp(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`))
		Expect(appauth.SubjectFromEmail("bob@example.com")).NotTo(Equal(a))
	})
})

var _ = Describe("auth.Service.IssueSession", func() {
	var (
		ctrl   *gomock.Controller
		signer *authmocks.MockSigner
		refr   *authmocks.MockRefreshTokens
		tokens *repomocks.MockTokenRepository
		svc    *appauth.Service
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		signer = authmocks.NewMockSigner(ctrl)
		refr = authmocks.NewMockRefreshTokens(ctrl)
		tokens = repomocks.NewMockTokenRepository(ctrl)
		svc = appauth.NewService(signer, refr, tokens, &config.Config{
			TokenTTL:             time.Hour,
			SelfHostedUserEmails: []string{"alice@example.com"},
		})
	})

	AfterEach(func() { ctrl.Finish() })

	It("rejects emails that are not on the allow-list", func() {
		_, err := svc.IssueSession(context.Background(), "eve@example.com")
		Expect(err).To(MatchError(appauth.ErrEmailNotAllowed))
	})

	It("issues a token pair for an allow-listed email", func() {
		expectedSubject := appauth.SubjectFromEmail("alice@example.com")
		ctx := context.Background()
		signer.EXPECT().Sign(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, c domainauth.Claims) (string, error) {
			Expect(c.Subject).To(Equal(expectedSubject))
			Expect(c.TokenID).NotTo(BeEmpty())
			Expect(c.ExpiresAt.Sub(c.IssuedAt)).To(Equal(time.Hour))
			return "jwt", nil
		})
		refr.EXPECT().Issue(ctx, expectedSubject).Return("rt", nil)
		tokens.EXPECT().Insert(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, m repository.TokenMetadata) error {
			Expect(m.Subject).To(Equal(expectedSubject))
			Expect(m.TokenID).NotTo(BeEmpty())
			return nil
		})
		pair, err := svc.IssueSession(ctx, "Alice@Example.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(pair.AccessToken).To(Equal("jwt"))
		Expect(pair.RefreshToken).To(Equal("rt"))
		Expect(pair.ExpiresIn).To(Equal(3600))
	})

	It("falls back to 1h TTL when configured TTL is zero", func() {
		svc = appauth.NewService(signer, refr, tokens, &config.Config{
			SelfHostedUserEmails: []string{"alice@example.com"},
		})
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("jwt", nil)
		refr.EXPECT().Issue(gomock.Any(), gomock.Any()).Return("rt", nil)
		tokens.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
		pair, err := svc.IssueSession(context.Background(), "alice@example.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(pair.ExpiresIn).To(Equal(3600))
	})

	It("propagates signing errors", func() {
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("", errors.New("sign fail"))
		_, err := svc.IssueSession(context.Background(), "alice@example.com")
		Expect(err).To(MatchError(ContainSubstring("sign fail")))
	})

	It("propagates refresh-issue errors", func() {
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("jwt", nil)
		refr.EXPECT().Issue(gomock.Any(), gomock.Any()).Return("", errors.New("issue fail"))
		_, err := svc.IssueSession(context.Background(), "alice@example.com")
		Expect(err).To(MatchError(ContainSubstring("issue fail")))
	})

	It("propagates persistence errors", func() {
		signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return("jwt", nil)
		refr.EXPECT().Issue(gomock.Any(), gomock.Any()).Return("rt", nil)
		tokens.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(errors.New("db fail"))
		_, err := svc.IssueSession(context.Background(), "alice@example.com")
		Expect(err).To(MatchError(ContainSubstring("db fail")))
	})
})
