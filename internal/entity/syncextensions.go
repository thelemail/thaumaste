package entity

import (
	"encoding/json"
	"errors"
	"strconv"
)

const (
	ExtensionToDevice    = "to_device"
	ExtensionE2EE        = "e2ee"
	ExtensionAccountData = "account_data"
	ExtensionReceipts    = "receipts"
	ExtensionTyping      = "typing"

	scopeEverything = "*"
)

type ExtensionScope struct {
	Enabled bool
	Lists   []string
	Rooms   []string
}

func (s ExtensionScope) Covers(roomID string, lists []string) bool {
	if len(s.Lists) == 0 && len(s.Rooms) == 0 {
		return true
	}
	for _, wanted := range s.Rooms {
		if wanted == scopeEverything || wanted == roomID {
			return true
		}
	}
	for _, wanted := range s.Lists {
		if wanted == scopeEverything {
			return true
		}
		for _, name := range lists {
			if name == wanted {
				return true
			}
		}
	}
	return false
}

type ToDeviceRequest struct {
	Enabled bool
	Limit   int
	Since   int64
}

type SyncExtensionRequest struct {
	ToDevice    ToDeviceRequest
	E2EE        ExtensionScope
	AccountData ExtensionScope
	Receipts    ExtensionScope
	Typing      ExtensionScope
}

type ToDeviceResult struct {
	NextBatch string
	Events    []json.RawMessage
}

type E2EEResult struct {
	DeviceLists    DeviceLists
	HasDeviceLists bool
	OneTimeKeys    map[string]int
	HasOneTimeKeys bool
	FallbackTypes  []string
	HasFallback    bool
}

type AccountDataResult struct {
	Global []json.RawMessage
	Rooms  map[string][]json.RawMessage
}

type SyncExtensions struct {
	ToDevice    *ToDeviceResult
	E2EE        *E2EEResult
	AccountData *AccountDataResult
	Receipts    map[string]json.RawMessage
	Typing      map[string]json.RawMessage
}

func (e SyncExtensions) Carries() bool {
	switch {
	case e.ToDevice != nil && len(e.ToDevice.Events) > 0:
		return true
	case e.E2EE != nil && e.E2EE.HasDeviceLists && !e.E2EE.DeviceLists.Empty():
		return true
	case e.AccountData != nil && (len(e.AccountData.Global) > 0 || len(e.AccountData.Rooms) > 0):
		return true
	case len(e.Receipts) > 0, len(e.Typing) > 0:
		return true
	}
	return false
}

var ErrExtensionMalformed = errors.New("entity: malformed extension configuration")

type extensionScopeBody struct {
	Enabled *bool     `json:"enabled"`
	Lists   *[]string `json:"lists"`
	Rooms   *[]string `json:"rooms"`
}

type toDeviceBody struct {
	Enabled *bool   `json:"enabled"`
	Limit   *int    `json:"limit"`
	Since   *string `json:"since"`
}

func ParseSyncExtensions(raw map[string]json.RawMessage) (SyncExtensionRequest, error) {
	var out SyncExtensionRequest

	if body, ok := raw[ExtensionToDevice]; ok {
		var decoded toDeviceBody
		if err := json.Unmarshal(body, &decoded); err != nil {
			return SyncExtensionRequest{}, ErrExtensionMalformed
		}
		out.ToDevice.Enabled = decoded.Enabled != nil && *decoded.Enabled
		out.ToDevice.Limit = DefaultToDeviceLimit
		if decoded.Limit != nil {
			out.ToDevice.Limit = *decoded.Limit
		}
		if out.ToDevice.Limit <= 0 || out.ToDevice.Limit > MaxToDeviceLimit {
			return SyncExtensionRequest{}, ErrExtensionMalformed
		}
		if decoded.Since != nil && *decoded.Since != "" {
			since, err := strconv.ParseInt(*decoded.Since, 10, 64)
			if err != nil || since < 0 {
				return SyncExtensionRequest{}, ErrToDeviceUnknown
			}
			out.ToDevice.Since = since
		}
	}

	for name, target := range map[string]*ExtensionScope{
		ExtensionE2EE:        &out.E2EE,
		ExtensionAccountData: &out.AccountData,
		ExtensionReceipts:    &out.Receipts,
		ExtensionTyping:      &out.Typing,
	} {
		body, ok := raw[name]
		if !ok {
			continue
		}
		var decoded extensionScopeBody
		if err := json.Unmarshal(body, &decoded); err != nil {
			return SyncExtensionRequest{}, ErrExtensionMalformed
		}
		target.Enabled = decoded.Enabled != nil && *decoded.Enabled
		if decoded.Lists != nil {
			target.Lists = *decoded.Lists
		}
		if decoded.Rooms != nil {
			target.Rooms = *decoded.Rooms
		}
	}
	return out, nil
}
