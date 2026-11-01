# ccctl — Makefile dispatcher.
# Thin targets; real work in Go (and scripts/ when a recipe outgrows one line).

.DEFAULT_GOAL := help

BINARY := ccctl
CMD    := ./cmd/$(BINARY)
COMPLETION_DIR ?= $(HOME)/.oh-my-zsh/completions

.PHONY: help build install completions test test-real fmt vet tidy clean

help: ## list targets
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z_-]+:.*##/{printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## compile to ./bin/ccctl
	@mkdir -p bin
	go build -o bin/$(BINARY) $(CMD)

install: ## install via `go install` (into GOBIN or GOPATH/bin)
	go install $(CMD)
	@dir="$$(go env GOBIN)"; [ -z "$$dir" ] && dir="$$(go env GOPATH)/bin"; \
	 case ":$$PATH:" in \
	   *":$$dir:"*) : ;; \
	   *) echo ""; echo "note: $$dir is not on your PATH — add it to run \`$(BINARY)\` directly." ;; \
	 esac

completions: ## write zsh completion to $(COMPLETION_DIR)/_$(BINARY)
	@mkdir -p $(COMPLETION_DIR)
	@$(BINARY) completion zsh > $(COMPLETION_DIR)/_$(BINARY)
	@echo "zsh completion → $(COMPLETION_DIR)/_$(BINARY) (run 'zz' to activate)"

test: ## run go test
	go test ./...

test-real: ## read-only: check that edits round-trip the real ~/.claude.json and history.jsonl
	CCCTL_REAL_FILES=1 go test -count=1 -run TestRealFilesRoundTrip -v ./internal/claudestate/

fmt: ## gofmt the tree
	gofmt -w .

vet: ## go vet
	go vet ./...

tidy: ## tidy go.mod / go.sum
	go mod tidy

clean: ## remove build output
	rm -rf bin/
