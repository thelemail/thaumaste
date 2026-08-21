package internal

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/wire"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/handler/http/matrix"
	"github.com/thelemail/thaumaste/internal/handler/http/matrix/middleware"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/runtime"
	"github.com/thelemail/thaumaste/internal/service"
)

type ServeRuntime struct {
	srv             *http.Server
	db              *postgres.Client
	shutdownTimeout time.Duration
}

func (r *ServeRuntime) Name() string { return "serve" }

func (r *ServeRuntime) Ready(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

func (r *ServeRuntime) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := r.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.shutdownTimeout)
		defer cancel()
		return r.srv.Shutdown(shutdownCtx)
	}
}

var _ runtime.Service = (*ServeRuntime)(nil)

func provideServeRuntime(
	cfg config.Server,
	sign config.Signing,
	db *postgres.Client,
	tenants service.Tenants,
	tokens service.Tokens,
	users service.Users,
	rooms service.Rooms,
	clock func() time.Time,
) *ServeRuntime {
	router := chi.NewRouter()
	router.Use(middleware.CORS)
	router.Use(middleware.RequestID)
	router.Use(middleware.RecoverPanic)
	router.Use(middleware.AccessLog)

	matrix.New(tenants, tokens, users, rooms, cfg, sign, clock).Mount(router)

	return &ServeRuntime{
		srv: &http.Server{
			Addr:         cfg.Addr,
			Handler:      router,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
		},
		db:              db,
		shutdownTimeout: cfg.ShutdownTimeout,
	}
}

var ServeSet = wire.NewSet(provideServeRuntime)
