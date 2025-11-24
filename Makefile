.PHONY: test lint lint-fix install-tools

# Install required tools
install-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Run tests
test:
	go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...

# Run linter
lint:
	@golangci-lint run --timeout=5m ./internal/...

# Run linter and fix issues
lint-fix:
	@golangci-lint run --fix --timeout=5m ./...

# Prepare development environment
setup: install-tools lint test

# Run the application
run:
	go run cmd/marti/main.go
