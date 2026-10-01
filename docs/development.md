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

## Release

After CI passes on `main`, run:

```bash
./scripts/release.sh v0.10.0
```

The script requires a clean working tree and a local `main` that matches
`origin/main`. It checks that the tag is unused, creates an annotated tag,
and pushes it to trigger the release workflow. It does not wait for the build.

## Layout

- `cmd/jocasta`: CLI entry point.
- `internal/scanner`: the ICMP sweep, and the reverse DNS, mDNS, NetBIOS,
  DNS-SD and SSDP lookups that name what answers.
- `internal/plugin`: sources beyond the sweep (RouterOS, OpenWrt, NetFlow).
- `internal/inventory`: the store, covering identity resolution, address
  handling, device classification and the change log.
- `internal/classify`: the rules that guess a device's type from its vendor,
  name and open ports.
- `internal/notify`: sends each finished scan's changes to ntfy, a webhook
  or a templated HTTP request.
- `internal/web`, `internal/api`: the HTML UI and the JSON API.
- `internal/db`: connection, migrations, generated queries.

Queries are SQLC-generated from `internal/db/queries/`. The schema is in
`internal/db/migrations/`.

## Add a notification service

Most services take an HTTP request, and the `http` provider reaches them
with a body template and no new code. For one of those, add a recipe to
[Send to another service](setup.md#send-to-another-service).
`TestRecipesInTheDocsRender` renders every recipe there, so a broken one
fails the tests.

A service gets a provider of its own only when the `http` provider cannot
reach it: it needs more than one request, a protocol other than HTTP, or
signing a template cannot do. Each provider is in `internal/notify`, in a
file of its own, as `ntfy.go` and `webhook.go` are:

1. Add a struct with `koanf` tags for its settings, and give it `Validate`,
   `Host` and `Send`. `Send` must not put a credential in its error. `post`
   covers a service that takes an HTTP request.
2. Add a field for it to `notify.Config`, and a line to `Config.provider`.
3. Add an example to `jocasta.example.yaml`.

The notifier, the settings page, the store and `cmd` need no change.

## Add a name source

Each name carries a standing, `dbtype.HostnameSource`, which says how it was
learned. When sources disagree, the name with the higher standing is shown.

1. Add a constant for it in `internal/db/dbtype/enum.go` and add it to
   `hostnameSources`.
2. Give it a place in `HostnameSource.Rank`, and say why in the comment
   there. A name that resolves ranks above one a device only claims.
3. Word it for the device page in `standing`, in `internal/web/funcs.go`.
4. List it in [Name devices](setup.md#name-devices).

A sweep that asks devices for their names takes two more steps:

1. Add its lookup in `internal/scanner`, in a file of its own, and ask it in
   `enrich` in rank order. A query sent to each host is a `nameProtocol`, as
   in `mdns.go` and `netbios.go`. Any other lookup is a `nameLookup`, as
   `askSSDP` in `ssdp.go` is, or names hosts itself, as `browseServices`
   does with `dnssd.go`.
2. Add it to `HostnameSource.Swept`, so a lower-ranked lookup never replaces
   its name on the sweep's claim.

The column has no CHECK, so a new standing needs no migration.

## Screenshots

The images in [`docs/ui.md`](ui.md) are taken against a database of fabricated
devices, never real network data: addresses from RFC 5737, hardware addresses
from RFC 7042, invented names. Internet peers are well-known public services,
so their organisations and countries resolve.
