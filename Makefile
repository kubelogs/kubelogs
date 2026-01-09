.PHONY: dev test build docker-build clean help

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
BINARY_COLLECTOR=kubelogs-collector
BINARY_SERVER=kubelogs-server

# Docker parameters
REGISTRY?=ghcr.io
IMAGE_PREFIX?=$(REGISTRY)/$(shell basename $(CURDIR))
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
PLATFORMS?=linux/amd64,linux/arm64

# Build flags
LDFLAGS=-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

## dev: Run collector locally
dev:
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o bin/$(BINARY_COLLECTOR) ./cmd/collector
	./bin/$(BINARY_COLLECTOR)

## test: Run all tests
test:
	$(GOTEST) -v -race ./...

## build: Build both binaries
build:
	CGO_ENABLED=0 $(GOBUILD) -ldflags "$(LDFLAGS)" -o bin/$(BINARY_COLLECTOR) ./cmd/collector
	CGO_ENABLED=0 $(GOBUILD) -ldflags "$(LDFLAGS)" -o bin/$(BINARY_SERVER) ./cmd/server

## docker-build: Build Docker images locally (amd64 only for speed)
docker-build:
	docker buildx build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
		-f build/collector/Dockerfile \
		-t $(IMAGE_PREFIX)-collector:$(VERSION) \
		--load .
	docker buildx build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
		-f build/server/Dockerfile \
		-t $(IMAGE_PREFIX)-server:$(VERSION) \
		--load .

## docker-build-multi: Build multi-arch Docker images
docker-build-multi:
	docker buildx build --platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
		-f build/collector/Dockerfile \
		-t $(IMAGE_PREFIX)-collector:$(VERSION) .
	docker buildx build --platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
		-f build/server/Dockerfile \
		-t $(IMAGE_PREFIX)-server:$(VERSION) .

## clean: Remove build artifacts
clean:
	rm -rf bin/
	$(GOCMD) clean -cache

## help: Show this help
help:
	@echo "Available targets:"
	@grep -E '^##' Makefile | sed 's/## /  /'
