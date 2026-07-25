package universe

import "errors"

var (
	ErrNameRequired     = errors.New("universe: name is required")
	ErrCreatorsRequired = errors.New("universe: at least one creator is required")
	ErrCreatorNotFound  = errors.New("universe: user is not a creator of this universe")
	ErrLastCreator      = errors.New("universe: cannot remove the last creator")
	ErrArchived         = errors.New("universe: universe is archived")
)
