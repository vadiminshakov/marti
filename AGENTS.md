# AGENTS.md - Marti Cryptocurrency Trading Bot

## Commands
- **Run**: `make run` or `go run cmd/marti/main.go`
- **Test all**: `make test` or `go test -v -race ./...`
- **Test single**: `go test -v -race ./path/to/package -run TestName`
- **Lint**: `make lint` (uses golangci-lint with strict config)
- **Lint fix**: `make lint-fix`

## Architecture
- `cmd/marti/` - Main entry point
- `internal/` - Core logic: `clients/` (exchange APIs), `domain/` (models), `services/` (strategies), `storage/`, `wal/`
- `pkg/` - Reusable packages: `indicators/`, `retrier/`
- `dashboard/` - Web UI (static HTML/JS)
- `config/` - Configuration parsing; uses YAML config files

## Code Style (enforced by .golangci.yml)
- Imports order: stdlib → external → `github.com/vadiminshakov/marti` (use goimports/gci)
- Use `github.com/pkg/errors` for error wrapping; always wrap errors with context
- Naming: camelCase for vars, PascalCase for exports; no snake_case
- Tests use `github.com/stretchr/testify`; mocks in `mocks/` dir
- Logging via `go.uber.org/zap`
- Comments must end with period (godot linter)
