package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/thelemail/thaumaste/internal/pkg/health"
)

var ErrShutdownTimeout = errors.New("shutdown timeout exceeded; some services did not drain")

type Options struct {
	HealthAddr string
	DrainDelay time.Duration
	Ready      health.ReadyFunc
}

func Run(ctx context.Context, shutdownTimeout time.Duration, opts Options, services ...Service) error {
	if len(services) == 0 {
		return errors.New("no services to run")
	}

	state := health.NewState()

	serviceCtx, stopServices := context.WithCancel(context.WithoutCancel(ctx))
	defer stopServices()

	g, gctx := errgroup.WithContext(serviceCtx)
	for _, svc := range services {
		g.Go(func() error {
			slog.InfoContext(gctx, "service starting", "service", svc.Name())
			err := svc.Run(gctx)
			slog.InfoContext(gctx, "service stopped", "service", svc.Name())
			if err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("%s: %w", svc.Name(), err)
			}
			return nil
		})
	}

	healthCtx, stopHealth := context.WithCancel(context.WithoutCancel(ctx))
	defer stopHealth()

	healthDone := make(chan struct{})
	if opts.HealthAddr == "" {
		close(healthDone)
	} else {
		srv := health.NewServer(opts.HealthAddr, state, opts.Ready)
		go func() {
			defer close(healthDone)
			if err := srv.Run(healthCtx); err != nil {
				slog.ErrorContext(healthCtx, "health server stopped", "error", err)
			}
		}()
	}

	done := make(chan error, 1)
	go func() { done <- g.Wait() }()

	stop := func(err error) error {
		stopHealth()
		<-healthDone
		return err
	}

	select {
	case err := <-done:
		return stop(err)
	case <-ctx.Done():
	}

	state.StartDraining()
	if opts.DrainDelay > 0 {
		select {
		case err := <-done:
			return stop(err)
		case <-time.After(opts.DrainDelay):
		}
	}

	slog.Info("draining services")
	stopServices()

	select {
	case err := <-done:
		return stop(err)
	case <-time.After(shutdownTimeout):
		return stop(ErrShutdownTimeout)
	}
}
