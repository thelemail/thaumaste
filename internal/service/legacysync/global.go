package legacysync

import (
	"context"
	"encoding/json"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *srv) global(ctx context.Context, sess *session, out *entity.LegacySyncResult) error {
	if err := s.accountData(ctx, sess, out); err != nil {
		return err
	}
	if err := s.toDevice(ctx, sess, out); err != nil {
		return err
	}
	return s.keys(ctx, sess, out)
}

func (s *srv) accountData(ctx context.Context, sess *session, out *entity.LegacySyncResult) error {
	found, err := s.stores.AccountData.Since(ctx, sess.scope, sess.caller, sess.since.AccountData)
	if err != nil {
		return err
	}
	global := sess.request.Filter.GlobalAccountData()
	for _, data := range found {
		if data.RoomID != "" || !global.KeepsType(data.Type) {
			continue
		}
		raw, err := marshalEvent(data.Type, json.RawMessage(data.Content))
		if err != nil {
			return err
		}
		out.AccountData = append(out.AccountData, raw)
	}
	return nil
}

func (s *srv) roomData(ctx context.Context, sess *session, entries []*roomDelivery) error {
	if len(entries) == 0 {
		return nil
	}
	found, err := s.stores.AccountData.Since(ctx, sess.scope, sess.caller, sess.since.AccountData)
	if err != nil {
		return err
	}
	byID := make(map[string]*roomDelivery, len(entries))
	for _, entry := range entries {
		byID[entry.room.RoomID] = entry
	}

	filter := sess.request.Filter.RoomAccountData()
	for _, data := range found {
		entry, ok := byID[data.RoomID]
		if !ok || !filter.KeepsType(data.Type) {
			continue
		}
		raw, err := marshalEvent(data.Type, json.RawMessage(data.Content))
		if err != nil {
			return err
		}
		entry.data = append(entry.data, raw)
	}
	return nil
}

func (s *srv) toDevice(ctx context.Context, sess *session, out *entity.LegacySyncResult) error {
	if sess.since.ToDevice > 0 {
		if err := s.stores.ToDevice.DeleteUpTo(ctx, sess.scope, sess.caller, sess.deviceID,
			sess.since.ToDevice); err != nil {
			return err
		}
	}
	messages, err := s.stores.ToDevice.Since(ctx, sess.scope, sess.caller, sess.deviceID,
		sess.since.ToDevice, entity.DefaultToDeviceLimit)
	if err != nil {
		return err
	}
	sess.upTo.ToDevice = sess.since.ToDevice
	for _, message := range messages {
		raw, err := json.Marshal(map[string]any{
			"sender":  message.Sender,
			"type":    message.Type,
			"content": json.RawMessage(message.Content),
		})
		if err != nil {
			return err
		}
		out.ToDevice = append(out.ToDevice, raw)
		sess.upTo.ToDevice = message.StreamID
	}
	return nil
}

func (s *srv) keys(ctx context.Context, sess *session, out *entity.LegacySyncResult) error {
	counts, err := s.stores.Keys.CountOneTime(ctx, sess.scope, sess.caller, sess.deviceID)
	if err != nil {
		return err
	}
	fallback, err := s.stores.Keys.UnusedFallbackAlgorithms(ctx, sess.scope, sess.caller, sess.deviceID)
	if err != nil {
		return err
	}
	out.OneTimeKeys = counts
	out.FallbackTypes = fallback
	if out.FallbackTypes == nil {
		out.FallbackTypes = []string{}
	}

	if sess.initial {
		return nil
	}
	lists, err := s.deviceLists.ChangedSince(ctx, sess.scope, sess.caller,
		sess.since.Cursors(), sess.upTo.Cursors())
	if err != nil {
		return err
	}
	if lists.Empty() {
		return nil
	}
	out.DeviceLists, out.HasDeviceList = lists, true
	return nil
}
