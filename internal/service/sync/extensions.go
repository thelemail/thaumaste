package sync

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *srv) ceilings(ctx context.Context, sess *session) error {
	sess.ceilings.Events = sess.ceiling

	var err error
	if sess.ceilings.AccountData, err = s.streams.AccountData.Published(ctx); err != nil {
		return err
	}
	if sess.ceilings.Receipts, err = s.streams.Receipts.Published(ctx); err != nil {
		return err
	}
	if sess.ceilings.DeviceLists, err = s.streams.DeviceLists.Published(ctx); err != nil {
		return err
	}
	if sess.ceilings.Typing, err = s.stores.Typing.Version(ctx, sess.scope); err != nil {
		return err
	}
	return nil
}

func (s *srv) quiet(sess *session) entity.SyncExtensions {
	var out entity.SyncExtensions
	if sess.wanted.ToDevice.Enabled {
		out.ToDevice = &entity.ToDeviceResult{
			NextBatch: formatBatch(parseBatch(sess.wanted.ToDevice.Since)),
		}
	}
	return out
}

func (s *srv) extensions(ctx context.Context, sess *session) (entity.SyncExtensions, error) {
	var out entity.SyncExtensions

	if sess.wanted.ToDevice.Enabled {
		batch, err := s.toDevice(ctx, sess)
		if err != nil {
			return entity.SyncExtensions{}, err
		}
		out.ToDevice = batch
	}
	if sess.wanted.E2EE.Enabled {
		keys, err := s.e2ee(ctx, sess)
		if err != nil {
			return entity.SyncExtensions{}, err
		}
		out.E2EE = keys
	}

	if sess.wanted.AccountData.Enabled {
		data, err := s.accountData(ctx, sess)
		if err != nil {
			return entity.SyncExtensions{}, err
		}
		out.AccountData = data
	}
	if sess.wanted.Receipts.Enabled {
		receipts, err := s.receipts(ctx, sess)
		if err != nil {
			return entity.SyncExtensions{}, err
		}
		out.Receipts = receipts
	}
	if sess.wanted.Typing.Enabled {
		typing, err := s.typing(ctx, sess)
		if err != nil {
			return entity.SyncExtensions{}, err
		}
		out.Typing = typing
	}
	return out, nil
}

func (s *srv) toDevice(ctx context.Context, sess *session) (*entity.ToDeviceResult, error) {
	since := parseBatch(sess.wanted.ToDevice.Since)
	if since > 0 {
		if err := s.stores.ToDevice.DeleteUpTo(ctx, sess.scope, sess.caller, sess.deviceID, since); err != nil {
			return nil, err
		}
	}

	messages, err := s.stores.ToDevice.Since(ctx, sess.scope, sess.caller, sess.deviceID,
		since, sess.wanted.ToDevice.Limit)
	if err != nil {
		return nil, err
	}

	out := &entity.ToDeviceResult{NextBatch: formatBatch(since)}
	for _, message := range messages {
		raw, err := json.Marshal(map[string]any{
			"sender":  message.Sender,
			"type":    message.Type,
			"content": json.RawMessage(message.Content),
		})
		if err != nil {
			return nil, err
		}
		out.Events = append(out.Events, raw)
		out.NextBatch = formatBatch(message.StreamID)
	}
	return out, nil
}

func (s *srv) e2ee(ctx context.Context, sess *session) (*entity.E2EEResult, error) {
	out := &entity.E2EEResult{}

	counts, err := s.stores.Keys.CountOneTime(ctx, sess.scope, sess.caller, sess.deviceID)
	if err != nil {
		return nil, err
	}
	fallback, err := s.stores.Keys.UnusedFallbackAlgorithms(ctx, sess.scope, sess.caller, sess.deviceID)
	if err != nil {
		return nil, err
	}
	out.OneTimeKeys, out.HasOneTimeKeys = counts, true
	out.FallbackTypes, out.HasFallback = fallback, true
	if out.FallbackTypes == nil {
		out.FallbackTypes = []string{}
	}

	since := sess.connection.ConfirmedCursors.DeviceLists
	if since >= sess.ceilings.DeviceLists {
		return out, nil
	}
	changed, err := s.stores.DeviceLists.ChangedSince(ctx, sess.scope, since, sess.ceilings.DeviceLists)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return out, nil
	}
	visible, err := s.members.SharedWith(ctx, sess.scope, sess.caller, changed)
	if err != nil {
		return nil, err
	}
	sort.Strings(visible)

	seen := map[string]bool{}
	for _, userID := range visible {
		seen[userID] = true
	}
	lists := entity.DeviceLists{Changed: visible}
	for _, userID := range changed {
		if !seen[userID] && userID != sess.caller {
			lists.Left = append(lists.Left, userID)
		}
	}
	sort.Strings(lists.Left)
	out.DeviceLists, out.HasDeviceLists = lists, true
	return out, nil
}

