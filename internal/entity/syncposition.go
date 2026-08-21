package entity

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrUnknownPos = errors.New("entity: unknown sync position")

type SyncPosition struct {
	ConnectionNID int64
	Generation    int64
}

func (p SyncPosition) String() string {
	raw := strconv.FormatInt(p.ConnectionNID, 36) + "." + strconv.FormatInt(p.Generation, 36)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func ParseSyncPosition(raw string) (SyncPosition, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return SyncPosition{}, fmt.Errorf("%w: %q", ErrUnknownPos, raw)
	}
	connection, generation, found := strings.Cut(string(decoded), ".")
	if !found {
		return SyncPosition{}, fmt.Errorf("%w: %q", ErrUnknownPos, raw)
	}
	c, err := strconv.ParseInt(connection, 36, 64)
	if err != nil || c <= 0 {
		return SyncPosition{}, fmt.Errorf("%w: %q", ErrUnknownPos, raw)
	}
	g, err := strconv.ParseInt(generation, 36, 64)
	if err != nil || g < 0 {
		return SyncPosition{}, fmt.Errorf("%w: %q", ErrUnknownPos, raw)
	}
	return SyncPosition{ConnectionNID: c, Generation: g}, nil
}
