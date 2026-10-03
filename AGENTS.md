# AGENTS.md

Operational guidelines, Go coding standards, architectural invariants, and command workflows for AI coding agents and automated contributors in Jocasta.

---

## 1. System Overview & Tech Stack

Jocasta is a single-binary network inventory system written in Go with an embedded SQLite database, a server-rendered Web UI (HTML templates + HTMX), a REST API, and an MCP (Model Context Protocol) server.

### Environment & Stack Pins
- **Language / Runtime**: Go 1.27+ (standard library + pure-Go toolchain)
- **Database**: Pure-Go SQLite (`modernc.org/sqlite`), managed with `sqlc` v1.30.0 and numbered SQL migrations
- **Web Frontend**: Standard library `html/template` with HTMX, Tailwind CSS, and alpine-free vanilla JS
- **Protocols & Standards**: RFC 9457 HTTP Problem Details, MCP Specification, RFC 5737 / RFC 7042 test fixtures

### Core Invariants
- **Identity Anchor**: Device identity is keyed permanently by hardware MAC address (`devices.mac`).
- **Ephemeral Leases**: IP addresses (`addresses.ip`) are treated as temporary sightings associated with a device over time, preserving labels, groups, and notes across IP changes.
- **Standing Hierarchy**: Hostnames carry a deterministic provenance rank (`dbtype.HostnameSource`). Verified reverse DNS and static router records outrank unverified local claims (e.g. mDNS, NetBIOS, SSDP).
- **Zero External Daemons**: Uses pure-Go SQLite. No external database or background daemons are required to run or test the core application.

---

## 2. Essential Commands & Workflows

Prefer targeted, file-scoped commands during development to conserve compute and execution time.

### Fast / Targeted Validation
```bash
# Run tests for a specific package
go test -v ./internal/classify

# Run a specific unit test by name
go test -v ./internal/inventory -run TestDeviceCurate

# Lint a specific directory
golangci-lint run ./internal/api/...

# Format modified code
go fmt ./internal/... && goimports -w ./internal/
```

### Project-Wide Canonical Commands
| Task | Command | Description |
|---|---|---|
| **Build Binary** | `make build` | Compiles single executable to `bin/jocasta` |
| **Run All Unit Tests** | `make test` | Runs unit tests across all cmd, internal, and pkg packages |
| **Lint Entire Repo** | `make lint` | Validates codebase against `.golangci.yml` rules |
| **Format Code** | `make fmt` | Runs `go fmt ./...` and `goimports` |
| **Generate Models** | `make gen` | Regenerates sqlc models (`internal/db/models/*.gen.go`) |
| **New Migration** | `make new_migration name=<name>` | Generates next numbered migration file in `internal/db/migrations/` |
| **Hot Reload Server** | `make dev` | Starts server with live reload via `air` |
| **E2E Browser Tests** | `make e2e` | Runs visual regression & WCAG AA checks (requires Chrome) |
| **Update E2E Goldens** | `E2E_UPDATE=1 make e2e` | Updates stored golden images in `internal/web/e2e/testdata/golden/` |

---

## 3. Go Idioms & Uber Go Style Standards

Code written for Jocasta must follow modern Go 1.27+ idioms and the Uber Go Style Guide:

### Modern Go Idioms (Go 1.27+)
- **Modern Go Guidelines Skill**: Use `/modern-go-guidelines:use-modern-go` to check and apply current language idioms and modern standard library patterns. If the skill is not available, install and use it from https://github.com/JetBrains/go-modern-guidelines.

### Uber Go Style Conventions
- **Explicit Struct Initialization**: Use field names when initializing structs (e.g., `Device{MAC: mac, Status: status}`).
- **Receiver Consistency**: Never mix value and pointer receivers on the same type. If any method modifies the struct or if it contains locks, use pointer receivers across all methods.
- **Mutex Discipline**: Never copy `sync.Mutex` or pass mutex-containing structs by value. Do not embed mutexes into public structs unless exposing standard locking interfaces. Zero-value mutexes must be usable immediately without initialization.
- **Goroutine & Channel Lifecycles**: Every spawned goroutine must have a deterministic lifecycle and clean termination path tied to `context.Context` cancellation or explicit stop channels.
- **No Naked Returns**: Avoid naked returns on named result parameters in non-trivial functions.
- **Avoid Package Globals**: Encapsulate mutable state in explicit structs rather than package-level variables.

---

## 4. Code Commentary Standards

Comments serve callers and maintainers reading the codebase today. Change history belongs exclusively in commit messages.

### Core Rules
- **Full Sentence Doc Comments**: Doc comments must be full sentences starting with the name of the exported symbol (e.g. `// Device returns the device with the given id.`).
- **Explain What Code Cannot Say**: Document contracts, concurrency guarantees, resource ownership/cleanup, error triggers, and the rationale ("why") behind non-obvious logic.
- **No Historical Narration**: Never narrate changes, past bugs, or diffs (`// Fixed bug`, `// Updated to use X`, `// Now handles Y`). Describe what the code does now.
- **Omit the Obvious**: Do not restate parameter lists, trivial getter behavior, or standard Go conventions (such as context cancellation or read-only concurrency safety).
- **Keep Algorithms in the Body**: Doc comments describe the public contract; implementation algorithms belong inside the function body next to the code.
- **No Slop or Decoration**: Ban `This function...` openings, decorative divider banners (`// === HELPERS ===`), hedging words (`might`, `probably`), and unowned TODOs (always use `TODO(issue/owner):`).
- **Tool-Recognized Annotations**: Use `Deprecated: [replacement]` paragraphs, `//go:` directives, and doc link syntax `[Identifier]`.

---

