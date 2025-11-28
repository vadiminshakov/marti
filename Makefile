.PHONY: install-tools test lint lint-fix run

install-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

test:
	go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...

lint:
	@golangci-lint run --timeout=5m ./internal/...

lint-fix:
	@golangci-lint run --fix --timeout=5m ./...

run:
	go run cmd/marti/main.go
