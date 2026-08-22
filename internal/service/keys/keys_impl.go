package keys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	deviceLists service.DeviceLists
	keys        repository.Key
	members     repository.RoomMember
	tx          repository.Transactor
	cfg         config.Keys
}

func New(keys repository.Key, members repository.RoomMember, tx repository.Transactor,
	deviceLists service.DeviceLists, cfg config.Keys,
) service.Keys {
	return &srv{keys: keys, members: members, tx: tx, deviceLists: deviceLists, cfg: cfg}
}

func (s *srv) Upload(ctx context.Context, scope entity.TenantScope, in entity.KeyUpload) (map[string]int, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	oneTime, err := in.OneTime()
	if err != nil {
		return nil, err
	}
	fallback, err := in.Fallback()
	if err != nil {
		return nil, err
	}

	var counts map[string]int
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.keys.Lock(ctx, scope, in.UserID, in.DeviceID); err != nil {
			return err
		}
		if len(in.DeviceKeys) > 0 {
			canonical, err := entity.CanonicalJSONFrom(in.DeviceKeys)
			if err != nil {
				return entity.ErrDeviceKeyMalformed
			}
			device := entity.NewDeviceKey{
				TenantID: scope.ID(), UserID: in.UserID, DeviceID: in.DeviceID, KeyJSON: canonical,
			}
			if err := device.Validate(); err != nil {
				return err
			}
			if err := s.keys.UpsertDevice(ctx, device); err != nil {
				return err
			}
		}
		if err := s.addOneTime(ctx, scope, in, oneTime); err != nil {
			return err
		}
		if err := s.setFallback(ctx, scope, in, fallback); err != nil {
			return err
		}
		counts, err = s.keys.CountOneTime(ctx, scope, in.UserID, in.DeviceID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(in.DeviceKeys) > 0 && s.deviceLists != nil {
		if err := s.deviceLists.Record(ctx, scope, in.UserID); err != nil {
			return nil, err
		}
	}
	return counts, nil
}

func (s *srv) addOneTime(ctx context.Context, scope entity.TenantScope, in entity.KeyUpload,
	keys map[entity.KeyIdentifier]json.RawMessage,
) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]entity.KeyIdentifier, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].Algorithm != ids[j].Algorithm {
			return ids[i].Algorithm < ids[j].Algorithm
		}
		return ids[i].KeyID < ids[j].KeyID
	})

	existing, err := s.keys.ExistingOneTime(ctx, scope, in.UserID, in.DeviceID, ids)
	if err != nil {
		return err
	}

	fresh := make([]entity.NewOneTimeKey, 0, len(ids))
	for _, id := range ids {
		canonical, err := entity.CanonicalJSONFrom(keys[id])
		if err != nil {
			return entity.ErrDeviceKeyMalformed
		}
		if held, ok := existing[id]; ok {
			same, err := sameMaterial(held, canonical)
			if err != nil {
				return err
			}
			if !same {
				return entity.ErrOneTimeKeyConflict
			}
			continue
		}
		key := entity.NewOneTimeKey{
			TenantID: scope.ID(), UserID: in.UserID, DeviceID: in.DeviceID, KeyID: id, KeyJSON: canonical,
		}
		if err := key.Validate(); err != nil {
			return err
		}
		fresh = append(fresh, key)
	}
	if len(fresh) == 0 {
		return nil
	}

	counts, err := s.keys.CountOneTime(ctx, scope, in.UserID, in.DeviceID)
	if err != nil {
		return err
	}
	held := 0
	for _, count := range counts {
		held += count
	}
	if s.cfg.MaxOneTimeKeys > 0 && held+len(fresh) > s.cfg.MaxOneTimeKeys {
		return entity.ErrTooManyOneTimeKeys
	}
	return s.keys.AddOneTime(ctx, fresh)
}

