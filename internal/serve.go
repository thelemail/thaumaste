package internal

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/wire"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/handler/http/matrix"
	"github.com/thelemail/thaumaste/internal/handler/http/matrix/middleware"
	"github.com/thelemail/thaumaste/internal/pkg/notify"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/runtime"
	"github.com/thelemail/thaumaste/internal/service"
)

type ServeRuntime struct {
	srv             *http.Server
	db              *postgres.Client
	events          service.Events
	sync            service.Sync
	notifier        *notify.Notifier
	connectionTTL   time.Duration
	connectionSweep time.Duration
	shutdownTimeout time.Duration
	sweepEvery      time.Duration
	retain          time.Duration
	clock           func() time.Time
}

func (r *ServeRuntime) Name() string { return "serve" }

func (r *ServeRuntime) Ready(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

func (r *ServeRuntime) sweep(ctx context.Context) {
	if r.sweepEvery <= 0 || r.retain <= 0 {
		return
	}
	ticker := time.NewTicker(r.sweepEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		deleted, err := r.events.SweepTransactions(ctx, r.clock().UTC().Add(-r.retain))
		if err != nil {
			slog.ErrorContext(ctx, "could not sweep spent transactions", "error", err)
			continue
		}
		if deleted > 0 {
			slog.InfoContext(ctx, "swept spent transactions", "deleted", deleted)
		}
	}
}

func (r *ServeRuntime) sweepConnections(ctx context.Context) {
	if r.connectionSweep <= 0 || r.connectionTTL <= 0 {
		return
	}
	ticker := time.NewTicker(r.connectionSweep)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		deleted, err := r.sync.SweepConnections(ctx, r.clock().UTC().Add(-r.connectionTTL))
		if err != nil {
			slog.ErrorContext(ctx, "could not sweep abandoned sync connections", "error", err)
			continue
		}
		if deleted > 0 {
			slog.InfoContext(ctx, "swept abandoned sync connections", "deleted", deleted)
		}
	}
}

func (r *ServeRuntime) Run(ctx context.Context) error {
	go r.sweep(ctx)
	go r.sweepConnections(ctx)
	go func() { _ = r.notifier.Run(ctx) }()

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
	limits config.Limits,
	db *postgres.Client,
	tenants service.Tenants,
	tokens service.Tokens,
	users service.Users,
	rooms service.Rooms,
	events service.Events,
	syncSvc service.Sync,
	keys service.Keys,
	accountData service.AccountData,
	receipts service.Receipts,
	typingSvc service.Typing,
	presenceSvc service.Presence,
	filters service.Filters,
	directory service.Directory,
	notifier *notify.Notifier,
	syncCfg config.Sync,
	clock func() time.Time,
) *ServeRuntime {
	router := chi.NewRouter()
	router.Use(middleware.CORS)
	router.Use(middleware.RequestID)
	router.Use(middleware.RecoverPanic)
	router.Use(middleware.AccessLog)

	matrix.New(tenants, tokens, users, rooms, syncSvc, keys, accountData, receipts, typingSvc,
		presenceSvc, filters, directory, cfg, sign, clock).Mount(router)

	return &ServeRuntime{
		srv: &http.Server{
			Addr:         cfg.Addr,
			Handler:      router,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			IdleTimeout:  cfg.IdleTimeout,
		},
		db:              db,
		events:          events,
		sync:            syncSvc,
		notifier:        notifier,
		connectionTTL:   syncCfg.ConnectionTTL,
		connectionSweep: syncCfg.SweepEvery,
		shutdownTimeout: cfg.ShutdownTimeout,
		sweepEvery:      limits.TxnSweepEvery,
		retain:          limits.TxnRetention,
		clock:           clock,
	}
}

var ServeSet = wire.NewSet(provideServeRuntime)
