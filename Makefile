BINARY_NAME=hsync
BUILD_DIR=bin

.PHONY: all build clean test install-service

all: build

build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/hsync

clean:
	rm -rf $(BUILD_DIR)

test: build
	./scripts/test.sh

install-service:
	./scripts/install_service.sh
