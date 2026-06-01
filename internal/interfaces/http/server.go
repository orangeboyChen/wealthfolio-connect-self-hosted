// Package http exposes an integrated HTTP gateway: chi router, common
// middleware, public health routes and authenticated /api/v1 + /auth/v1
// route groups. Handlers themselves live in handlers/.
package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"go.uber.org/fx"

	appauth "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/application/auth"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/infrastructure/config"
	"github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/handlers"
	mw "github.com/wealthfolio/wealthfolio-connect-self-hosted/internal/interfaces/http/middleware"
)

// Module wires the HTTP server, router and handlers.
var Module = fx.Module("http",
	fx.Provide(
		NewRouter,
		handlers.NewHealthHandler,
		// Auth route registrar
		AsAuthRoute(handlers.NewAuthHandler),
		// API route registrars
		AsAPIRoute(handlers.NewUserHandler),
		AsPublicAPIRoute(handlers.NewSubscriptionHandler),
		AsAPIRoute(handlers.NewConnectionHandler),
		AsAPIRoute(handlers.NewAccountHandler),
		AsAPIRoute(handlers.NewActivityHandler),
		AsAPIRoute(handlers.NewHoldingHandler),
	),
	fx.Invoke(StartServer),
)

// AuthRouteRegistrar is implemented by every handler that mounts under
// /auth/v1 (apikey-gated).
type AuthRouteRegistrar interface {
	RegisterAuthRoutes(r chi.Router)
}

// APIRouteRegistrar is implemented by every handler that mounts under
// /api/v1 (Bearer-gated).
type APIRouteRegistrar interface {
	RegisterAPIRoutes(r chi.Router)
}

// PublicAPIRouteRegistrar is implemented by handlers that mount under
// /api/v1 but must remain reachable without a Bearer token. Used for
// plans/marketing endpoints the Wealthfolio UI fetches before login.
type PublicAPIRouteRegistrar interface {
	RegisterPublicAPIRoutes(r chi.Router)
}

// RouterParams aggregates everything needed by NewRouter via fx.In so we can
// inject groups of registrars.
type RouterParams struct {
	fx.In
	Config           *config.Config
	Logger           zerolog.Logger
	Auth             *appauth.Service
	Health           *handlers.HealthHandler
	AuthRegistrars   []AuthRouteRegistrar      `group:"auth_routes"`
	APIRegistrars    []APIRouteRegistrar       `group:"api_routes"`
	PublicRegistrars []PublicAPIRouteRegistrar `group:"public_api_routes"`
}

// NewRouter builds the chi router with the full middleware stack and route
// registration.
func NewRouter(p RouterParams) http.Handler {
	r := chi.NewRouter()

	r.Use(mw.RequestID)
	r.Use(mw.Recover(p.Logger))
	r.Use(mw.Logger(p.Logger))
	r.Use(mw.CORS(p.Config.CORSOrigins))

	r.Get("/healthz", p.Health.Live)
	r.Get("/readyz", p.Health.Ready)

	r.Route("/auth/v1", func(sub chi.Router) {
		sub.Use(mw.APIKey(p.Auth))
		for _, reg := range p.AuthRegistrars {
			reg.RegisterAuthRoutes(sub)
		}
	})

	r.Route("/api/v1", func(sub chi.Router) {
		// Public sub-group: mounted before Bearer middleware so endpoints
		// like /subscription/plans remain reachable without a token.
		sub.Group(func(pub chi.Router) {
			for _, reg := range p.PublicRegistrars {
				reg.RegisterPublicAPIRoutes(pub)
			}
		})
		sub.Group(func(authd chi.Router) {
			authd.Use(mw.Bearer(p.Auth))
			for _, reg := range p.APIRegistrars {
				reg.RegisterAPIRoutes(authd)
			}
		})
	})

	return r
}

// StartServer registers OnStart/OnStop hooks that run the HTTP server.
func StartServer(lc fx.Lifecycle, cfg *config.Config, h http.Handler, log zerolog.Logger) {
	server := &http.Server{
		Addr:    ":" + strconv.Itoa(cfg.ServerPort),
		Handler: h,
		// Defense-in-depth against Slowloris-style attacks: bound every
		// phase of the request/response lifecycle.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			ln, err := net.Listen("tcp", server.Addr) //nolint:noctx // startup listener does not need request context
			if err != nil {
				return fmt.Errorf("http: listen %s: %w", server.Addr, err)
			}
			go func() {
				log.Info().Str("addr", server.Addr).Msg("http server starting")
				if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error().Err(err).Msg("http server crashed")
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return server.Shutdown(shutdownCtx)
		},
	})
}

// AsAuthRoute is a helper for fx.Provide registrations that adds a handler
// to the "auth_routes" group.
func AsAuthRoute(f any) any {
	return fx.Annotate(f,
		fx.As(new(AuthRouteRegistrar)),
		fx.ResultTags(`group:"auth_routes"`),
	)
}

// AsAPIRoute is a helper for fx.Provide registrations that adds a handler
// to the "api_routes" group.
func AsAPIRoute(f any) any {
	return fx.Annotate(f,
		fx.As(new(APIRouteRegistrar)),
		fx.ResultTags(`group:"api_routes"`),
	)
}

// AsPublicAPIRoute is a helper for fx.Provide registrations that adds a
// handler to the "public_api_routes" group: routes mounted under /api/v1
// without the Bearer middleware.
func AsPublicAPIRoute(f any) any {
	return fx.Annotate(f,
		fx.As(new(PublicAPIRouteRegistrar)),
		fx.ResultTags(`group:"public_api_routes"`),
	)
}
