BINARY := agent-slack
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOCACHE ?= $(CURDIR)/.cache/go-build

build:
	GOCACHE=$(GOCACHE) go build -buildvcs=false -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/agent-slack

test:
	GOCACHE=$(GOCACHE) go test ./... -count=1

# The event socket is the repo's only real concurrency — a reader and a
# keepalive per connection, swapped under the consumer on reconnect — so the
# race detector earns its runtime here.
test-race:
	GOCACHE=$(GOCACHE) go test ./... -count=1 -race

test-short:
	GOCACHE=$(GOCACHE) go test ./... -count=1 -short

lint:
	golangci-lint run ./...

# Scoped to tracked files: this repo keeps a module cache under .cache/, which
# the go tool skips (dot-directory) but gofmt and goimports walk into, so a bare
# `-w .` rewrites vendored dependencies and makes `gofmt -l .` report noise.
fmt:
	gofmt -w $$(git ls-files '*.go')
	@command -v goimports >/dev/null && goimports -w $$(git ls-files '*.go') || echo "goimports not installed (optional; install: go install golang.org/x/tools/cmd/goimports@latest)"

vet:
	GOCACHE=$(GOCACHE) go vet ./...

dev:
	GOCACHE=$(GOCACHE) go run ./cmd/agent-slack $(ARGS)

clean:
	rm -f $(BINARY)
	rm -rf dist/

.PHONY: build test test-short lint fmt vet dev clean
