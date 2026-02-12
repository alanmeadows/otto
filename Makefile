VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

.PHONY: all build test lint vet install clean

all: lint vet test build

build:
	go build -ldflags "-X github.com/alanmeadows/otto/internal/cli.Version=$(VERSION)" -o bin/otto ./cmd/otto

test:
	go test ./... -timeout 120s

lint:
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run || echo "golangci-lint not found, skipping"

vet:
	go vet ./...

install: build
	@mkdir -p ~/.local/bin
	cp bin/otto ~/.local/bin/otto

clean:
	rm -rf ./bin/
	go clean -cache
