SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

ifneq (,$(wildcard .env))
include .env
export
endif

GOOSE := go tool goose
MIGRATIONS_DIR := db/migrations/postgres

.PHONY: db-gen migrate-create migrate-up migrate-down migrate-status help env infra infra-down infra-reset psql build run gen test lint lint-fix complement complement-all complement-report

help:
	@printf "%s\n" \
	  "env        create .env from .env.example" \
	  "infra      start postgres" \
	  "infra-down stop postgres, keep the volume" \
	  "psql       psql shell against local postgres" \
	  "migrate-create name=<snake_name>   new migration file" \
	  "migrate-up / migrate-down / migrate-status" \
	  "build      build the binary into bin/thaumaste" \
	  "run        run the homeserver" \
	  "gen        go generate ./..." \
	  "db-gen     regenerate the sqlboiler models from the live schema" \
	  "test       go test ./... -race -count=1" \
	  "lint       golangci-lint run ./..." \
	  "complement          run the complement allowlist" \
	  "complement-all      run the whole complement csapi suite" \
	  "complement-report   regenerate complement/COVERAGE.md"

env:
	@if [ -f .env ]; then \
		echo ".env already exists; not overwriting"; \
	else \
		cp .env.example .env && echo "Created .env from .env.example."; \
	fi

infra:
	docker compose up -d postgres

infra-down:
	docker compose down

infra-reset:
	@read -r -p "This destroys all local data. Type 'yes' to continue: " ans; \
	if [ "$$ans" = "yes" ]; then docker compose down -v; else echo "aborted"; fi

psql:
	docker compose exec postgres psql -U $(THAUMASTE_POSTGRES_USER) -d $(THAUMASTE_POSTGRES_DB)

migrate-create:
	@if [ -z "$(name)" ]; then \
		echo "usage: make migrate-create name=<snake_name>" >&2; exit 1; \
	fi
	@mkdir -p $(MIGRATIONS_DIR)
	$(GOOSE) -dir $(MIGRATIONS_DIR) create $(name) sql

migrate-up:
	go run . migrate up

migrate-down:
	go run . migrate down

migrate-status:
	go run . migrate status

build:
	@mkdir -p bin
	go build -o bin/thaumaste .

run:
	go run . serve

gen:
	go generate ./...

db-gen:
	@mkdir -p bin
	go build -o bin/sqlboiler-psql github.com/aarondl/sqlboiler/v4/drivers/sqlboiler-psql
	PATH="$(CURDIR)/bin:$$PATH" go tool sqlboiler psql -c sqlboiler.toml

test:
	go test ./... -race -count=1

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run ./... --fix

complement:
	./scripts/complement.sh allowlist

complement-all:
	./scripts/complement.sh full

complement-report:
	./scripts/complement.sh report