func (s *srv) setFallback(ctx context.Context, scope entity.TenantScope, in entity.KeyUpload,
	keys map[entity.KeyIdentifier]json.RawMessage,
) error {
	ids := make([]entity.KeyIdentifier, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Algorithm < ids[j].Algorithm })

	for _, id := range ids {
		canonical, err := entity.CanonicalJSONFrom(keys[id])
		if err != nil {
			return entity.ErrDeviceKeyMalformed
		}
		key := entity.NewFallbackKey{
			TenantID: scope.ID(), UserID: in.UserID, DeviceID: in.DeviceID, KeyID: id, KeyJSON: canonical,
		}
		if err := key.Validate(); err != nil {
			return err
		}
		if err := s.keys.SetFallback(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func sameMaterial(held, offered []byte) (bool, error) {
	a, err := entity.KeyMaterial(held)
	if err != nil {
		return false, err
	}
	b, err := entity.KeyMaterial(offered)
	if err != nil {
		return false, err
	}
	return bytes.Equal(a, b), nil
}

func (s *srv) Query(ctx context.Context, scope entity.TenantScope, caller string,
	in entity.KeyQuery,
) (map[string]map[string]entity.DeviceKey, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if s.cfg.MaxQueryUsers > 0 && len(in.Devices) > s.cfg.MaxQueryUsers {
		return nil, entity.ErrTooManyKeyTargets
	}

	wanted := make([]string, 0, len(in.Devices))
	for userID := range in.Devices {
		if _, _, err := entity.ParseUserID(userID); err != nil {
			continue
		}
		wanted = append(wanted, userID)
	}
	sort.Strings(wanted)

	visible, err := s.visible(ctx, scope, caller, wanted)
	if err != nil {
		return nil, err
	}

	out := make(map[string]map[string]entity.DeviceKey, len(wanted))
	for _, userID := range wanted {
		out[userID] = map[string]entity.DeviceKey{}
	}
	if len(visible) == 0 {
		return out, nil
	}

	found, err := s.keys.ListDevices(ctx, scope, visible)
	if err != nil {
		return nil, err
	}
	for _, key := range found {
		if !wants(in.Devices[key.UserID], key.DeviceID) {
			continue
		}
		out[key.UserID][key.DeviceID] = key
	}
	return out, nil
}

func (s *srv) visible(ctx context.Context, scope entity.TenantScope, caller string, wanted []string) ([]string, error) {
	others := make([]string, 0, len(wanted))
	out := make([]string, 0, len(wanted))
	for _, userID := range wanted {
		if userID == caller {
			out = append(out, userID)
			continue
		}
		others = append(others, userID)
	}
	shared, err := s.members.SharedWith(ctx, scope, caller, others)
	if err != nil {
		return nil, err
	}
	return append(out, shared...), nil
}

func wants(devices []string, deviceID string) bool {
	if len(devices) == 0 {
		return true
	}
	for _, wanted := range devices {
		if wanted == deviceID {
			return true
		}
	}
	return false
}

func (s *srv) Claim(ctx context.Context, scope entity.TenantScope, in entity.KeyClaim) ([]entity.ClaimedKey, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	targets := 0
	users := make([]string, 0, len(in.Devices))
	for userID, devices := range in.Devices {
		users = append(users, userID)
		targets += len(devices)
	}
	if s.cfg.MaxClaimDevices > 0 && targets > s.cfg.MaxClaimDevices {
		return nil, entity.ErrTooManyKeyTargets
	}
	sort.Strings(users)

	out := make([]entity.ClaimedKey, 0, targets)
	for _, userID := range users {
		devices := make([]string, 0, len(in.Devices[userID]))
		for deviceID := range in.Devices[userID] {
			devices = append(devices, deviceID)
		}
		sort.Strings(devices)

		for _, deviceID := range devices {
			claimed, err := s.claim(ctx, scope, userID, deviceID, in.Devices[userID][deviceID])
			if err != nil {
				if errors.Is(err, repository.ErrKeyNotFound) {
					continue
				}
				return nil, err
			}
			out = append(out, claimed)
		}
	}
	return out, nil
}

func (s *srv) claim(ctx context.Context, scope entity.TenantScope, userID, deviceID, algorithm string) (entity.ClaimedKey, error) {
	claimed, err := s.keys.ClaimOneTime(ctx, scope, userID, deviceID, algorithm)
	if err == nil {
		return claimed, nil
	}
	if !errors.Is(err, repository.ErrKeyNotFound) {
		return entity.ClaimedKey{}, err
	}
	return s.keys.ClaimFallback(ctx, scope, userID, deviceID, algorithm)
}

func (s *srv) FallbackAlgorithms(ctx context.Context, scope entity.TenantScope, userID, deviceID string) ([]string, error) {
	return s.keys.UnusedFallbackAlgorithms(ctx, scope, userID, deviceID)
}
