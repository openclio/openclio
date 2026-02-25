APP_NAME  := openclio
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS   := -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -s -w"
INSTALL_DIR := $(HOME)/.local/bin

.PHONY: build run serve setup test test-integration lint vet proto clean build-all install checksums open-core-check release-check help

## build: Build for current platform
build:
	go build $(LDFLAGS) -o bin/$(APP_NAME) ./cmd/openclio

## run: Build and run in chat mode
run: build
	./bin/$(APP_NAME)

## serve: Build and run in serve mode
serve: build
	./bin/$(APP_NAME) serve

## setup: Build and run interactive setup wizard (for first-time users)
setup: build
	./bin/$(APP_NAME) init

## test: Run all unit tests
test:
	go test -race -timeout 120s ./...

## test-integration: Run unit + integration tests
test-integration:
	go test -race -tags=integration -timeout 120s ./...

## test-v: Run all tests (verbose)
test-v:
	go test -v -race -timeout 120s ./...

## vet: Run go vet
vet:
	go vet ./...

## lint: Run go vet + golangci-lint
lint: vet
	@which golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, ran go vet only"

## proto: Generate Go gRPC stubs from proto/agent.proto
## Requires: protoc, protoc-gen-go, protoc-gen-go-grpc
## Install: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
##          go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
proto:
	@which protoc >/dev/null 2>&1 || (echo "protoc not found — install from https://grpc.io/docs/protoc-installation/"; exit 1)
	mkdir -p internal/rpc/agentpb
	protoc --go_out=internal/rpc/agentpb --go_opt=module=github.com/openclio/openclio/internal/rpc/agentpb \
	       --go-grpc_out=internal/rpc/agentpb --go-grpc_opt=module=github.com/openclio/openclio/internal/rpc/agentpb \
	       proto/agent.proto
	@echo "Generated → internal/rpc/agentpb/"

## clean: Remove build artifacts
clean:
	rm -rf bin/ dist/

## build-all: Cross-compile for all supported platforms
build-all:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64  go build $(LDFLAGS) -o bin/$(APP_NAME)-linux-amd64    ./cmd/openclio
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64  go build $(LDFLAGS) -o bin/$(APP_NAME)-linux-arm64    ./cmd/openclio
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64  go build $(LDFLAGS) -o bin/$(APP_NAME)-darwin-amd64   ./cmd/openclio
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64  go build $(LDFLAGS) -o bin/$(APP_NAME)-darwin-arm64   ./cmd/openclio
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64  go build $(LDFLAGS) -o bin/$(APP_NAME)-windows-amd64.exe ./cmd/openclio
	@echo "Built all platforms → bin/"

## checksums: Generate SHA256 checksums for release binaries
checksums:
	@cd bin && sha256sum $(APP_NAME)-* > checksums.txt && cat checksums.txt

## open-core-check: Ensure public repo has no private imports
open-core-check:
	./scripts/check-open-core-boundaries.sh

## install: Install binary to $(INSTALL_DIR)
install: build
	mkdir -p $(INSTALL_DIR)
	cp bin/$(APP_NAME) $(INSTALL_DIR)/$(APP_NAME)
	@echo "Installed to $(INSTALL_DIR)/$(APP_NAME)"

## release-check: Run all pre-release checks
release-check: open-core-check vet test test-integration build-all checksums
	@echo "✓ Pre-release checks passed"

## help: Show available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'

