// Package apperrors holds the small, shared vocabulary of cross-aggregate validation errors
// (plan §4.3): every command service that references a different aggregate type (Campaign
// validating its parent Universe, Character validating its parent Campaign and Player User,
// etc.) reports failures using these same four errors, so internal/httpapi/command/errors.go
// has one place to map them to HTTP status codes instead of each aggregate reinventing its
// own equivalents.
package apperrors

import "errors"

var (
	// ErrParentNotFound means the immutable parent aggregate referenced at creation (e.g.
	// Campaign's Universe) doesn't exist.
	ErrParentNotFound = errors.New("apperrors: parent aggregate not found")
	// ErrParentArchived means the parent aggregate exists but is archived, so it can't be
	// used to form a new child (plan §4.6).
	ErrParentArchived = errors.New("apperrors: parent aggregate is archived")
	// ErrReferenceNotFound means a non-parent cross-aggregate reference (e.g. a Gamemaster
	// or Player User id) doesn't exist.
	ErrReferenceNotFound = errors.New("apperrors: referenced aggregate not found")
	// ErrReferenceArchived means a non-parent cross-aggregate reference exists but is
	// archived, so it can't be used to form a new relationship (plan §4.6).
	ErrReferenceArchived = errors.New("apperrors: referenced aggregate is archived")
)
