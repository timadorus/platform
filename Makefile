.PHONY: build build-tools test lint generate migrate-up migrate-down dev-up dev-down

DATABASE_URL ?= postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable
MIGRATE      := go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1
BINDIR	     ?= $(CURDIR)/bin

# One migration directory per schema owner (plan §1) -> each gets its own
# schema_migrations tracking table, since they're independent version sequences that just
# happen to share a physical database, not one linear history.
SCHEMA_OWNERS := eventstore:internal/eventstore/postgres/migrations \
                 projection_checkpoint:internal/projection/checkpoint/migrations \
                 projection_universe:internal/projection/universe/migrations \
                 projection_user:internal/projection/user/migrations \
                 projection_campaign:internal/projection/campaign/migrations \
                 projection_entity:internal/projection/entity/migrations \
                 projection_character:internal/projection/character/migrations \
                 projection_object:internal/projection/object/migrations

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

migrate-up:
	@for owner in $(SCHEMA_OWNERS); do \
		name=$${owner%%:*}; path=$${owner#*:}; \
		echo "==> migrate up: $$name"; \
		$(MIGRATE) -database "$(DATABASE_URL)&x-migrations-table=schema_migrations_$$name" -source "file://$(CURDIR)/$$path" up; \
	done

migrate-down:
	@for owner in $(SCHEMA_OWNERS); do \
		name=$${owner%%:*}; path=$${owner#*:}; \
		echo "==> migrate down: $$name"; \
		$(MIGRATE) -database "$(DATABASE_URL)&x-migrations-table=schema_migrations_$$name" -source "file://$(CURDIR)/$$path" down 1; \
	done

dev-up:
	docker compose up -d

dev-down:
	docker compose down -v


$(BINDIR):
	mkdir -p $(BINDIR)