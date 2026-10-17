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

`internal/web/e2e` drives Chrome over the real server. It builds with the `e2e` tag, so `go test ./...` leaves it out. It needs Google Chrome or Chromium on `PATH`, or `E2E_CHROME` set to one of them.

The tests draw each page over four fabricated inventories: empty, a home network, a network with extreme data, and a network with no accounts. They also draw each dialog, menu and inline edit that the page opens. Each drawing is checked for these problems:

- sideways scrolling
- clipped or overlapping text
- small tap targets and small text
- WCAG 2.2 AA failures (axe-core)
- script errors and failed requests

`make e2e` draws every page as admin on a phone, a tablet and a laptop, in both themes. It takes about two minutes. CI does not run it. So run it before you open a pull request that changes the UI. `make e2e-full` adds every other screen and account. `scripts/release.sh` runs it before it tags a release.

A finding fails the run, unless `testdata/known.json` lists it under an open issue. A fix deletes its entries. `make e2e-full` also fails on an entry that no longer matches a finding.

The run also compares the first screen of every page on the home network with a stored picture in `testdata/golden/`. It does this on a phone, a tablet and a laptop in the light theme, and on the laptop in the dark theme. A picture fails when more than 64 of its pixels changed. One added or removed word changes more pixels than that. If you change the UI on purpose, store the new pictures and commit them with the change:

```
E2E_UPDATE=1 make e2e
```

To see what changed, set `E2E_ARTIFACTS` to a directory. The run writes each picture that differs there, next to a map of the changed pixels.

To look at a fixture in a browser:

```
E2E_SERVE=weird go test -tags e2e -run TestServe -timeout 0 ./internal/web/e2e/
```

To capture every page and read the findings, without a pass or fail:

```
E2E_AUDIT=$PWD/tmp/audit TMPDIR=$PWD/tmp go test -tags e2e -run TestAudit -timeout 0 ./internal/web/e2e/
```

To limit either run, set `E2E_FIXTURES`, `E2E_ROLES`, `E2E_VIEWPORTS`, `E2E_THEMES` or `E2E_PAGES` to a comma-separated list.

## Smoke tests

`make smoke` reads real OpenWrt, RouterOS, Pi-hole, AdGuard Home and Technitium servers and a real NetFlow exporter, each in a container. It checks what jocasta says about them. The plugin tests use canned replies. The smoke tests find a server that changed what it sends. They need Docker and Go. CI does not run them. So run them before you open a pull request that changes one of these plugins or its client under `pkg/`.

`scripts/smoke/smoke.sh up` starts the servers on their own network, `198.51.100.0/24`:

