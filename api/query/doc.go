// Package query holds the hand-written OpenAPI spec for the read-side (query) API. Run
// `go generate ./...` to regenerate api/query/gen/server.gen.go after editing openapi.yaml.
package query

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config codegen.yaml openapi.yaml
