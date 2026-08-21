package entity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"
)

const (
	maxSafeInteger = int64(1)<<53 - 1
	minSafeInteger = -maxSafeInteger
)

var (
	ErrCanonicalFloat        = errors.New("entity: float values are not permitted")
	ErrCanonicalIntegerRange = errors.New("entity: integer outside the safe range")
	ErrCanonicalUTF8         = errors.New("entity: string is not valid utf-8")
	ErrCanonicalType         = errors.New("entity: unsupported type")
)

func CanonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("entity: marshal: %w", err)
	}
	return CanonicalJSONFrom(raw)
}

func CanonicalJSONFrom(raw []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("entity: decode: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("entity: trailing data after the top-level value")
	}

	var buf bytes.Buffer
	if err := write(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func write(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case json.Number:
		return writeNumber(buf, t)
	case string:
		return writeString(buf, t)
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := write(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case map[string]any:
		return writeObject(buf, t)
	default:
		return fmt.Errorf("%w: %T", ErrCanonicalType, v)
	}
}

func writeObject(buf *bytes.Buffer, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeString(buf, k); err != nil {
			return err
		}
		buf.WriteByte(':')
		if err := write(buf, m[k]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

func writeNumber(buf *bytes.Buffer, n json.Number) error {
	if i, err := n.Int64(); err == nil {
		if i < minSafeInteger || i > maxSafeInteger {
			return fmt.Errorf("%w: %s", ErrCanonicalIntegerRange, n)
		}
		buf.WriteString(strconv.FormatInt(i, 10))
		return nil
	}

	f, err := n.Float64()
	if err != nil {
		return fmt.Errorf("entity: number %s: %w", n, err)
	}
	if math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) {
		return fmt.Errorf("%w: %s", ErrCanonicalFloat, n)
	}
	if f < float64(minSafeInteger) || f > float64(maxSafeInteger) {
		return fmt.Errorf("%w: %s", ErrCanonicalIntegerRange, n)
	}
	buf.WriteString(strconv.FormatInt(int64(f), 10))
	return nil
}

func writeString(buf *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: %q", ErrCanonicalUTF8, s)
	}

	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
				continue
			}
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
	return nil
}
