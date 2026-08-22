package valkey_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/pkg/valkey"
	"github.com/thelemail/thaumaste/internal/testutil/valkeytest"
)

func limits() config.Limits {
	return config.Limits{SendPerUser: 3, SendWindow: time.Second}
}

func TestALockIsHeldByOneCallerAtATime(t *testing.T) {
	client := valkeytest.Connect(t, limits())

	var concurrent, peak atomic.Int32
	done := make(chan struct{})

	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			held, release, err := client.Lock(t.Context(), "room")
			if err != nil {
				t.Errorf("Lock: %v", err)
				return
			}
			defer release()

			now := concurrent.Add(1)
			for {
				high := peak.Load()
				if now <= high || peak.CompareAndSwap(high, now) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			concurrent.Add(-1)

			if held.Err() != nil {
				t.Errorf("lock context died while held: %v", held.Err())
			}
		}()
	}
	for range 8 {
		<-done
	}

	if got := peak.Load(); got != 1 {
		t.Fatalf("peak concurrency = %d, want 1", got)
	}
}

func TestTheLockContextDiesWhenTheLockIsReleased(t *testing.T) {
	client := valkeytest.Connect(t, limits())

	held, release, err := client.Lock(t.Context(), "room")
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if held.Err() != nil {
		t.Fatalf("context already cancelled: %v", held.Err())
	}
	release()

	select {
	case <-held.Done():
	case <-time.After(time.Second):
		t.Fatal("the lock context outlived the lock")
	}
}

func TestWaitingForALockRespectsCancellation(t *testing.T) {
	client := valkeytest.Connect(t, limits())

	_, release, err := client.Lock(t.Context(), "room")
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	if _, _, err := client.Lock(ctx, "room"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestTheLimiterRefusesOncePastTheLimitAndRecovers(t *testing.T) {
	client := valkeytest.Connect(t, limits())

	for i := range 3 {
		verdict, err := client.Allow(t.Context(), "user", 3, 300*time.Millisecond)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if !verdict.Allowed {
			t.Fatalf("send %d refused inside the limit", i)
		}
	}

	verdict, err := client.Allow(t.Context(), "user", 3, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if verdict.Allowed {
		t.Fatal("the fourth send was allowed past a limit of three")
	}
	if verdict.ResetAt.IsZero() {
		t.Fatal("a refusal carries no reset time for the client to back off to")
	}

	time.Sleep(400 * time.Millisecond)

	verdict, err = client.Allow(t.Context(), "user", 3, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !verdict.Allowed {
		t.Fatal("the limit did not recover after its window")
	}
}

func TestLimitsAreCountedPerKey(t *testing.T) {
	client := valkeytest.Connect(t, limits())

	for range 3 {
		if _, err := client.Allow(t.Context(), "alice", 3, time.Second); err != nil {
			t.Fatalf("Allow: %v", err)
		}
	}
	verdict, err := client.Allow(t.Context(), "bob", 3, time.Second)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !verdict.Allowed {
		t.Fatal("one caller exhausting their limit refused another")
	}
}

func TestAnUnreachableServerIsAStartupFailure(t *testing.T) {
	cfg := config.Valkey{
		Addrs:        []string{"127.0.0.1:1"},
		KeyPrefix:    "thaumaste_test_unreachable",
		DialTimeout:  200 * time.Millisecond,
		LockValidity: time.Second,
	}

	client, err := valkey.New(t.Context(), cfg, limits())
	if err == nil {
		client.Close()
		t.Fatal("New built a client against an unreachable server, so the process would start without valkey")
	}
}

func TestAServerWithNoAddressIsAConfigurationError(t *testing.T) {
	if _, err := valkey.New(t.Context(), config.Valkey{}, limits()); !errors.Is(err, valkey.ErrNoAddress) {
		t.Fatalf("New error = %v, want %v", err, valkey.ErrNoAddress)
	}
}

func TestWaitingForALockGivesUpRatherThanHangingForever(t *testing.T) {
	settings := valkeytest.Settings(t)
	settings.LockValidity = 300 * time.Millisecond
	valkeytest.Require(t, settings)

	holder, err := valkey.New(t.Context(), settings, limits())
	if err != nil {
		t.Fatalf("valkey: %v", err)
	}
	t.Cleanup(holder.Close)

	waiter, err := valkey.New(t.Context(), settings, limits())
	if err != nil {
		t.Fatalf("valkey: %v", err)
	}
	t.Cleanup(waiter.Close)

	_, release, err := holder.Lock(t.Context(), "contended")
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer release()

	started := time.Now()
	_, _, err = waiter.Lock(t.Context(), "contended")
	waited := time.Since(started)

	if err == nil {
		t.Fatal("a second caller took a lock that was already held")
	}
	if !errors.Is(err, valkey.ErrHeld) {
		t.Fatalf("waiting for a held lock returned %v, want it to read as held", err)
	}
	if errors.Is(err, valkey.ErrUnavailable) {
		t.Fatal("a contended lock is indistinguishable from an outage")
	}
	if waited > 5*time.Second {
		t.Fatalf("waiting for a held lock took %s", waited)
	}
}
