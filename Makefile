BINARY_NAME=hsync
BUILD_DIR=bin

.PHONY: all build clean test

all: build

build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/hsync

clean:
	rm -rf $(BUILD_DIR)

test: build
	./scripts/test.sh
