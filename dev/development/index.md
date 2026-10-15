# Development

## Make targets

```
make build          # build to bin/
make test           # go test ./...
make e2e            # browser tests a pull request must pass (needs Chrome)
make e2e-full       # browser tests on every screen and account
make smoke          # read the router, DNS and NetFlow sources in containers (needs Docker)
make lint           # golangci-lint (installs it to bin/ on first run)
make dev            # hot-reload server via air
make gen            # regenerate sqlc models
make new_migration name=<name>
make oui            # rebuild the embedded MAC-vendor table
make htmx           # refresh the vendored htmx
make docs           # build the docs site and check every link (needs uv)
make docs-serve     # preview the docs site at http://localhost:8000
```

## Browser tests

`internal/web/e2e` drives Chrome over the real server, built with the `e2e` tag so `go test ./...` leaves it out. It needs Google Chrome or Chromium on `PATH`, or `E2E_CHROME` set to one.

Each page is drawn over four fabricated inventories (empty, a home network, one with extreme data, and one with no accounts), and each dialog, menu and inline edit it opens is drawn too. Every drawing is checked for sideways scrolling, clipped or overlapping text, small tap targets, small text, WCAG 2.2 AA failures (axe-core), script errors and failed requests.

`make e2e` draws every page as admin on a phone, a tablet and a laptop, in both themes. It takes about two minutes, and CI does not run it: run it before opening a pull request that touches the UI. `make e2e-full` adds every other screen and account; `scripts/release.sh` runs it before tagging.

A finding fails the run unless `testdata/known.json` lists it under an open issue. A fix deletes its entries. `make e2e-full` also fails on an entry that no longer matches anything.

The run also compares the first screen of every page on the home network with a stored picture in `testdata/golden/`: on a phone, a tablet and a laptop in light, and the laptop in dark. A picture fails when more than 64 of its pixels changed, which a single word added or lost exceeds. When a change is meant, store the new pictures and commit them with it:

```
E2E_UPDATE=1 make e2e
```

To see what changed, set `E2E_ARTIFACTS` to a directory: each picture that differs is written there beside a map of the changed pixels.

To look at a fixture in a browser:

```
E2E_SERVE=weird go test -tags e2e -run TestServe -timeout 0 ./internal/web/e2e/
```

To capture every page and read the findings, with no pass or fail:

```
E2E_AUDIT=$PWD/tmp/audit TMPDIR=$PWD/tmp go test -tags e2e -run TestAudit -timeout 0 ./internal/web/e2e/
```

`E2E_FIXTURES`, `E2E_ROLES`, `E2E_VIEWPORTS`, `E2E_THEMES` and `E2E_PAGES` narrow either run to a comma-separated list.

## Smoke tests

`make smoke` reads real OpenWrt, RouterOS, Pi-hole, AdGuard Home and Technitium servers and a real NetFlow exporter, each in a container, and checks what jocasta says about them. The plugin tests answer from canned replies; this catches a server that has changed what it sends. It needs Docker and Go, and CI does not run it: run it before opening a pull request that changes one of these plugins or the client under `pkg/`.

`scripts/smoke/smoke.sh up` starts the servers on a network of their own, `198.51.100.0/24`:

