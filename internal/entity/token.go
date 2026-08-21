package entity

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrBadToken = errors.New("entity: malformed pagination token")

type Position struct {
	Topological int64
	Stream      int64
}

func (p Position) String() string {
	return "t" + strconv.FormatInt(p.Topological, 10) + "-" + strconv.FormatInt(p.Stream, 10)
}

func (p Position) Before(other Position) bool {
	if p.Topological != other.Topological {
		return p.Topological < other.Topological
	}
	return p.Stream < other.Stream
}

type Token struct {
	Position       Position
	HasTopological bool
}

func (t Token) String() string {
	if !t.HasTopological {
		return "s" + strconv.FormatInt(t.Position.Stream, 10)
	}
	return t.Position.String()
}

func Anchor(position Position) Token {
	return Token{Position: position, HasTopological: true}
}

func ParseToken(raw string) (Token, error) {
	switch {
	case strings.HasPrefix(raw, "t"):
		topological, stream, found := strings.Cut(raw[1:], "-")
		if !found {
			return Token{}, fmt.Errorf("%w: %q", ErrBadToken, raw)
		}
		t, err := strconv.ParseInt(topological, 10, 64)
		if err != nil {
			return Token{}, fmt.Errorf("%w: %q", ErrBadToken, raw)
		}
		s, err := strconv.ParseInt(stream, 10, 64)
		if err != nil {
			return Token{}, fmt.Errorf("%w: %q", ErrBadToken, raw)
		}
		return Anchor(Position{Topological: t, Stream: s}), nil
	case strings.HasPrefix(raw, "s"):
		s, err := strconv.ParseInt(raw[1:], 10, 64)
		if err != nil {
			return Token{}, fmt.Errorf("%w: %q", ErrBadToken, raw)
		}
		return Token{Position: Position{Stream: s}}, nil
	default:
		return Token{}, fmt.Errorf("%w: %q", ErrBadToken, raw)
	}
}
