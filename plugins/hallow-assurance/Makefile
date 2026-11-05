PKGS := ./cmd/assure ./adapters/go/assure-adapter-go ./adapters/typescript/assure-adapter-typescript ./adapters/python/assure-adapter-python
BINS := assure assure-adapter-go assure-adapter-typescript assure-adapter-python
# Hooks run with the PATH Claude Code launched with, so both the mise toolchain's GOBIN and
# $HOME/go/bin must carry the same build; a stale copy in either one wins depending on PATH order.
GOBINS := $(sort $(HOME)/go/bin $(shell go env GOBIN))

GOFILES = $(shell git ls-files '*.go')

.PHONY: help install verify nice vuln lint vet fmt fix test mod-verify tidy

.DEFAULT_GOAL := help

help: ## Show available commands
	@echo "hallow-assurance - Development Commands"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

install: ## Install assure and adapters to $HOME/go/bin and mise's GOBIN, then verify
	@for d in $(GOBINS); do \
		echo "go install -> $$d"; \
		GOBIN=$$d go install $(PKGS) || exit 1; \
	done
	@$(MAKE) --no-print-directory verify
	@echo "✓ Installed $(BINS)"

# A mise shim is a symlink to the mise executable, so hashing it never matches;
# verify hashes the binary the shim dispatches to (`mise which`) instead.
verify: ## Check every assure binary on PATH matches $HOME/go/bin
	@status=0; \
	for b in $(BINS); do \
		want=$$(shasum -a 256 $(HOME)/go/bin/$$b | cut -d' ' -f1); \
		found=$$(which -a $$b) || { echo "$$b: not on PATH"; status=1; continue; }; \
		for got in $$found; do \
			case "$$got" in */mise/shims/*) \
				shim=$$got; \
				got=$$(mise which $$b 2>/dev/null) || { echo "$$b: mise shim $$shim resolves to no binary"; status=1; continue; }; \
			esac; \
			if [ "$$(shasum -a 256 $$got | cut -d' ' -f1)" = "$$want" ]; then \
				echo "$$b: $$got"; \
			else \
				echo "$$b: $$got is stale (differs from $(HOME)/go/bin/$$b)"; status=1; \
			fi; \
		done; \
	done; \
	exit $$status

nice: vuln lint vet fmt fix test mod-verify tidy ## Run every check and formatter

vuln: ## Run govulncheck
	@command -v govulncheck >/dev/null 2>&1 || GOTOOLCHAIN=auto go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

lint: ## Run golangci-lint
	golangci-lint run

vet: ## Run go vet
	go vet ./...

fmt: tidy ## Format tracked Go files (gofmt -s, interface{}->any, goimports)
	@echo "gofmt -s -w <tracked .go files>"
	@gofmt -s -w $(GOFILES)
	@gofmt -w -r 'interface{} -> any' $(GOFILES)
	@command -v goimports >/dev/null 2>&1 && goimports -w -local github.com/hallow-inc/claude-plugins/plugins/hallow-assurance $(GOFILES) || true

fix: ## Apply go fix rewrites
	go fix ./...

test: ## Run tests with race detector and shuffle
	go test -race -shuffle=on ./...

mod-verify: ## Download and verify module dependencies
	go mod download
	go mod verify

tidy: ## Tidy go.mod
	go mod tidy
