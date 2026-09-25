BINARY ?= whop2p
FULL_BINARY ?= who-pirates-the-pirates
BUILD_DIR ?= bin
GO ?= go
GOFLAGS ?=

.PHONY: all build test vet race clean linux darwin

all: test build

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY) ./cmd/server
	ln -sf $(BINARY) $(BUILD_DIR)/$(FULL_BINARY)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

race:
	$(GO) test -race ./...

linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/server
	ln -sf $(BINARY)-linux-amd64 $(BUILD_DIR)/$(FULL_BINARY)-linux-amd64

darwin:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 ./cmd/server
	ln -sf $(BINARY)-darwin-arm64 $(BUILD_DIR)/$(FULL_BINARY)-darwin-arm64

clean:
	rm -rf $(BUILD_DIR)
