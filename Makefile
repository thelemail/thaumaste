SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help env build run gen test lint lint-fix

help:
	@printf "%s\n" \
	  "env        create .env from .env.example" \
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
