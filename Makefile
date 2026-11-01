GO_CMD ?= go
BUILD_DIR ?= bin

.PHONY: build run test race vet fmt clean

build:
	mkdir -p $(BUILD_DIR)
	$(GO_CMD) build -o $(BUILD_DIR)/goterminalfa ./cmd/goterminalfa

run:
	$(GO_CMD) run ./cmd/goterminalfa -config ./config.json

test:
	$(GO_CMD) test ./...

race:
	$(GO_CMD) test -race ./...

vet:
	$(GO_CMD) vet ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BUILD_DIR)/goterminalfa