func (s *srv) accountData(ctx context.Context, sess *session) (*entity.AccountDataResult, error) {
	since := sess.connection.ConfirmedCursors.AccountData
	found, err := s.stores.AccountData.Since(ctx, sess.scope, sess.caller, since)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, nil
	}

	out := &entity.AccountDataResult{Rooms: map[string][]json.RawMessage{}}
	for _, data := range found {
		raw, err := json.Marshal(map[string]any{
			"type":    data.Type,
			"content": json.RawMessage(data.Content),
		})
		if err != nil {
			return nil, err
		}
		if data.RoomID == "" {
			out.Global = append(out.Global, raw)
			continue
		}
		if !sess.wanted.AccountData.Covers(data.RoomID, nil) {
			continue
		}
		out.Rooms[data.RoomID] = append(out.Rooms[data.RoomID], raw)
	}
	if len(out.Global) == 0 && len(out.Rooms) == 0 {
		return nil, nil
	}
	return out, nil
}

func (s *srv) receipts(ctx context.Context, sess *session) (map[string]json.RawMessage, error) {
	roomNIDs := sess.inScope()
	if len(roomNIDs) == 0 {
		return nil, nil
	}
	found, err := s.stores.Receipts.Since(ctx, sess.scope, roomNIDs, sess.caller,
		sess.connection.ConfirmedCursors.Receipts)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, nil
	}

	byRoom := map[string]map[string]map[string]map[string]any{}
	for _, receipt := range found {
		if !sess.wanted.Receipts.Covers(receipt.RoomID, nil) {
			continue
		}
		events := byRoom[receipt.RoomID]
		if events == nil {
			events = map[string]map[string]map[string]any{}
			byRoom[receipt.RoomID] = events
		}
		types := events[receipt.EventID]
		if types == nil {
			types = map[string]map[string]any{}
			events[receipt.EventID] = types
		}
		users := types[receipt.Type]
		if users == nil {
			users = map[string]any{}
			types[receipt.Type] = users
		}
		entry := map[string]any{"ts": receipt.Timestamp}
		if receipt.ThreadID != entity.ThreadUnthreaded {
			entry["thread_id"] = receipt.ThreadID
		}
		users[receipt.UserID] = entry
	}

	out := make(map[string]json.RawMessage, len(byRoom))
	for roomID, content := range byRoom {
		raw, err := json.Marshal(map[string]any{"type": entity.EventTypeReceipt, "content": content})
		if err != nil {
			return nil, err
		}
		out[roomID] = raw
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (s *srv) typing(ctx context.Context, sess *session) (map[string]json.RawMessage, error) {
	roomNIDs := sess.inScope()
	if sess.connection.ConfirmedCursors.Typing == sess.ceilings.Typing || len(roomNIDs) == 0 {
		return nil, nil
	}
	sets, err := s.stores.Typing.ChangedSince(ctx, sess.scope, roomNIDs,
		sess.connection.ConfirmedCursors.Typing, s.clock().UTC())
	if err != nil {
		return nil, err
	}

	rooms, err := s.members.ListForSync(ctx, sess.scope, sess.caller)
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for _, room := range rooms {
		if _, scoped := sets[room.RoomNID]; !scoped {
			continue
		}
		if !sess.wanted.Typing.Covers(room.RoomID, nil) {
			continue
		}
		users, changed := sets[room.RoomNID]
		if !changed {
			continue
		}
		sort.Strings(users)
		raw, err := json.Marshal(map[string]any{
			"type":    entity.EventTypeTyping,
			"content": map[string]any{"user_ids": users},
		})
		if err != nil {
			return nil, err
		}
		out[room.RoomID] = raw
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (sess *session) inScope() []int64 {
	seen := make(map[int64]struct{}, len(sess.known)+len(sess.delivered))
	for roomNID := range sess.known {
		seen[roomNID] = struct{}{}
	}
	for roomNID := range sess.delivered {
		seen[roomNID] = struct{}{}
	}
	out := make([]int64, 0, len(seen))
	for roomNID := range seen {
		out = append(out, roomNID)
	}
	return out
}

func parseBatch(raw string) int64 {
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func formatBatch(position int64) string { return strconv.FormatInt(position, 10) }
