// Package command holds the hand-written OpenAPI spec for the write-side (command) API.
// Run `go generate ./...` to regenerate api/command/gen/server.gen.go after editing
// openapi.yaml.
package command

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config codegen.yaml openapi.yaml
