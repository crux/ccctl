# ccctl — Makefile dispatcher.
# Thin targets; the work happens in scripts/. Names and pins live in
# release.env, which the scripts read too. Release flow: see README
# "Releasing" (templates from the release-core, release-go and
# release-homebrew skills).

include release.env

VERSION    ?=
GO_LDFLAGS := -X main.version=$(VERSION)

.DEFAULT_GOAL := help

help: ## list the targets
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z0-9_-]+:.*##/{printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# --- develop -----------------------------------------------------------

build: ## build bin/ccctl for this machine (no VERSION needed)
	go build -ldflags "$(GO_LDFLAGS)" -o bin/$(BINARY) $(MAIN)

install: ## go install ccctl into GOBIN
	go install -ldflags "$(GO_LDFLAGS)" $(MAIN)

check: vet test fmt-check ## everything CI checks

vet: ## go vet
	go vet ./...

test: ## go test
	go test ./...

fmt-check: ## fail when gofmt would change a file
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }

fmt: ## gofmt the tree
	gofmt -w .

tidy: ## tidy go.mod / go.sum
	go mod tidy

test-real: ## read-only: check that edits round-trip the real ~/.claude.json and history.jsonl
	CCCTL_REAL_FILES=1 go test -count=1 -run TestRealFilesRoundTrip -v ./internal/claudestate/

clean: ## remove build output
	rm -rf bin dist

# --- release -----------------------------------------------------------

require-version:
	@test -n "$(VERSION)" || { echo "VERSION is required: make $(MAKECMDGOALS) VERSION=x.y.z" >&2; exit 1; }

release: ## tag vVERSION on main and push it; CI builds the release
	@bash scripts/release.sh "$(VERSION)"

dist: require-version ## build the release archives and .sha256 files into dist/
	bash scripts/build-dist.sh "$(VERSION)"

publish: require-version ## create the GitHub release from dist/ (CI)
	bash scripts/publish.sh "$(VERSION)"

tap: require-version ## write the Homebrew formula and push it to the tap (CI)
	bash scripts/tap.sh "$(VERSION)"

.PHONY: help build install check vet test fmt-check fmt tidy test-real clean require-version release dist publish tap
