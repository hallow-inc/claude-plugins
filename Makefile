SHELL := /usr/bin/env bash

REPO_DIR   := $(CURDIR)
MIRROR_DIR := $(HOME)/dev/claude-plugins-mirror
BRANCH     := $(shell git -C $(REPO_DIR) rev-parse --abbrev-ref HEAD)
MSG        ?= mirror $(shell date '+%Y-%m-%d %H:%M:%S')

.PHONY: help push sync mirror-push all check-mirror

help:
	@echo "Targets:"
	@echo "  push         git push current branch to origin"
	@echo "  sync         rsync repo -> $(MIRROR_DIR) (excludes .git, respects .gitignore)"
	@echo "  mirror-push  add+commit+push in mirror (MSG=... to override message)"
	@echo "  all          push && sync && mirror-push"

check-mirror:
	@test -d $(MIRROR_DIR)/.git || { echo "ERR: $(MIRROR_DIR) not a git repo"; exit 1; }
	@git -C $(MIRROR_DIR) remote get-url origin >/dev/null 2>&1 || { echo "ERR: mirror has no 'origin' remote"; exit 1; }

push:
	git -C $(REPO_DIR) push origin $(BRANCH)

sync: check-mirror
	rsync -a --delete \
		--exclude='.git/' \
		--filter=':- .gitignore' \
		$(REPO_DIR)/ $(MIRROR_DIR)/

mirror-push: check-mirror
	git -C $(MIRROR_DIR) add -A
	@if git -C $(MIRROR_DIR) diff --cached --quiet; then \
		echo "mirror: no changes"; \
	else \
		git -C $(MIRROR_DIR) commit -m "$(MSG)"; \
	fi
	git -C $(MIRROR_DIR) push origin $(shell git -C $(MIRROR_DIR) rev-parse --abbrev-ref HEAD)

all: push sync mirror-push
