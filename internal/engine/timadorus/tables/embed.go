package tables

import "embed"

// dataFiles embeds every YAML table file shipped in this directory, so the engine binary stays
// self-contained (no runtime file access needed) — matches this codebase's existing
// embedded-OpenAPI-spec convention (api/query/doc.go, api/command/doc.go).
//
//go:embed *.yaml
var dataFiles embed.FS
