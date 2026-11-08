# Repository Guidelines

## Project Structure & Module Organization

- The root `wireless` package owns evidence values, JSON, and fingerprints.
- `parser/` handles parsing contracts; `vendors/h3c/` and `vendors/huawei/` implement device syntax.
- `collect/`, `review/`, `change/`, and `report/` own business capabilities; `adapter/netconnector/` integrates transport.
- `cmd/wireless/` is the CLI entrypoint; `internal/` contains CLI, bootstrap, and workflows.
- Tests use `*_test.go`; fixtures live in `testdata/`, examples in `examples/`, and documentation in `docs/`.

Before changing package dependencies, read `docs/architecture.md` and preserve its import boundaries.

## Build, Test, and Development Commands

Use Go 1.26+ and Python 3. From the repository root, set `export GOWORK=off` to use published dependencies.

- `go build -o bin/wireless ./cmd/wireless`: build the CLI.
- `go run ./cmd/wireless parse --file examples/controller.cfg --complete --json`: exercise offline parsing.
- `go test ./...`: run Go tests.
- `bash scripts/ci`: check formatting, architecture, modules, race/shuffle tests, vet, Python checks, and independent consumer builds.
- `golangci-lint run`: run the linters configured in `.golangci.yml`.

## Coding Style & Naming Conventions

Format changed Go files with `goimports` using the version pinned in `scripts/ci`; it applies standard Go formatting and tabs. Use lowercase package names, `MixedCaps` exports, and `mixedCaps` private identifiers. Preserve initialisms such as `AP`, `JSON`, and `CLI`.

## Testing Guidelines

Use Go's standard `testing` package and native fuzzing. Name tests `TestXxx` and fuzz targets `FuzzXxx` in `*_test.go`. Reuse synthetic/frozen fixtures and injected sessions; ordinary tests need neither Ruby nor live devices. Preserve fixture provenance and checksums. No numeric coverage threshold is configured; cover changed behavior and failure paths. See `docs/verification.md` for validation boundaries.

## Commit & Pull Request Guidelines

History mixes imperative subjects with `fix:`, `refactor:`, and `test:` prefixes. Use concise subjects describing the change. PRs should explain the problem and resulting behavior, link relevant issues, list validation commands/results, and update affected API or migration documentation. Distinguish local checks from real-controller validation.

## Security & Configuration

Keep credentials in private mode-0600 `.env` files or process environment. Use synthetic controller examples, preserve strict SSH host checks, and run device-writing `apply` operations only with explicit authorization.
