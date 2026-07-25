package user

import "errors"

var (
	ErrNameRequired = errors.New("user: name is required")
	ErrArchived     = errors.New("user: user is archived")
)