- OpenWrt has the setup that [the OpenWrt page](<https://pushkar-anand.github.io/jocasta/0.14/plugins/openwrt/index.md>) gives, with the ACL in `docs/openwrt/jocasta.json`. It also gets leases and static hosts.
- RouterOS CHR runs in QEMU, and `scripts/smoke/routeros.sh` boots it. Its `ether1` gets an address over DHCP, as a WAN port does. Its `ether2` is on the smoke network. It gets a read-only `jocasta` user, a bridge with its own address, a DHCP server with a static lease, and a static ARP entry. It also gets Traffic Flow that exports IPFIX, with the setup from [the RouterOS page](<https://pushkar-anand.github.io/jocasta/0.14/plugins/routeros/#export-traffic-flows>).
- Pi-hole, AdGuard Home and Technitium each block a name. AdGuard Home also rewrites one name. Technitium has the setup that [the Technitium page](<https://pushkar-anand.github.io/jocasta/0.14/plugins/technitium/index.md>) gives: a user that can view Logs, its API token, and the Query Logs (Sqlite) app. Pi-hole is the upstream of the other two, so no lookup leaves the network.
- pmacct runs in the network namespace of the OpenWrt container. It exports NetFlow v9 to this host, as the router would.
- The OpenWrt container sends lookups to each resolver.

Each server publishes its web interface on `127.0.0.1`, and jocasta reads it there.

`scripts/smoke/smoke.sh check` runs `jocasta plugin run` against each server and checks the rows that it prints. It also runs these tests:

- `TestNetFlowLive` in `internal/plugin`, two times. The first time, the OpenWrt container makes a lookup. The second time, this host makes a request to the router. The script checks the flows that each run decodes.
- The live tests in `pkg/openwrt`, `pkg/routeros` and `internal/plugin`.

The script writes its config to `$TMPDIR/jocasta-smoke/jocasta.yaml`. So you can point `jocasta serve` at the same servers. `make smoke-down` removes the servers.

Some services need more than Docker. To leave a service out, add it to `SMOKE_SKIP`, such as `SMOKE_SKIP="technitium netflow" make smoke`:

- `routeros`: RouterOS runs under QEMU. If the host has no KVM, QEMU emulates the CPU. In both cases, it boots in less than a minute. Its container joins a second network, which needs Docker 28 or later.
- `technitium`: the server downloads the Query Logs app from the app store of Technitium. So its container needs internet access.
- `netflow`: the exporter sends to the gateway address of the network. Only Docker on Linux puts this address on the host.

To change a setting, set the matching variable:

- `SMOKE_REGISTRY` pulls the images from a mirror, such as `mirror.gcr.io`. Use it when Docker Hub limits your pulls.
- `SMOKE_PIHOLE_IMAGE`, `SMOKE_ADGUARD_IMAGE`, `SMOKE_OPENWRT_IMAGE`, `SMOKE_ROUTEROS_IMAGE`, `SMOKE_TECHNITIUM_IMAGE` and `SMOKE_PMACCT_IMAGE` test a different release. MikroTik publishes no image. The RouterOS image is `evilfreelancer/docker-routeros`, which contains the CHR disk and QEMU.
- `SMOKE_PIHOLE_PORT`, `SMOKE_ADGUARD_PORT`, `SMOKE_ADGUARD_SETUP_PORT`, `SMOKE_OPENWRT_PORT`, `SMOKE_TECHNITIUM_PORT` and `SMOKE_ROUTEROS_PORT` change the published ports, 18081 to 18086 by default. `SMOKE_FLOW_PORT` and `SMOKE_ROUTEROS_FLOW_PORT` change the ports for the NetFlow and IPFIX exports, 2055 and 2056 by default.

netifd cannot create the `br-lan` bridge in a container. So the `lan` of the OpenWrt container is on `eth0`. Its bridge table and VLANs are empty. The unit tests cover the reads of these tables.

## Docs

[Zensical](<https://zensical.org>) publishes the pages in `docs/` to GitHub Pages. There is one version for each minor release and one for `main`. The `Docs` workflow builds every pull request that changes the docs with `make docs`. This build fails on a broken link or anchor. A merge to `main` publishes the `dev` version. A `v*` tag publishes its minor version as `latest`. The Zensical fork of [mike](<https://github.com/squidfunk/mike>) pushes both to the `gh-pages` branch.

When you change the docs:

- Add a new page to `nav` in `mkdocs.yml`, and to the `llmstxt` sections there. If you do not, the site leaves it out of the sidebar and `llms.txt`.
- A new plugin gets a page under `docs/plugins/` and a line in the table on the plugins overview. A DNS resolver also gets a column on [Record what devices look up](<https://pushkar-anand.github.io/jocasta/0.14/plugins/dns/index.md>).
- A new setting gets a row in the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.14/reference/configuration/index.md>). `TestReferenceDocumentsEveryKey` fails until the setting has a row. It also fails on a row for a setting that no longer exists.
- Keep `docs/setup.md` and `docs/mcp.md`. Earlier releases link to their sections on GitHub. Each section there points to the new location.

## Release

After CI passes on `main`, run the command below. It runs `make e2e-full` first, so Chrome must be installed.

```
./scripts/release.sh v0.10.0
```

The script needs a clean working tree and a local `main` that matches `origin/main`. It checks that the tag is not used. Then it creates an annotated tag and pushes it, which starts the release workflow. It does not wait for the build.

git-cliff writes the GitHub release and the changelog page from the commit messages, as `cliff.toml` sets. To give a minor release highlights, merge `docs/releases/<major.minor>.md` before you tag its `.0`.

## Layout

- `cmd/jocasta`: CLI entry point.
- `internal/scanner`: the ICMP sweep, and the reverse DNS, mDNS, NetBIOS, DNS-SD and SSDP lookups that name the hosts that answer.
- `internal/plugin`: sources other than the sweep (RouterOS, OpenWrt, NetFlow, Technitium, Pi-hole, AdGuard Home).
- `internal/inventory`: the store. It covers identity resolution, address handling, device classification and the change log.
- `internal/classify`: the rules that guess the type of a device from its vendor, name and open ports.
- `internal/notify`: sends the changes of each finished scan to ntfy, a webhook or a templated HTTP request.
- `internal/web`, `internal/api`: the HTML UI and the JSON API.
- `internal/db`: connection, migrations, generated queries.

SQLC generates the queries from `internal/db/queries/`. The schema is in `internal/db/migrations/`.

## Add a notification service

Most services accept an HTTP request. The `http` provider reaches them with a body template and no new code. For such a service, add a recipe to [Send to another service](<https://pushkar-anand.github.io/jocasta/0.14/notifications/other-services/index.md>). `TestRecipesInTheDocsRender` renders every recipe on that page. So a broken recipe fails the tests.

A service gets its own provider only when the `http` provider cannot reach it. For example, the service needs more than one request, a protocol other than HTTP, or a signature that a template cannot make. Each provider is in its own file in `internal/notify`, as `ntfy.go` and `webhook.go` are:

1. Add a struct with `koanf` tags for its settings. Give it `Validate`, `Host` and `Send`. `Send` must not put a credential in its error. For a service that accepts an HTTP request, use `post`.
2. Add a field for it to `notify.Config`, and a line to `Config.provider`.
3. Add an example to `jocasta.example.yaml`. Add a row for each setting to the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.14/reference/configuration/index.md>).

The notifier, the settings page, the store and `cmd` need no change.

## Add a name source

Each name has a standing, `dbtype.HostnameSource`, which tells how Jocasta learned it. If sources give different names, Jocasta shows the name with the higher standing.

1. Add a constant for it in `internal/db/dbtype/enum.go`, and add it to `hostnameSources`.
2. Give it a place in `HostnameSource.Rank`. Give the reason in the comment there. A name that resolves ranks above a name that a device only claims.
3. Write its device page text in `standing`, in `internal/web/funcs.go`.
4. List it in [Name devices](<https://pushkar-anand.github.io/jocasta/0.14/naming/index.md>).

If a sweep asks devices for their names, do two more steps:

1. Add its lookup in its own file in `internal/scanner`. Ask it in `enrich`, in rank order. A query that goes to each host is a `nameProtocol`, as in `mdns.go` and `netbios.go`. Any other lookup is a `nameLookup`, as `askSSDP` in `ssdp.go` is. Or it names hosts itself, as `browseServices` does with `dnssd.go`.
2. Add it to `HostnameSource.Swept`. Then a lookup with a lower rank never replaces its name on the claim of the sweep.

The column has no CHECK, so a new standing needs no migration.

## Screenshots

The images in [`docs/ui.md`](<https://pushkar-anand.github.io/jocasta/0.14/ui/index.md>) come from a database of fabricated devices, never real network data: addresses from RFC 5737, hardware addresses from RFC 7042, and invented names. The internet peers are well-known public services, so Jocasta can find their organisations and countries.
