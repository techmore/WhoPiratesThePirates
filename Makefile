BINARY ?= whop2p
FULL_BINARY ?= who-pirates-the-pirates
BUILD_DIR ?= bin
GO ?= go
GOFLAGS ?=
MACHINE ?= whop2p
IMAGE ?= whop2p:local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -s -w
VERSION_LDFLAGS ?= -X main.version=$(VERSION)

.PHONY: all build test vet race clean linux linux-arm64 darwin orchard-build orchard-create orchard-shell orchard-stop

all: test build

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags="$(LDFLAGS) $(VERSION_LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/server
	ln -sf $(BINARY) $(BUILD_DIR)/$(FULL_BINARY)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

race:
	$(GO) test -race ./...

linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags="$(LDFLAGS) $(VERSION_LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/server
	ln -sf $(BINARY)-linux-amd64 $(BUILD_DIR)/$(FULL_BINARY)-linux-amd64

linux-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags="$(LDFLAGS) $(VERSION_LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/server

darwin:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags="$(LDFLAGS) $(VERSION_LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 ./cmd/server
	ln -sf $(BINARY)-darwin-arm64 $(BUILD_DIR)/$(FULL_BINARY)-darwin-arm64

# Default macOS deployment: Apple container machine managed by Orchard.
orchard-build: linux-arm64
	container build -f deploy/orchard/Containerfile -t $(IMAGE) .

orchard-create: orchard-build
	container machine create $(IMAGE) --name $(MACHINE)

orchard-shell:
	container machine run -n $(MACHINE)

orchard-stop:
	container machine stop $(MACHINE)

clean:
	rm -rf $(BUILD_DIR)
