package campaign

import "errors"

var (
	ErrNameRequired        = errors.New("campaign: name is required")
	ErrGamemastersRequired = errors.New("campaign: at least one gamemaster is required")
	ErrGamemasterNotFound  = errors.New("campaign: user is not a gamemaster of this campaign")
	ErrLastGamemaster      = errors.New("campaign: cannot remove the last gamemaster")
	ErrArchived            = errors.New("campaign: campaign is archived")
)
