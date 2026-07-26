.PHONY: build build-tools test lint generate migrate-up migrate-down test-e2e dev-up dev-down

DATABASE_URL ?= postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable
BINDIR	     ?= $(CURDIR)/bin

build:
	go build ./...

build-tools: $(BINDIR)
	go build -o $(BINDIR)/timadorusctl ./cmd/timadorusctl

test:
	go test ./...

lint:
	go vet ./...
	# Best-effort: no golangci-lint release yet supports analyzing a `go 1.26` module (hard
	# failure in go/types on older analyzing toolchains, not a config issue) — don't fail the
	# build on it, `go vet` above is the enforced check until golangci-lint catches up.
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run || true

generate:
	go generate ./...

# scripts/migrate-up.sh holds the schema-owner list (plan §1: one migration directory per
# schema owner, each with its own schema_migrations tracking table) so the Helm chart's
# migration Job (deploy/helm/timadorus-platform) can share it instead of duplicating it.
migrate-up:
	DATABASE_URL="$(DATABASE_URL)" MIGRATIONS_BASE="$(CURDIR)" ./scripts/migrate-up.sh up

migrate-down:
	DATABASE_URL="$(DATABASE_URL)" MIGRATIONS_BASE="$(CURDIR)" ./scripts/migrate-up.sh down

test-e2e:
	go test -tags e2e -count=1 ./test/e2e/... -v -timeout 30m

dev-up:
	docker compose up -d

dev-down:
	docker compose down -v


$(BINDIR):
	mkdir -p $(BINDIR)
