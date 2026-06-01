package logging_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/logging"
)

func TestLogging(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Logging Suite")
}

var _ = Describe("logging.New", func() {
	It("falls back to info level for empty config", func() {
		l := logging.New(&config.Config{})
		Expect(l.GetLevel()).To(Equal(zerolog.InfoLevel))
	})

	It("respects the configured level", func() {
		l := logging.New(&config.Config{LogLevel: "debug"})
		Expect(l.GetLevel()).To(Equal(zerolog.DebugLevel))
	})

	It("falls back to info on garbage", func() {
		l := logging.New(&config.Config{LogLevel: "shout"})
		Expect(l.GetLevel()).To(Equal(zerolog.InfoLevel))
	})
})
