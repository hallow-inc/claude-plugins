PKGS := ./cmd/assure ./adapters/go/assure-adapter-go ./adapters/typescript/assure-adapter-typescript ./adapters/python/assure-adapter-python
BINS := assure assure-adapter-go assure-adapter-typescript assure-adapter-python
# Hooks run with the PATH Claude Code launched with, so both the mise toolchain's GOBIN and
# $HOME/go/bin must carry the same build; a stale copy in either one wins depending on PATH order.
GOBINS := $(sort $(HOME)/go/bin $(shell go env GOBIN))

GOFILES = $(shell git ls-files '*.go')

.PHONY: install verify nice vuln lint vet fmt fix test mod-verify tidy

install:
	@for d in $(GOBINS); do \
		echo "go install -> $$d"; \
		GOBIN=$$d go install $(PKGS) || exit 1; \
	done
	@$(MAKE) --no-print-directory verify

verify:
	@status=0; \
	for b in $(BINS); do \
		want=$$(shasum -a 256 $(HOME)/go/bin/$$b | cut -d' ' -f1); \
		found=$$(which -a $$b) || { echo "$$b: not on PATH"; status=1; continue; }; \
		for got in $$found; do \
			if [ "$$(shasum -a 256 $$got | cut -d' ' -f1)" = "$$want" ]; then \
				echo "$$b: $$got"; \
			else \
				echo "$$b: $$got is stale (differs from $(HOME)/go/bin/$$b)"; status=1; \
			fi; \
		done; \
	done; \
	exit $$status

nice: vuln lint vet fmt fix test mod-verify tidy

vuln:
	@command -v govulncheck >/dev/null 2>&1 || GOTOOLCHAIN=auto go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

lint:
	golangci-lint run

vet:
	go vet ./...

fmt: tidy
	@echo "gofmt -s -w <tracked .go files>"
	@gofmt -s -w $(GOFILES)
	@gofmt -w -r 'interface{} -> any' $(GOFILES)
	@command -v goimports >/dev/null 2>&1 && goimports -w -local github.com/hallow-inc/claude-plugins/plugins/hallow-assurance $(GOFILES) || true

fix:
	go fix ./...

test:
	go test -race -shuffle=on ./...

mod-verify:
	go mod download
	go mod verify

tidy:
	go mod tidy
