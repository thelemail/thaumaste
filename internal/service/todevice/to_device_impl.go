package todevice

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/notify"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	messages repository.ToDevice
	devices  repository.Device
	tx       repository.Transactor
	stream   *postgres.Stream
	notifier *notify.Notifier
}

func New(messages repository.ToDevice, devices repository.Device, tx repository.Transactor,
	stream *postgres.Stream, notifier *notify.Notifier,
) service.ToDevice {
	return &srv{messages: messages, devices: devices, tx: tx, stream: stream, notifier: notifier}
}

func (s *srv) Send(ctx context.Context, scope entity.TenantScope, in entity.ToDeviceSend) error {
	if err := in.Validate(); err != nil {
		return err
	}

	spent, err := s.messages.Recorded(ctx, scope, in.Sender, in.DeviceID, in.TxnID)
	if err != nil {
		return err
	}
	if spent {
		return nil
	}

	queued, err := s.expand(ctx, scope, in)
	if err != nil {
		return err
	}
	if len(queued) == 0 {
		return s.tx.WithTx(ctx, func(ctx context.Context) error {
			return s.messages.Record(ctx, scope, in.Sender, in.DeviceID, in.TxnID)
		})
	}

	positions, err := s.stream.Next(ctx, len(queued))
	if err != nil {
		return err
	}
	defer positions.Release()

	if err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.messages.Add(ctx, queued, positions.IDs); err != nil {
			return err
		}
		return s.messages.Record(ctx, scope, in.Sender, in.DeviceID, in.TxnID)
	}); err != nil {
		return err
	}

	keys := make([]string, 0, len(queued))
	for _, message := range queued {
		keys = append(keys, entity.DeviceWakeKey(message.UserID, message.DeviceID))
	}
	s.notifier.Notify(ctx, keys...)
	return nil
}

func (s *srv) expand(ctx context.Context, scope entity.TenantScope,
	in entity.ToDeviceSend,
) ([]entity.NewToDeviceMessage, error) {
	users := make([]string, 0, len(in.Messages))
	for userID := range in.Messages {
		users = append(users, userID)
	}
	sort.Strings(users)

	var out []entity.NewToDeviceMessage
	for _, userID := range users {
		targets := in.Messages[userID]
		names := make([]string, 0, len(targets))
		for deviceID := range targets {
			names = append(names, deviceID)
		}
		sort.Strings(names)

		for _, deviceID := range names {
			content := targets[deviceID]
			if deviceID != entity.AllDevices {
				message, err := s.queue(scope, in, userID, deviceID, content)
				if err != nil {
					return nil, err
				}
				out = append(out, message)
				continue
			}
			known, err := s.devices.ListForUser(ctx, scope, userID)
			if err != nil {
				return nil, err
			}
			for _, device := range known {
				message, err := s.queue(scope, in, userID, device.DeviceID, content)
				if err != nil {
					return nil, err
				}
				out = append(out, message)
			}
		}
	}
	return out, nil
}

func (s *srv) queue(scope entity.TenantScope, in entity.ToDeviceSend, userID, deviceID string,
	content json.RawMessage,
) (entity.NewToDeviceMessage, error) {
	message := entity.NewToDeviceMessage{
		TenantID: scope.ID(),
		UserID:   userID,
		DeviceID: deviceID,
		Sender:   in.Sender,
		Type:     in.Type,
		Content:  content,
	}
	if err := message.Validate(); err != nil {
		return entity.NewToDeviceMessage{}, err
	}
	return message, nil
}

func (s *srv) Sweep(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.messages.DeleteBefore(ctx, cutoff)
}
