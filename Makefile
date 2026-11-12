ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
GO_CMD ?= go
APP_NAME := goterminalfa
CMD_PATH := ./cmd/$(APP_NAME)
BUILD_DIR ?= bin
BUILD_OUTPUT := $(ROOT_DIR)/$(APP_NAME)

.PHONY: build prod run test race vet fmt clean

build:
	mkdir -p "$(BUILD_DIR)"
	$(GO_CMD) build -o "$(BUILD_DIR)/$(APP_NAME)" "$(CMD_PATH)"

# Production build.
prod:
	@echo "Building production $(APP_NAME)..."
	CGO_ENABLED=0 $(GO_CMD) -C "$(ROOT_DIR)" build \
		-trimpath \
		-ldflags="-s -w" \
		-o "$(BUILD_OUTPUT)" \
		"$(CMD_PATH)"

run:
	$(GO_CMD) run "$(CMD_PATH)" -config ./config.json

test:
	$(GO_CMD) test ./...

race:
	$(GO_CMD) test -race ./...

vet:
	$(GO_CMD) vet ./...

fmt:
	gofmt -w .

clean:
	rm -f "$(BUILD_DIR)/$(APP_NAME)" "$(BUILD_OUTPUT)"
