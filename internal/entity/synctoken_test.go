package entity_test

import (
	"errors"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func TestASyncTokenRoundTrips(t *testing.T) {
	original := entity.SyncToken{Events: 42, AccountData: 7, Receipts: 0, ToDevice: 1234567, DeviceLists: 9, Typing: 3}

	back, err := entity.ParseSyncToken(original.String())
	if err != nil {
		t.Fatalf("ParseSyncToken: %v", err)
	}
	if back != original {
		t.Fatalf("token round-tripped as %+v, want %+v", back, original)
	}
}

func TestAnEmptyTokenIsTheStartOfTime(t *testing.T) {
	back, err := entity.ParseSyncToken("")
	if err != nil {
		t.Fatalf("ParseSyncToken(\"\"): %v", err)
	}
	if back != (entity.SyncToken{}) {
		t.Fatalf("an absent token parsed as %+v", back)
	}
}

func TestAnInventedTokenIsRefused(t *testing.T) {
	for _, raw := range []string{"nonsense", "s", "s!!!", "sYWJj", "sMC4wLjAuMC4w", "sMC4wLi0xLjAuMC4w"} {
		if _, err := entity.ParseSyncToken(raw); !errors.Is(err, entity.ErrUnknownToken) {
			t.Fatalf("ParseSyncToken(%q) error = %v, want ErrUnknownToken", raw, err)
		}
	}
}
