# Install

The [quick start](<https://github.com/pushkar-anand/jocasta#quick-start>) covers the common case: the container on the host network. This page covers the other ways to install, and what to do on the first run.

## Container image

Multi-arch images (amd64 and arm64) are on the GitHub Container Registry, tagged `latest` and with each release version, such as `:v0.3.0`. The image is distroless and runs as a non-root user. It listens on `0.0.0.0:8080`, keeps its SQLite file on the `/data` volume, and reads `/data/jocasta.yaml` if one is mounted.

Use host networking (`--network host`) if you can. On a bridge network (`-p 8080:8080`) the sweep still finds hosts, but it can only read the container's own neighbour table, so devices have no hardware address, and Jocasta identifies devices by hardware address. [Reading the router](<https://pushkar-anand.github.io/jocasta/0.11/plugins/index.md>) fills this gap. No added capability or sysctl is needed either way.

## Prebuilt binaries

Every release publishes archives for Linux, macOS and Windows, on amd64 and arm64, with a `checksums.txt`, on the [releases page](<https://github.com/pushkar-anand/jocasta/releases>).

1. Extract `jocasta` from the archive for your system.
2. Put a `jocasta.yaml` next to it.
3. From that directory, run `jocasta serve`. It runs without root.

The binary listens on `localhost:8080` by default. Set `server.host: 0.0.0.0` to reach it from other machines.

## From source

Needs Go 1.27 or newer.

```
git clone https://github.com/pushkar-anand/jocasta.git
cd jocasta
make build            # -> bin/jocasta
make docker           # -> jocasta:latest
```

## First run

On first visit the web UI asks you to create the admin account. Further users and API tokens are under Settings.

Until the admin account exists, whoever opens `/setup` first creates it. Finish setup straight after the first start, before anyone else on the network can reach the page.

The session cookie is HTTPS-only by default. If you reach Jocasta over plain HTTP from anywhere but `localhost`, sign-in does not stick until you set `server.auth.cookie_secure: false`. Putting it behind a TLS reverse proxy is better.

The database schema is embedded, and migrations run when the binary opens the database. Jocasta is pre-1.0, so check the release notes before upgrading.
