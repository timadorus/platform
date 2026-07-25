.PHONY: build test lint generate migrate-up migrate-down dev-up dev-down

DATABASE_URL ?= postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable
MIGRATE      := go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1

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

test:
	go test ./...

lint:
	go vet ./...
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run

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
