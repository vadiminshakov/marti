MOCKERY_VERSION ?= v2.53.5

.PHONY: install-tools test lint lint-fix run gen-mocks

install-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/vektra/mockery/v2@$(MOCKERY_VERSION)

test:
	go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...

lint:
	@golangci-lint run --timeout=5m ./internal/...

lint-fix:
	@golangci-lint run --fix --timeout=5m ./...

gen-mocks:
	go run github.com/vektra/mockery/v2@$(MOCKERY_VERSION) --all --recursive --dir . --output ./mocks --keeptree --case underscore --exclude mocks --exclude vendor --exclude internal/services/bot
	go run github.com/vektra/mockery/v2@$(MOCKERY_VERSION) --name tradingStrategy --exported --dir ./internal/services/bot --output ./mocks/bot --outpkg botmock --filename trading_strategy.go --case underscore
	go run github.com/vektra/mockery/v2@$(MOCKERY_VERSION) --name dcaCostBasisProvider --exported --dir ./internal/services/bot --output ./mocks/bot --outpkg botmock --filename dca_cost_basis_provider.go --case underscore

run:
	go run cmd/marti/main.go
