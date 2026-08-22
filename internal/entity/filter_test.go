package entity_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func TestEveryMalformedFilterComplementSendsIsRefused(t *testing.T) {
	const nao = `"not_an_object"`
	const nal = `"not_a_list"`

	cases := []string{
		`{"presence": ` + nao + `}`,
		`{"room": {"timeline": ` + nao + `}}`,
		`{"room": {"state": ` + nao + `}}`,
		`{"room": {"ephemeral": ` + nao + `}}`,
		`{"room": {"account_data": ` + nao + `}}`,
		`{"room": {"timeline": {"rooms": ` + nal + `}}}`,
		`{"room": {"timeline": {"not_rooms": ` + nal + `}}}`,
		`{"room": {"timeline": {"senders": ` + nal + `}}}`,
		`{"room": {"timeline": {"not_senders": ` + nal + `}}}`,
		`{"room": {"timeline": {"types": ` + nal + `}}}`,
		`{"room": {"timeline": {"not_types": ` + nal + `}}}`,
		`{"room": {"timeline": {"types": [1]}}}`,
		`{"room": {"timeline": {"rooms": ["not_a_room_id"]}}}`,
		`{"room": {"timeline": {"senders": ["not_a_sender_id"]}}}`,
	}

	for _, raw := range cases {
		if _, err := entity.ParseFilter([]byte(raw)); !errors.Is(err, entity.ErrBadFilter) {
			t.Fatalf("ParseFilter(%s) error = %v, want ErrBadFilter", raw, err)
		}
	}
}

func TestAWellFormedFilterRoundTrips(t *testing.T) {
	raw := []byte(`{"room":{"timeline":{"limit":10,"types":["m.room.message"],"senders":["@alice:example.test"]}}}`)

	filter, err := entity.ParseFilter(raw)
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}

	var back struct {
		Room struct {
			Timeline struct {
				Limit int `json:"limit"`
			} `json:"timeline"`
		} `json:"room"`
	}
	if err := json.Unmarshal(filter.Document, &back); err != nil {
		t.Fatalf("decode stored filter: %v", err)
	}
	if back.Room.Timeline.Limit != 10 {
		t.Fatalf("room.timeline.limit = %d, want 10", back.Room.Timeline.Limit)
	}

	timeline := filter.Timeline()
	if timeline.Limit != 10 || len(timeline.Types) != 1 || timeline.Types[0] != "m.room.message" {
		t.Fatalf("projected timeline filter = %+v", timeline)
	}
}

func TestIdenticalFiltersHashTheSameAndDifferentOnesDoNot(t *testing.T) {
	first, err := entity.ParseFilter([]byte(`{"room":{"timeline":{"limit":10}}}`))
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	spaced, err := entity.ParseFilter([]byte("{\n  \"room\" : { \"timeline\" : { \"limit\" : 10 } }\n}"))
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	other, err := entity.ParseFilter([]byte(`{"room":{"timeline":{"limit":11}}}`))
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}

	firstHash, err := first.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	spacedHash, err := spaced.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	otherHash, err := other.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if string(firstHash) != string(spacedHash) {
		t.Fatal("the same filter written differently hashed differently")
	}
	if string(firstHash) == string(otherHash) {
		t.Fatal("two different filters hashed the same")
	}
}

func TestAnUnknownEventFormatIsRefused(t *testing.T) {
	if _, err := entity.ParseFilter([]byte(`{"event_format":"telepathy"}`)); !errors.Is(err, entity.ErrBadFilter) {
		t.Fatalf("an unknown event_format was accepted")
	}
	for _, format := range []string{"client", "federation"} {
		if _, err := entity.ParseFilter([]byte(`{"event_format":"` + format + `"}`)); err != nil {
			t.Fatalf("event_format %q was refused: %v", format, err)
		}
	}
}
