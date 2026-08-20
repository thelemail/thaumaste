SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help build run gen test lint lint-fix

help:
	@printf "%s\n" \
	  "build      build the binary into bin/thaumaste" \
	  "run        run the homeserver" \
	  "gen        go generate ./..." \
	  "test       go test ./... -race -count=1" \
	  "lint       golangci-lint run ./..."

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
