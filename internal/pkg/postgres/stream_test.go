package postgres_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/testutil/pgtest"
)

func newStream(t *testing.T, pg *postgres.Client, name string, negative bool) *postgres.Stream {
	t.Helper()
	s, err := postgres.NewStream(t.Context(), pg, postgres.StreamConfig{
		Name:     name,
		Instance: "test",
		Sequence: "events_stream_seq",
		Negative: negative,
	})
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	return s
}

func next(t *testing.T, s *postgres.Stream, n int) *postgres.Positions {
	t.Helper()
	p, err := s.Next(t.Context(), n)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return p
}

func TestNextRejectsANonPositiveCount(t *testing.T) {
	pg := pgtest.Connect(t, "stream_positions")
	s := newStream(t, pg, "reject", false)

	if _, err := s.Next(t.Context(), 0); !errors.Is(err, postgres.ErrNonPositiveCount) {
		t.Fatalf("Next(0) error = %v, want %v", err, postgres.ErrNonPositiveCount)
	}
}

func TestPositionsIncreaseStrictlyUnderConcurrentAllocation(t *testing.T) {
	pg := pgtest.Connect(t, "stream_positions")
	s := newStream(t, pg, "concurrent", false)

	const writers, each = 8, 25

	var mu sync.Mutex
	seen := make(map[int64]struct{}, writers*each)

	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				p := next(t, s, 1)
				mu.Lock()
				if _, dup := seen[p.IDs[0]]; dup {
					mu.Unlock()
					t.Errorf("position %d handed out twice", p.IDs[0])
					return
				}
				seen[p.IDs[0]] = struct{}{}
				mu.Unlock()
				p.Release()
			}
		}()
	}
	wg.Wait()

	if len(seen) != writers*each {
		t.Fatalf("got %d distinct positions, want %d", len(seen), writers*each)
	}
}

func TestWatermarkDoesNotPassAnUnreleasedPosition(t *testing.T) {
	pg := pgtest.Connect(t, "stream_positions")
	s := newStream(t, pg, "held", false)

	held := next(t, s, 1)
	later := next(t, s, 1)

	later.Release()

	if got := s.Current(); got >= held.IDs[0] {
		t.Fatalf("watermark = %d, must stay below the held position %d", got, held.IDs[0])
	}

	held.Release()

	if got := s.Current(); got != later.IDs[0] {
		t.Fatalf("watermark = %d, want %d once everything is released", got, later.IDs[0])
	}
}

func TestWatermarkAdvancesPastAPositionThatWasNeverPersisted(t *testing.T) {
	pg := pgtest.Connect(t, "stream_positions")
	s := newStream(t, pg, "rolledback", false)

	committed := next(t, s, 1)
	rolledBack := next(t, s, 1)
	alsoCommitted := next(t, s, 1)

	committed.Release()
	rolledBack.Release()
	alsoCommitted.Release()

	if got := s.Current(); got != alsoCommitted.IDs[0] {
		t.Fatalf("watermark = %d, want %d: a rolled back position must not stall the stream",
			got, alsoCommitted.IDs[0])
	}
}

func TestReleasingTwiceIsHarmless(t *testing.T) {
	pg := pgtest.Connect(t, "stream_positions")
	s := newStream(t, pg, "twice", false)

	first := next(t, s, 1)
	second := next(t, s, 1)

	first.Release()
	first.Release()

	if got := s.Current(); got >= second.IDs[0] {
		t.Fatalf("watermark = %d, must stay below the still-held position %d", got, second.IDs[0])
	}
}

func TestWatermarkNeverPassesAPositionStillInFlight(t *testing.T) {
	pg := pgtest.Connect(t, "stream_positions")
	s := newStream(t, pg, "stress", false)

	var mu sync.Mutex
	held := map[int64]struct{}{}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 60 {
				p := next(t, s, 1+rand.IntN(3))

				mu.Lock()
				for _, id := range p.IDs {
					held[id] = struct{}{}
				}
				lowest := lowestOf(held)
				mu.Unlock()

				if got := s.Current(); lowest > 0 && got >= lowest {
					t.Errorf("watermark %d reached or passed in-flight position %d", got, lowest)
				}

				mu.Lock()
				for _, id := range p.IDs {
					delete(held, id)
				}
				mu.Unlock()

				p.Release()
			}
		}()
	}
	wg.Wait()
}

func lowestOf(set map[int64]struct{}) int64 {
	var lowest int64
	for id := range set {
		if lowest == 0 || id < lowest {
			lowest = id
		}
	}
	return lowest
}

func TestWatermarkSurvivesARestart(t *testing.T) {
	pg := pgtest.Connect(t, "stream_positions")

	before := newStream(t, pg, "restart", false)
	p := next(t, before, 3)
	p.Release()
	want := before.Current()

	after := newStream(t, pg, "restart", false)

	if got := after.Current(); got != want {
		t.Fatalf("watermark after restart = %d, want %d", got, want)
	}
}

func TestNegativePositionsRunBackwards(t *testing.T) {
	pg := pgtest.Connect(t, "stream_positions")
	s := newStream(t, pg, "backfill", true)

	first := next(t, s, 1)
	first.Release()
	second := next(t, s, 1)
	second.Release()

	if first.IDs[0] >= 0 || second.IDs[0] >= 0 {
		t.Fatalf("backfill positions must be negative, got %d then %d", first.IDs[0], second.IDs[0])
	}
	if second.IDs[0] >= first.IDs[0] {
		t.Fatalf("backfill positions must decrease, got %d then %d", first.IDs[0], second.IDs[0])
	}
	if got := s.Current(); got != second.IDs[0] {
		t.Fatalf("watermark = %d, want %d", got, second.IDs[0])
	}
}

func TestANegativeWatermarkSurvivesARestart(t *testing.T) {
	pg := pgtest.Connect(t, "stream_positions")

	before := newStream(t, pg, "backfill-restart", true)
	for range 3 {
		next(t, before, 1).Release()
	}
	want := before.Current()

	after := newStream(t, pg, "backfill-restart", true)

	if got := after.Current(); got != want {
		t.Fatalf("watermark after restart = %d, want %d", got, want)
	}
}

func TestACancelledContextDoesNotLeakAnInFlightPosition(t *testing.T) {
	pg := pgtest.Connect(t, "stream_positions")
	s := newStream(t, pg, "cancelled", false)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.Next(ctx, 1); err == nil {
		t.Fatal("Next with a cancelled context should fail")
	}

	p := next(t, s, 1)
	p.Release()

	if got := s.Current(); got != p.IDs[0] {
		t.Fatalf("watermark = %d, want %d: a failed allocation must not pin the stream", got, p.IDs[0])
	}
}
