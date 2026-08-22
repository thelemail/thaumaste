package entity

import (
	"errors"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

const (
	DefaultDirectoryLimit       = 10
	MaxDirectorySearchTermBytes = 255
)

var ErrDirectoryTermRequired = errors.New("entity: a search term is required")

type DirectorySearch struct {
	Term  string
	Limit int
}

func (s DirectorySearch) Validate() error {
	if s.Term == "" {
		return ErrDirectoryTermRequired
	}
	return validation.ValidateStruct(&s,
		validation.Field(&s.Term, validation.Length(1, MaxDirectorySearchTermBytes)),
		validation.Field(&s.Limit, validation.Min(0)),
	)
}

type DirectoryResult struct {
	UserID      string
	DisplayName string
	AvatarURL   string
}

func (DirectoryResult) Validate() error { return nil }
