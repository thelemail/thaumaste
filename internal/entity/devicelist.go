package entity

import (
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

type DeviceLists struct {
	Changed []string
	Left    []string
}

func (d DeviceLists) Empty() bool { return len(d.Changed) == 0 && len(d.Left) == 0 }

func (DeviceLists) Validate() error { return nil }

type NewDeviceListChange struct {
	TenantID uuid.UUID
	UserID   string
}

func (n NewDeviceListChange) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
	)
}
