# Graph Review Development Makefile

.PHONY: build test clean install deps fmt vet lint

# Build variables
BINARY_NAME=graph-review
BUILD_DIR=./bin
MAIN_FILE=./cmd/graph-review/main.go

# Default target
all: deps fmt vet test build

# Install dependencies
deps:
	go mod tidy
	go mod download

# Format code
fmt:
	go fmt ./...

# Vet code
vet:
	go vet ./...

# Run tests
test:
	go test -v ./...

# Build binary
build: deps fmt vet
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) .

# Build for multiple platforms
build-all: deps fmt vet
	mkdir -p $(BUILD_DIR)
	# Linux
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 .
	# macOS
	GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 .
	# Windows
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe .

# Install binary globally
install: build
	go install .

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)
	go clean

# Run linting (requires golangci-lint)
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not found. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; exit 1; }
	golangci-lint run

# Development workflow
dev: fmt vet test build

# Release target (for GitHub Actions)
release: clean build-all
	mkdir -p $(BUILD_DIR)/release
	# Create archives
	cd $(BUILD_DIR) && tar -czf release/$(BINARY_NAME)-linux-amd64.tar.gz $(BINARY_NAME)-linux-amd64
	cd $(BUILD_DIR) && tar -czf release/$(BINARY_NAME)-darwin-amd64.tar.gz $(BINARY_NAME)-darwin-amd64
	cd $(BUILD_DIR) && tar -czf release/$(BINARY_NAME)-darwin-arm64.tar.gz $(BINARY_NAME)-darwin-arm64
	cd $(BUILD_DIR) && zip release/$(BINARY_NAME)-windows-amd64.zip $(BINARY_NAME)-windows-amd64.exe

# Help
help:
	@echo "Available targets:"
	@echo "  all            - Run full development workflow (deps, fmt, vet, test, build)"
	@echo "  deps           - Install/update Go dependencies"
	@echo "  fmt            - Format code"
	@echo "  vet            - Run go vet"
	@echo "  test           - Run tests"
	@echo "  build          - Build binary"
	@echo "  build-all      - Build for multiple platforms"
	@echo "  install        - Install binary globally"
	@echo "  clean          - Clean build artifacts"
	@echo "  lint           - Run golangci-lint (requires golangci-lint installation)"
	@echo "  dev            - Quick development workflow"
	@echo "  release        - Create release archives"
	@echo "  help           - Show this help message"
