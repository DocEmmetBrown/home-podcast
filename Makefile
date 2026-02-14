BINARY ?= home-podcast
BUILD_DIR ?= bin

.PHONY: build build-local test coverage clean

build:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY) ./cmd/home-podcast

build-local:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/home-podcast

test:
	go test ./... -coverprofile=coverage.out

coverage: test
	go tool cover -func=coverage.out

clean:
	rm -rf $(BUILD_DIR) coverage.out
