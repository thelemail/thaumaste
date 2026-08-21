package entity

import (
	"errors"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

var ErrTransactionMissing = errors.New("entity: a transaction id is required")

type NewMessage struct {
	RoomID   string
	Type     string
	Sender   string
	DeviceID string
	TxnID    string
	Content  map[string]any
}

func (n NewMessage) Validate() error {
	if n.TxnID == "" {
		return ErrTransactionMissing
	}
	return validation.ValidateStruct(&n,
		validation.Field(&n.RoomID, validation.Required, validation.Length(1, MaxRoomIDBytes)),
		validation.Field(&n.Type, validation.Required, validation.Length(1, MaxEventTypeSize)),
		validation.Field(&n.Sender, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&n.TxnID, validation.Length(1, MaxTransactionIDBytes)),
	)
}

func (n NewMessage) Event() NewEvent {
	return NewEvent{
		RoomID:  n.RoomID,
		Type:    n.Type,
		Sender:  n.Sender,
		Content: n.Content,
		Txn: &TransactionRef{
			DeviceID: n.DeviceID,
			Endpoint: EndpointSend,
			TxnID:    n.TxnID,
		},
	}
}
