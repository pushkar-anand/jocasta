# Development

## Make targets

```bash
make build          # build to bin/
make test           # go test ./...
make lint           # golangci-lint (installs it to bin/ on first run)
make dev            # hot-reload server via air
make gen            # regenerate sqlc models
make new_migration name=<name>
make oui            # rebuild the embedded MAC-vendor table
make htmx           # refresh the vendored htmx
```

## Layout

- `cmd/jocasta`: CLI entry point.
- `internal/scanner`: the ICMP sweep.
- `internal/plugin`: sources beyond the sweep (RouterOS).
- `internal/inventory`: the store, covering identity resolution, address
  handling and the change log.
- `internal/web`, `internal/api`: the HTML UI and the JSON API.
- `internal/db`: connection, migrations, generated queries.

Queries are SQLC-generated from `internal/db/queries/`. The schema is in
`internal/db/migrations/`.

## Screenshots

The images in [`docs/ui.md`](ui.md) are taken against a database of fabricated
devices, never real network data: addresses from RFC 5737, hardware addresses
from RFC 7042, invented names.
