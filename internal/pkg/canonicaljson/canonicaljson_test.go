package canonicaljson_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thelemail/thaumaste/internal/pkg/canonicaljson"
)

func encode(t *testing.T, in string) string {
	t.Helper()
	out, err := canonicaljson.Encode([]byte(in))
	if err != nil {
		t.Fatalf("Encode(%s): %v", in, err)
	}
	return string(out)
}

func TestSpecExamplesEncodeAsPublished(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{}`, `{}`},
		{`{"one":1,"two":"Two"}`, `{"one":1,"two":"Two"}`},
		{`{"b":"2","a":"1"}`, `{"a":"1","b":"2"}`},
		{
			`{"auth":{"success":true,"mxid":"@john.doe:example.com","profile":{"display_name":"John Doe","three_pids":[{"medium":"email","address":"john.doe@example.org"},{"medium":"msisdn","address":"123456789"}]}}}`,
			`{"auth":{"mxid":"@john.doe:example.com","profile":{"display_name":"John Doe","three_pids":[{"address":"john.doe@example.org","medium":"email"},{"address":"123456789","medium":"msisdn"}]},"success":true}}`,
		},
		{`{"a":"日"}`, "{\"a\":\"日\"}"},
		{`{"a":null}`, `{"a":null}`},
	}

	for _, c := range cases {
		if got := encode(t, c.in); got != c.want {
			t.Fatalf("Encode(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestNegativeZeroAndExponentsAreNormalised(t *testing.T) {
	if got := encode(t, `{"a":-0,"b":1e10}`); got != `{"a":0,"b":10000000000}` {
		t.Fatalf("Encode = %s", got)
	}
}

func TestKeysSortByCodepointNotByLength(t *testing.T) {
	if got := encode(t, `{"本":2,"日":1}`); got != "{\"日\":1,\"本\":2}" {
		t.Fatalf("Encode = %s", got)
	}
	if got := encode(t, `{"ab":1,"a":2}`); got != `{"a":2,"ab":1}` {
		t.Fatalf("Encode = %s", got)
	}
}

func TestNonIntegerNumbersAreRejected(t *testing.T) {
	if _, err := canonicaljson.Encode([]byte(`{"a":1.5}`)); !errors.Is(err, canonicaljson.ErrFloat) {
		t.Fatalf("error = %v, want ErrFloat", err)
	}
}

func TestIntegersOutsideTheSafeRangeAreRejected(t *testing.T) {
	for _, in := range []string{`{"a":9007199254740992}`, `{"a":-9007199254740992}`} {
		if _, err := canonicaljson.Encode([]byte(in)); !errors.Is(err, canonicaljson.ErrIntegerRange) {
			t.Fatalf("Encode(%s) error = %v, want ErrIntegerRange", in, err)
		}
	}
}

func TestControlCharactersAreEscapedAndPrintableOnesAreNot(t *testing.T) {
	if got := encode(t, `{"a":"\u0000\n\t\"\\"}`); got != `{"a":"\u0000\n\t\"\\"}` {
		t.Fatalf("Encode = %s", got)
	}
	if got := encode(t, `{"a":"\u001f"}`); got != `{"a":"\u001f"}` {
		t.Fatalf("Encode = %s", got)
	}
	if got := encode(t, `{"a":"\u00e9\u2603"}`); got != "{\"a\":\"\u00e9\u2603\"}" {
		t.Fatalf("Encode = %s", got)
	}
}

func TestOutputIsTheShortestEncodingWithNoWhitespace(t *testing.T) {
	got := encode(t, "{\n\t\"b\" : 2,\n\t\"a\" : [ 1, 2 ]\n}")
	if strings.ContainsAny(got, " \n\t") {
		t.Fatalf("Encode kept whitespace: %s", got)
	}
	if got != `{"a":[1,2],"b":2}` {
		t.Fatalf("Encode = %s", got)
	}
}

func TestEncodingIsStableAcrossRepeatedRuns(t *testing.T) {
	const in = `{"z":1,"y":{"b":2,"a":3},"x":[{"n":1,"m":2}]}`
	first := encode(t, in)
	for range 20 {
		if got := encode(t, in); got != first {
			t.Fatalf("Encode is not stable: %s then %s", first, got)
		}
	}
}

func TestMarshalGoesThroughTheSameCanonicalForm(t *testing.T) {
	got, err := canonicaljson.Marshal(map[string]any{"b": 2, "a": "1"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != `{"a":"1","b":2}` {
		t.Fatalf("Marshal = %s", got)
	}
}
