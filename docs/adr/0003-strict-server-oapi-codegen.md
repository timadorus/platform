# 0003: oapi-codegen strict-server + gorilla-server generation

## Status
Accepted

## Context
Both command-api and query-api are spec-first OpenAPI 3 services. We need generated server
code that makes it hard for a handler to drift from the spec.

## Decision
Generate with oapi-codegen's `strict-server: true` + `gorilla-server: true` + `embedded-spec: true`.
Strict-server generation gives each handler a typed request struct in and a typed union
response type out, so the compiler rejects a handler that returns a response shape the spec
doesn't define. gorilla/mux is the router target (see plan §1); `nethttp-middleware`'s
`OapiRequestValidatorWithOptions` still applies via `mux.Router.Use` since gorilla/mux
middleware is standard `func(http.Handler) http.Handler`.

## Consequences
Handlers implement `StrictServerInterface`, not the raw `ServerInterface` — slightly more
boilerplate per handler in exchange for compile-time contract enforcement.
