package entity

import (
	"errors"

	"github.com/google/uuid"
)

func notNilUUID(value any) error {
	id, ok := value.(uuid.UUID)
	if !ok || id == uuid.Nil {
		return errors.New("must not be empty")
	}
	return nil
}
