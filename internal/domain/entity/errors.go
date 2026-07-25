package entity

import "errors"

var (
	ErrNameRequired = errors.New("entity: name is required")
	ErrArchived     = errors.New("entity: entity is archived")
)
