package ruleset

import "errors"

var (
	ErrNameRequired      = errors.New("ruleset: name is required")
	ErrArchived          = errors.New("ruleset: ruleset is archived")
	ErrNameAlreadyExists = errors.New("ruleset: name already exists")
)
