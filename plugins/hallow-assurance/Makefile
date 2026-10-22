PKGS := ./cmd/assure ./adapters/go/assure-adapter-go ./adapters/typescript/assure-adapter-typescript
BINS := assure assure-adapter-go assure-adapter-typescript
# Hooks run with the PATH Claude Code launched with, so both the mise toolchain's GOBIN and
# $HOME/go/bin must carry the same build; a stale copy in either one wins depending on PATH order.
GOBINS := $(sort $(HOME)/go/bin $(shell go env GOBIN))

.PHONY: install verify

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