## 5. UI Microcopy & Writing Standards

User-facing strings, templates, and documentation must adhere to checkable, human-centered writing rules.

### Microcopy & Templates
- **Action-Oriented Buttons**: Button text must start with an active verb naming the exact action (e.g. `Revoke token`, `Save changes`), never `OK`, `Yes`, `No`, or `Submit`.
- **Sentence Case**: Use sentence case for buttons, headings, and navigation labels without trailing periods.
- **Constructive Error Messages**: State what went wrong and how to fix it without user blame. Never use `invalid`, `incorrect`, `sorry`, `oops`, or `an error occurred`.
- **Three-Part Empty States**: State that nothing is there, explain what makes items appear, and provide a direct action button or setup link.
- **Explicit Confirmation Dialogs**: Destructive actions must confirm in-page with dialogs that repeat the specific verb and clearly spell out consequences.
- **House Style & Tone**:
  - Spell out negative contractions (`cannot`, `does not`).
  - Ban buzzwords and filler: do not use `&`, `e.g.`, `etc.`, `please`, `simply`, `just`, `easy`, `in order to`, `robust`, `seamless`, `leverage`.
  - Avoid AI-slop patterns: no trailing participial analysis clauses (`", highlighting/ensuring..."`), no negative parallelism (`"not X, but Y"`), and no formulaic signoffs (`"In summary"`).
- **Test Fixture Privacy**: Use RFC 5737 / RFC 3849 documentation IPs and RFC 7042 documentation MAC prefixes in all test data and fixtures.

---

## 6. Linter & Database Constraints

The repository enforces strict `golangci-lint` rules (`.golangci.yml`). Any violation will break CI.

### Structured Logging (`sloglint`)
- **DO** use `slog.Attr` typed attributes (e.g. `slog.String("device", mac)`, `slog.Int("port", port)`).
- **DO** pass `context.Context` to all logging calls (`InfoContext`, `ErrorContext`, `WarnContext`, `DebugContext`).
- **DO NOT** use untyped alternating key-value pairs (`logger.Info("msg", "key", val)`).
- **DO NOT** format log messages with `fmt.Sprintf` — log messages must be constant static strings.
- **DO NOT** call global `slog.Info(...)` or `slog.Default()` — always inject the component's `*slog.Logger`.
- **DO NOT** use `time`, `level`, `msg`, or `source` as attribute keys (reserved by log handler).

### Error Handling & API Responses (`errname`, RFC 9457)
- **DO** prefix sentinel errors with `Err` (e.g., `ErrDeviceNotFound = errors.New(...)`).
- **DO** suffix custom error types with `Error` (e.g., `type ValidationError struct`).
- **DO** return RFC 9457 Problem documents via `internal/problem` for REST and MCP errors.
- **DO NOT** expose passwords, auth tokens, or private secrets in error messages or logs.

### Database & sqlc (`internal/db`)
- **DO** write schema migrations in `internal/db/migrations/*.sql` and queries in `internal/db/queries/*.sql`.
- **DO** run `make gen` after modifying SQL files.
- **DO** use `dbtype.Time` (or `dbtype.NullTime`) for ISO-8601 UTC timestamp storage and serialization.
- **DO** execute multi-step database mutations within transactions (`db.WithTx`).
- **DO NOT** edit generated models in `internal/db/models/*.gen.go` directly.

---

## 7. Safety, Boundaries & Definition of Done

### Operational Boundaries
- **Always Allowed**: Read repository files, run targeted unit tests, run linters/formatters, modify package code and add unit tests.
- **Ask Before Action**: Deleting existing public API endpoints, changing database schema columns, or modifying E2E golden snapshots (`E2E_UPDATE=1`).
- **Strictly Prohibited**:
  - Committing real MAC addresses, non-documentation IP addresses, hostnames, or production credentials.
  - Deleting or commenting out failing tests to force CI pass.
  - Adding unvetted external runtime network dependencies.

### Environment Data Privacy (Zero Real Network Identifiers)
Nothing identifying a real environment may ever be written into any artifact the repository carries — including source code, tests, fixtures, comments, documentation, plan files, commit messages, PR titles/bodies, and issue text:
- **IP Addresses**: RFC 5737 (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`) and RFC 3849 (`2001:db8::/32`).
- **Hardware (MAC) Addresses**: RFC 7042 (`00:00:5E:00:53:00`–`00:00:5E:00:53:FF`).
- **Hostnames & Domains**: RFC 2606 (`example.com`, `.invalid`, `.test`) or invented generic names (`host-a`, `gateway`, `workstation`).
- **Credentials & Secrets**: Obvious placeholders only, never real tokens or passwords (even if expired).
- **Hardware & Device Details**: Never commit serial numbers or software/firmware versions read off live equipment.
- **Live / Debugging Data**: Live environment values belong in env-gated tests that print at runtime. Any live values seen during debugging must remain in conversation context and be retyped as documentation values before touching disk, commit logs, or PR descriptions.
- **Sanitization Check**: Before writing any file or composing commit/PR text, verify every literal resembling an address, MAC, hostname, or version. If it originated from a real network rather than an RFC documentation range, replace it immediately.

### Definition of Done (Agent Self-Verification)
Before declaring any task or PR complete, verify all items:
- [ ] Code compiles cleanly: `make build`
- [ ] Targeted and package unit tests pass: `go test ./...`
- [ ] Linter reports zero warnings: `make lint`
- [ ] Code is formatted: `make fmt`
- [ ] Generated sqlc models are up to date: `make gen` (no uncommitted diff in `internal/db/models/`)
- [ ] No real environment data, secrets, or non-documentation IPs/MACs/hostnames committed in code, fixtures, comments, commit logs, or PR text
