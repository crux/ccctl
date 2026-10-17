# claudectl — Makefile dispatcher.
# Thin targets; real work in Go (and scripts/ when a recipe outgrows one line).

.DEFAULT_GOAL := help

BINARY := claudectl
CMD    := ./cmd/$(BINARY)

.PHONY: help build install test fmt vet tidy clean

help: ## list targets
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z_-]+:.*##/{printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## compile to ./bin/claudectl
	@mkdir -p bin
	go build -o bin/$(BINARY) $(CMD)

install: ## install via `go install` (into GOBIN or GOPATH/bin)
	go install $(CMD)
	@dir="$$(go env GOBIN)"; [ -z "$$dir" ] && dir="$$(go env GOPATH)/bin"; \
	 case ":$$PATH:" in \
	   *":$$dir:"*) : ;; \
	   *) echo ""; echo "note: $$dir is not on your PATH — add it to run \`$(BINARY)\` directly." ;; \
	 esac

test: ## run go test
	go test ./...

fmt: ## gofmt the tree
	gofmt -w .

vet: ## go vet
	go vet ./...

tidy: ## tidy go.mod / go.sum
	go mod tidy

clean: ## remove build output
	rm -rf bin/
