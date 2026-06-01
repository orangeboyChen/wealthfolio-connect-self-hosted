# AGENTS.md — AI Agent Constraints

This document defines the conventions and constraints that any AI coding agent (or
human contributor) MUST follow when working on this repository. It is the source of
truth for project structure, tooling and code style. Keep it in sync with the actual
project as it evolves.

---

## Architecture

- The project follows a **Domain-Driven Design (DDD)** layered architecture.
  All non-`main` code lives under `internal/` so external modules cannot import
  it (Go's `internal` visibility rule):
  - `internal/domain/` — Entities, Value Objects, Domain Services and Repository
    **interfaces**. No external/framework dependencies; pure Go business logic only.
  - `internal/application/` — Application Services (use cases / orchestration),
    DTOs, commands and queries. Depends only on `internal/domain/` interfaces.
  - `internal/infrastructure/` — Repository **implementations** (PostgreSQL),
    external API clients (Futu, IBKR, exchanges, RPC), config, logging, JWT signing.
  - `internal/interfaces/` — HTTP handlers, middleware, request/response models
    and route registration. Delegates all business logic to `internal/application/`.
  - `cmd/server/` — Process entry point (`main`); only this package is allowed
    outside `internal/`.
- HTTP handlers MUST NOT contain business logic; they only translate
  request → application call → response.
- Repository interfaces live in `internal/domain/`. Implementations live in
  `internal/infrastructure/`.

## Dependency Injection

- All wiring goes through **uber-go/fx**. There is **no global state**, and **no
  `init()` functions** for DI.
- Every layer exposes a single `fx.Option` named `Module` (e.g. `domain.Module`,
  `application.Module`, `infrastructure.Module`, `interfaces.Module`).
- Lifecycle (start/stop of HTTP server, DB pool, sync scheduler) is managed via
  `fx.Lifecycle` hooks.

## Testing

- BDD style with **Ginkgo** + **Gomega**. Use `Describe` / `Context` / `It` blocks.
- Mocks are generated with **gomock** (`go.uber.org/mock/mockgen`).
- Test files are colocated with the source: `foo.go` ↔ `foo_test.go`.
- Each package that owns interfaces SHOULD provide a `mocks/` subpackage or a
  `mock_<name>.go` file in the same package.
- **Coverage threshold: ≥ 80%** overall (`go test ./... -race -coverprofile=coverage.out`).
- HTTP handlers are tested with `httptest` (no real listening server in unit tests).

## Code Style

- Format with `gofmt` / `goimports`. CI fails otherwise.
- Use **structured logging** (zerolog). **No `fmt.Println`** in production code.
- Wrap errors with context: `fmt.Errorf("doing X: %w", err)`.
- No naked returns in non-trivial functions.
- Exported identifiers MUST have doc comments.

## Configuration

- All configuration is read from **environment variables**. No config files.
- A single `Config` struct in `internal/infrastructure/config` is populated at
  startup and injected by fx.
- Required vars: `DATABASE_URL`, `JWT_SECRET`, `CONNECT_AUTH_PUBLISHABLE_KEY`.
- See [README.md](./README.md) for the complete env var table.

## Naming Conventions

- Interfaces: no `I` prefix (e.g. `Repository`, not `IRepository`).
- Mocks: filename `mock_<name>.go` in the same package, or under a `mocks/` subdir.
- DTOs: suffixed with `DTO` (e.g. `AccountDTO`).
- Packages: short, lowercase, no underscores.

## Git

- Commit messages follow **Conventional Commits**: `feat:`, `fix:`, `refactor:`,
  `test:`, `docs:`, `chore:`, `ci:`.
- PRs MUST pass CI (vet, lint, test ≥ 90% coverage) before merge.
- Branch names: `feat/<topic>`, `fix/<topic>`, `chore/<topic>`.

## Forbidden Patterns

- **No `panic()`** in production code (only `main` may panic on unrecoverable
  startup errors).
- **No `os.Exit()`** outside of `main`.
- **No raw SQL strings scattered across files** — keep SQL in repository
  implementations or query constants.
- **No hardcoded secrets** anywhere in the repo.
- **No business logic in HTTP handlers**.
- **No direct dependency from `internal/domain/` to `internal/infrastructure/`**.

## Workflow Reminders for Agents

1. Before adding a new dependency, verify it's not already provided.
2. Before adding code to a layer, confirm it belongs there (see Architecture).
3. After every behavioural change, add or update tests so the ≥90% coverage
   threshold is preserved.
4. After touching env vars, update `README.md` and
   `internal/infrastructure/config/config.go`.
