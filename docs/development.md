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
  handling, device classification and the change log.
- `internal/classify`: the rules that guess a device's type from its vendor,
  name and open ports.
- `internal/notify`: sends each finished scan's changes to ntfy or a webhook.
- `internal/web`, `internal/api`: the HTML UI and the JSON API.
- `internal/db`: connection, migrations, generated queries.

Queries are SQLC-generated from `internal/db/queries/`. The schema is in
`internal/db/migrations/`.

## Add a notification service

Each service is a provider in `internal/notify`, in a file of its own, as
`ntfy.go` and `webhook.go` are:

1. Add a struct with `koanf` tags for its settings, and give it `Validate`,
   `Host` and `Send`. `Send` must not put a credential in its error. `post`
   covers a service that takes a JSON request.
2. Add a field for it to `notify.Config`, and a line to `Config.provider`.
3. Add an example to `jocasta.example.yaml`.

The notifier, the settings page, the store and `cmd` need no change.

## Screenshots

The images in [`docs/ui.md`](ui.md) are taken against a database of fabricated
devices, never real network data: addresses from RFC 5737, hardware addresses
from RFC 7042, invented names. Internet peers are well-known public services,
so their organisations and countries resolve.
