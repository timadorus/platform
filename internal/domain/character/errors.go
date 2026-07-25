package character

import "errors"

var (
	ErrNameRequired   = errors.New("character: name is required")
	ErrPlayerRequired = errors.New("character: player is required")
	ErrArchived       = errors.New("character: character is archived")
)
