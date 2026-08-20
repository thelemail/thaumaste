package serialiser

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkForOneKeyRunsOneAtATime(t *testing.T) {
	s := New()

	var concurrent, peak atomic.Int32
	var wg sync.WaitGroup

	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Do(t.Context(), "room", func(context.Context) error {
				now := concurrent.Add(1)
				for {
					high := peak.Load()
					if now <= high || peak.CompareAndSwap(high, now) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				concurrent.Add(-1)
				return nil
			})
		}()
	}
	wg.Wait()

	if got := peak.Load(); got != 1 {
		t.Fatalf("peak concurrency for one key = %d, want 1", got)
	}
}

func TestWorkForDifferentKeysRunsConcurrently(t *testing.T) {
	s := New()

	const keys = 4
	entered := make(chan struct{}, keys)
	release := make(chan struct{})

	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Do(t.Context(), string(rune('a'+i)), func(context.Context) error {
				entered <- struct{}{}
				<-release
				return nil
			})
		}()
	}

	for range keys {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("keys did not run concurrently")
		}
	}
	close(release)
	wg.Wait()
}

func TestWaitingRespectsCancellation(t *testing.T) {
	s := New()

	holding := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = s.Do(t.Context(), "room", func(context.Context) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	defer close(release)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := s.Do(ctx, "room", func(context.Context) error {
		t.Error("work must not run once the caller has gone away")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want %v", err, context.Canceled)
	}
}

func TestTheErrorFromTheWorkIsReturned(t *testing.T) {
	s := New()
	boom := errors.New("boom")

	if err := s.Do(t.Context(), "room", func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("Do error = %v, want %v", err, boom)
	}
}

func TestKeysAreForgottenOnceNobodyHoldsThem(t *testing.T) {
	s := New()

	for i := range 100 {
		_ = s.Do(t.Context(), string(rune('a'+i%26)), func(context.Context) error { return nil })
	}

	if got := s.tracked(); got != 0 {
		t.Fatalf("%d keys still tracked, want 0", got)
	}
}

func TestAPanicDoesNotWedgeTheKey(t *testing.T) {
	s := New()

	func() {
		defer func() { _ = recover() }()
		_ = s.Do(t.Context(), "room", func(context.Context) error { panic("boom") })
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Do(t.Context(), "room", func(context.Context) error { return nil })
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the key stayed locked after a panic")
	}
}