- OpenWrt is set up as [the OpenWrt page](<https://pushkar-anand.github.io/jocasta/dev/plugins/openwrt/index.md>) describes, with the ACL in `docs/openwrt/jocasta.json`, and given leases and static hosts.
- RouterOS CHR runs in QEMU, booted by `scripts/smoke/routeros.sh`. Its `ether1` takes an address over DHCP, as a WAN does, and `ether2` sits on the smoke network. It gets a read-only `jocasta` user, a bridge with an address of its own, a DHCP server with a static lease, a static ARP entry, and Traffic Flow exporting IPFIX, as [the RouterOS page](<https://pushkar-anand.github.io/jocasta/dev/plugins/routeros/#export-traffic-flows>) sets it up.
- Pi-hole, AdGuard Home and Technitium each block a name, and AdGuard Home rewrites one. Technitium is set up as [the Technitium page](<https://pushkar-anand.github.io/jocasta/dev/plugins/technitium/index.md>) describes: a user that can view Logs, its API token, and the Query Logs (Sqlite) app. Pi-hole answers as the other two's upstream, so no lookup leaves the network.
- pmacct runs in the OpenWrt container's network namespace and exports NetFlow v9 to this host, as the router would.
- The OpenWrt container sends lookups to each resolver.

Each server's web interface is published on `127.0.0.1`, which jocasta reads.

`scripts/smoke/smoke.sh check` runs `jocasta plugin run` against each server and checks the rows it prints. It runs `TestNetFlowLive` in `internal/plugin` while the OpenWrt container makes a lookup, and again while this host makes a request to the router, and checks the flows each decodes. It also runs the live tests in `pkg/openwrt`, `pkg/routeros` and `internal/plugin`. It writes the config it used to `$TMPDIR/jocasta-smoke/jocasta.yaml`, so you can point `jocasta serve` at the same servers. `make smoke-down` removes them.

Some services need more than Docker. Leave any of them out with `SMOKE_SKIP`, such as `SMOKE_SKIP="technitium netflow" make smoke`:

- `routeros`: RouterOS runs under QEMU, which emulates the CPU where the host has no KVM. It boots in under a minute either way. Its container joins a second network, which needs Docker 28 or later.
- `technitium`: the server downloads the Query Logs app from Technitium's app store, so its container needs internet.
- `netflow`: the exporter sends to the network's gateway address, which only Docker on Linux puts on the host.

To change a setting, set the matching variable:

- `SMOKE_REGISTRY` pulls the images from a mirror, such as `mirror.gcr.io`, when Docker Hub limits your pulls.
- `SMOKE_PIHOLE_IMAGE`, `SMOKE_ADGUARD_IMAGE`, `SMOKE_OPENWRT_IMAGE`, `SMOKE_ROUTEROS_IMAGE`, `SMOKE_TECHNITIUM_IMAGE` and `SMOKE_PMACCT_IMAGE` test another release. MikroTik publishes no image; the RouterOS one is `evilfreelancer/docker-routeros`, which carries the CHR disk and QEMU.
- `SMOKE_PIHOLE_PORT`, `SMOKE_ADGUARD_PORT`, `SMOKE_ADGUARD_SETUP_PORT`, `SMOKE_OPENWRT_PORT`, `SMOKE_TECHNITIUM_PORT` and `SMOKE_ROUTEROS_PORT` move the published ports, 18081 to 18086 by default. `SMOKE_FLOW_PORT` and `SMOKE_ROUTEROS_FLOW_PORT` move the ports the NetFlow and IPFIX exports arrive on, 2055 and 2056 by default.

netifd cannot create the `br-lan` bridge inside a container, so the OpenWrt container's `lan` sits on `eth0`. Its bridge table and VLANs are empty, and the unit tests cover reading them.

## Docs

The pages in `docs/` are published with [Zensical](<https://zensical.org>) to GitHub Pages, one version for each minor release and one for `main`. The `Docs` workflow builds every pull request that touches them with `make docs`, which fails on a broken link or anchor. A merge to `main` publishes the `dev` version, and a `v*` tag publishes that minor version as `latest`. Both are pushed to the `gh-pages` branch with the Zensical fork of [mike](<https://github.com/squidfunk/mike>).

When you change the docs:

- Add a new page to `nav` in `mkdocs.yml`, and to the `llmstxt` sections there, or the site leaves it out of the sidebar and `llms.txt`.
- A new plugin gets a page under `docs/plugins/` and a line in the table on the plugins overview. A DNS resolver also gets a column on [Record what devices look up](<https://pushkar-anand.github.io/jocasta/dev/plugins/dns/index.md>).
- A new setting gets a row in the [configuration reference](<https://pushkar-anand.github.io/jocasta/dev/reference/configuration/index.md>). `TestReferenceDocumentsEveryKey` fails until it has one, and fails on a row for a setting that no longer exists.
- Keep `docs/setup.md` and `docs/mcp.md`. Earlier releases link to their sections on GitHub, and each section there points to where it moved.

## Release

After CI passes on `main`, run the following. It runs `make e2e-full` first, so Chrome must be installed.

```
./scripts/release.sh v0.10.0
```

The script requires a clean working tree and a local `main` that matches `origin/main`. It checks that the tag is unused, creates an annotated tag, and pushes it to trigger the release workflow. It does not wait for the build.

The GitHub release and the changelog page are written from the commit messages, as `cliff.toml` sets out. To give a minor release highlights, merge `docs/releases/<major.minor>.md` before tagging its `.0`.

## Layout

- `cmd/jocasta`: CLI entry point.
- `internal/scanner`: the ICMP sweep, and the reverse DNS, mDNS, NetBIOS, DNS-SD and SSDP lookups that name what answers.
- `internal/plugin`: sources beyond the sweep (RouterOS, OpenWrt, NetFlow, Technitium, Pi-hole, AdGuard Home).
- `internal/inventory`: the store, covering identity resolution, address handling, device classification and the change log.
- `internal/classify`: the rules that guess a device's type from its vendor, name and open ports.
- `internal/notify`: sends each finished scan's changes to ntfy, a webhook or a templated HTTP request.
- `internal/web`, `internal/api`: the HTML UI and the JSON API.
- `internal/db`: connection, migrations, generated queries.

Queries are SQLC-generated from `internal/db/queries/`. The schema is in `internal/db/migrations/`.

## Add a notification service

Most services take an HTTP request, and the `http` provider reaches them with a body template and no new code. For one of those, add a recipe to [Send to another service](<https://pushkar-anand.github.io/jocasta/dev/notifications/other-services/index.md>). `TestRecipesInTheDocsRender` renders every recipe there, so a broken one fails the tests.

A service gets a provider of its own only when the `http` provider cannot reach it: it needs more than one request, a protocol other than HTTP, or signing a template cannot do. Each provider is in `internal/notify`, in a file of its own, as `ntfy.go` and `webhook.go` are:

1. Add a struct with `koanf` tags for its settings, and give it `Validate`, `Host` and `Send`. `Send` must not put a credential in its error. `post` covers a service that takes an HTTP request.
2. Add a field for it to `notify.Config`, and a line to `Config.provider`.
3. Add an example to `jocasta.example.yaml`, and a row for each setting to the [configuration reference](<https://pushkar-anand.github.io/jocasta/dev/reference/configuration/index.md>).

The notifier, the settings page, the store and `cmd` need no change.

## Add a name source

Each name carries a standing, `dbtype.HostnameSource`, which says how it was learned. When sources disagree, the name with the higher standing is shown.

1. Add a constant for it in `internal/db/dbtype/enum.go` and add it to `hostnameSources`.
2. Give it a place in `HostnameSource.Rank`, and say why in the comment there. A name that resolves ranks above one a device only claims.
3. Word it for the device page in `standing`, in `internal/web/funcs.go`.
4. List it in [Name devices](<https://pushkar-anand.github.io/jocasta/dev/naming/index.md>).

A sweep that asks devices for their names takes two more steps:

1. Add its lookup in `internal/scanner`, in a file of its own, and ask it in `enrich` in rank order. A query sent to each host is a `nameProtocol`, as in `mdns.go` and `netbios.go`. Any other lookup is a `nameLookup`, as `askSSDP` in `ssdp.go` is, or names hosts itself, as `browseServices` does with `dnssd.go`.
2. Add it to `HostnameSource.Swept`, so a lower-ranked lookup never replaces its name on the sweep's claim.

The column has no CHECK, so a new standing needs no migration.

## Screenshots

The images in [`docs/ui.md`](<https://pushkar-anand.github.io/jocasta/dev/ui/index.md>) are taken against a database of fabricated devices, never real network data: addresses from RFC 5737, hardware addresses from RFC 7042, invented names. Internet peers are well-known public services, so their organisations and countries resolve.
