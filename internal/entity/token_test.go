package entity_test

import (
	"errors"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func TestATokenRoundTripsThroughItsString(t *testing.T) {
	for _, want := range []entity.Position{
		{Topological: 1, Stream: 1},
		{Topological: 4096, Stream: 900719925474099},
		{Topological: 7, Stream: -42},
	} {
		token, err := entity.ParseToken(entity.Anchor(want).String())
		if err != nil {
			t.Fatalf("ParseToken(%v): %v", want, err)
		}
		if !token.HasTopological || token.Position != want {
			t.Fatalf("round trip = %+v, want %v", token, want)
		}
	}
}

func TestAStreamTokenCarriesOnlyItsStreamPosition(t *testing.T) {
	token, err := entity.ParseToken("s918")
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if token.HasTopological {
		t.Fatal("a stream token claimed a topological position")
	}
	if token.Position.Stream != 918 {
		t.Fatalf("stream = %d, want 918", token.Position.Stream)
	}
	if got := token.String(); got != "s918" {
		t.Fatalf("String() = %q, want s918", got)
	}
}

func TestAMalformedTokenIsRefused(t *testing.T) {
	for _, raw := range []string{"", "t", "t1", "t1-", "t-1", "tx-1", "t1-x", "s", "sx", "1-2", "!"} {
		if _, err := entity.ParseToken(raw); !errors.Is(err, entity.ErrBadToken) {
			t.Fatalf("ParseToken(%q) error = %v, want %v", raw, err, entity.ErrBadToken)
		}
	}
}

func TestPositionsOrderTopologicallyThenByStream(t *testing.T) {
	ordered := []entity.Position{
		{Topological: 1, Stream: 5},
		{Topological: 1, Stream: 9},
		{Topological: 2, Stream: 1},
		{Topological: 2, Stream: 2},
	}
	for i := 1; i < len(ordered); i++ {
		if !ordered[i-1].Before(ordered[i]) {
			t.Fatalf("%v is not before %v", ordered[i-1], ordered[i])
		}
		if ordered[i].Before(ordered[i-1]) {
			t.Fatalf("%v sorts before %v", ordered[i], ordered[i-1])
		}
	}
	same := entity.Position{Topological: 3, Stream: 3}
	if same.Before(same) {
		t.Fatal("a position sorts before itself")
	}
}
