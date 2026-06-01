// Package logging provides a structured zerolog.Logger for the application.
package logging

import (
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.uber.org/fx"

	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
)

// Module exposes the zerolog logger.
var Module = fx.Module("logging",
	fx.Provide(New),
)

// New builds a zerolog.Logger at the configured level. By default it writes
// human-readable console output; set LOG_FORMAT=json for structured JSON.
func New(cfg *config.Config) zerolog.Logger {
	level, err := zerolog.ParseLevel(strings.ToLower(cfg.LogLevel))
	if err != nil || level == zerolog.NoLevel {
		level = zerolog.InfoLevel
	}
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs

	if strings.EqualFold(cfg.LogFormat, "json") {
		return zerolog.New(os.Stderr).Level(level).With().Timestamp().Logger()
	}
	writer := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.DateTime}
	return zerolog.New(writer).Level(level).With().Timestamp().Logger()
}
