// Package registry is the single source of truth for "every projector this platform registers,
// split into Base and ChangeFeed" — both cmd/projector/main.go and cmd/rebuild-read-models use it,
// so there is never a second hardcoded projector list that could drift out of sync with the real
// one. This package imports internal/projection and every concrete projector package; none of
// them import this package back, so this stays a leaf in the import graph, same as
// cmd/projector/main.go's own registration list was before this refactor — internal/projection
// itself (the generic framework) is untouched and still knows nothing about any concrete
// projector, preserving its own "adding a projection never changes the framework" principle.
package registry

import (
	"github.com/timadorus/platform/internal/projection"
	campaignprojection "github.com/timadorus/platform/internal/projection/campaign"
	characterprojection "github.com/timadorus/platform/internal/projection/character"
	entityprojection "github.com/timadorus/platform/internal/projection/entity"
	objectprojection "github.com/timadorus/platform/internal/projection/object"
	rulesetprojection "github.com/timadorus/platform/internal/projection/ruleset"
	universeprojection "github.com/timadorus/platform/internal/projection/universe"
	universechangesprojection "github.com/timadorus/platform/internal/projection/universechanges"
	userprojection "github.com/timadorus/platform/internal/projection/user"
)

// Base returns the 7 base read-model projectors, one per aggregate type. A fresh slice of fresh
// instances every call — these are cheap, stateless constructors, and callers (cmd/projector,
// run once per process; cmd/rebuild-read-models, run once per invocation) never need to share an
// instance across calls.
func Base() []projection.Projector {
	return []projection.Projector{
		universeprojection.NewProjector(),
		userprojection.NewProjector(),
		campaignprojection.NewProjector(),
		entityprojection.NewProjector(),
		characterprojection.NewProjector(),
		objectprojection.NewProjector(),
		rulesetprojection.NewProjector(),
	}
}

// ChangeFeed returns the 5 universe-change-feed projectors (internal/projection/universechanges).
// A full read-model rebuild must reset Base before ChangeFeed, never the reverse — see
// docs/BACKLOG.md's "projector" section for why. Registration order in cmd/projector/main.go
// itself has no runtime ordering effect (the Router processes each projector's own subjects
// independently), so this split matters only for a full rebuild, not for normal operation.
func ChangeFeed() []projection.Projector {
	return []projection.Projector{
		universechangesprojection.NewUniverseProjector(),
		universechangesprojection.NewCampaignProjector(),
		universechangesprojection.NewEntityProjector(),
		universechangesprojection.NewObjectProjector(),
		universechangesprojection.NewCharacterProjector(),
	}
}

// All returns every registered projector, Base then ChangeFeed.
func All() []projection.Projector {
	return append(Base(), ChangeFeed()...)
}
