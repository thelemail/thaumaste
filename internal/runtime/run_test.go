package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/runtime"
)

type stub struct {
	name string
	run  func(ctx context.Context) error
}

func (s stub) Name() string                  { return s.name }
func (s stub) Run(ctx context.Context) error { return s.run(ctx) }

func TestRunRefusesToStartWithNoServices(t *testing.T) {
	if err := runtime.Run(context.Background(), time.Second, runtime.Options{}); err == nil {
		t.Fatal("Run with no services should fail")
	}
}

func TestRunReturnsTheErrorFromAFailedService(t *testing.T) {
	boom := errors.New("boom")
	svc := stub{name: "stub", run: func(context.Context) error { return boom }}

	err := runtime.Run(context.Background(), time.Second, runtime.Options{}, svc)
	if !errors.Is(err, boom) {
		t.Fatalf("Run error = %v, want %v", err, boom)
	}
}

func TestServicesKeepRunningUntilTheDrainDelayElapses(t *testing.T) {
	const drain = 80 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()

	var cancelledAfter time.Duration
	svc := stub{name: "stub", run: func(ctx context.Context) error {
		<-ctx.Done()
		cancelledAfter = time.Since(start)
		return nil
	}}

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	if err := runtime.Run(ctx, time.Second, runtime.Options{DrainDelay: drain}, svc); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cancelledAfter < drain {
		t.Fatalf("service cancelled after %v, want it to survive the %v drain delay", cancelledAfter, drain)
	}
}

func TestRunReportsShutdownTimeoutWhenAServiceWillNotDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	defer close(release)

	svc := stub{name: "stuck", run: func(context.Context) error {
		<-release
		return nil
	}}

	go cancel()

	err := runtime.Run(ctx, 50*time.Millisecond, runtime.Options{}, svc)
	if !errors.Is(err, runtime.ErrShutdownTimeout) {
		t.Fatalf("Run error = %v, want %v", err, runtime.ErrShutdownTimeout)
	}
}
