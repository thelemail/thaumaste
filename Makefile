SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help env infra infra-down infra-reset psql build run gen test lint lint-fix

help:
	@printf "%s\n" \
	  "env        create .env from .env.example" \
	  "infra      start postgres" \
	  "infra-down stop postgres, keep the volume" \
	  "psql       psql shell against local postgres" \
	  "build      build the binary into bin/thaumaste" \
	  "run        run the homeserver" \
	  "gen        go generate ./..." \
	  "test       go test ./... -race -count=1" \
	  "lint       golangci-lint run ./..."

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

build:
	@mkdir -p bin
	go build -o bin/thaumaste .

run:
	go run . serve

gen:
	go generate ./...

test:
	go test ./... -race -count=1

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run ./... --fix
