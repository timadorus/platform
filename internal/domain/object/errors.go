package object

import "errors"

var (
	ErrNameRequired = errors.New("object: name is required")
	ErrArchived     = errors.New("object: object is archived")
)
