// Command server is the entrypoint for the wealthfolio-connect-self-hosted backend.
// It composes every fx Module exposed by the DDD layers and starts the
// long-running HTTP server.
package main

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/fx"

	appauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/auth"
	appbrokerage "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/brokerage"
	appsync "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/sync"
	infraauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/auth"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/clients"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/database"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/logging"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/persistence"
	httpiface "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http"
)

// Modules returns the full graph used by main and integration tests.
func Modules() fx.Option {
	return fx.Options(
		config.Module,
		logging.Module,
		database.Module,
		persistence.Module,
		infraauth.Module,
		appauth.Module,
		appbrokerage.Module,
		appsync.Module,
		clients.Module,
		httpiface.Module,
	)
}

func main() {
	app := fx.New(
		Modules(),
		fx.NopLogger,
	)

	startCtx, cancelStart := context.WithCancel(context.Background())
	if err := app.Start(startCtx); err != nil {
		cancelStart()
		fmt.Fprintln(os.Stderr, "startup error:", err)
		os.Exit(1)
	}
	cancelStart()

	<-app.Done()

	stopCtx, cancelStop := context.WithCancel(context.Background())
	if err := app.Stop(stopCtx); err != nil {
		cancelStop()
		fmt.Fprintln(os.Stderr, "shutdown error:", err)
		os.Exit(1)
	}
	cancelStop()
}
