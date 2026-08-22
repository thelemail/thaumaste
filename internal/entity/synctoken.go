package entity

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

var ErrUnknownToken = errors.New("entity: unknown sync token")

const tokenParts = 6

type SyncToken struct {
	Events      int64
	AccountData int64
	Receipts    int64
	ToDevice    int64
	DeviceLists int64
	Typing      int64
}

func (t SyncToken) Cursors() SyncCursors {
	return SyncCursors{
		Events:      t.Events,
		AccountData: t.AccountData,
		Receipts:    t.Receipts,
		DeviceLists: t.DeviceLists,
		Typing:      t.Typing,
	}
}

func (t SyncToken) String() string {
	raw := strings.Join([]string{
		strconv.FormatInt(t.Events, 36),
		strconv.FormatInt(t.AccountData, 36),
		strconv.FormatInt(t.Receipts, 36),
		strconv.FormatInt(t.ToDevice, 36),
		strconv.FormatInt(t.DeviceLists, 36),
		strconv.FormatInt(t.Typing, 36),
	}, ".")
	return "s" + base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func ParseSyncToken(raw string) (SyncToken, error) {
	if raw == "" {
		return SyncToken{}, nil
	}
	if !strings.HasPrefix(raw, "s") {
		return SyncToken{}, ErrUnknownToken
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw[1:])
	if err != nil {
		return SyncToken{}, ErrUnknownToken
	}
	fields := strings.Split(string(decoded), ".")
	if len(fields) != tokenParts {
		return SyncToken{}, ErrUnknownToken
	}
	positions := make([]int64, tokenParts)
	for i, field := range fields {
		value, err := strconv.ParseInt(field, 36, 64)
		if err != nil || value < 0 {
			return SyncToken{}, ErrUnknownToken
		}
		positions[i] = value
	}
	return SyncToken{
		Events:      positions[0],
		AccountData: positions[1],
		Receipts:    positions[2],
		ToDevice:    positions[3],
		DeviceLists: positions[4],
		Typing:      positions[5],
	}, nil
}
