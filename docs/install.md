# Install

To try Jocasta with the fewest choices, follow
[Your first inventory](tutorial.md). This page covers every way to install.

## Container image

Multi-arch images (amd64 and arm64) are on the GitHub Container Registry,
tagged `latest` and with each release version, such as `:v0.11.1`. The image
is distroless and runs as a non-root user. It listens on `0.0.0.0:8080`, keeps
its SQLite file on the `/data` volume, and reads `/data/jocasta.yaml` if one
is mounted.

=== "docker run"

    ```bash
    docker run -d --name jocasta --network host \
      -v jocasta-data:/data \
      -v ./jocasta.yaml:/data/jocasta.yaml:ro \
      ghcr.io/pushkar-anand/jocasta:latest
    ```

=== "Compose"

    ```yaml
    services:
      jocasta:
        image: ghcr.io/pushkar-anand/jocasta:latest
        network_mode: host
        restart: unless-stopped
        volumes:
          - jocasta-data:/data
          - ./jocasta.yaml:/data/jocasta.yaml:ro

    volumes:
      jocasta-data:
    ```

Use host networking if you can. On a bridge network (`-p 8080:8080`) the sweep
still finds hosts, but it reads only the container's own neighbour table, so
no device gets a hardware address. A [router plugin](plugins/index.md) fills
this gap. Host networking also lets the mDNS, DNS-SD and SSDP lookups reach
your devices. No added capability or sysctl is needed either way.

## Prebuilt binaries

Every release publishes archives for Linux, macOS and Windows, on amd64 and
arm64, with a `checksums.txt`, on the [releases page][releases].

1. Extract `jocasta` from the archive for your system.
2. Put a `jocasta.yaml` next to it.
3. From that directory, run `jocasta serve`. It runs without root.

The binary listens on `localhost:8080` by default. Set `server.host: 0.0.0.0`
to reach it from other machines.

[releases]: https://github.com/pushkar-anand/jocasta/releases

## From source

Needs Go 1.27 or newer.

```bash
git clone https://github.com/pushkar-anand/jocasta.git
cd jocasta
make build            # -> bin/jocasta
make docker           # -> jocasta:latest
```

## First run

On first visit the web UI asks you to create the admin account. Until it
exists, whoever opens the page first creates it, so finish setup straight
after the first start. To add more users, see
[Manage users and sign-in](operate/users.md).

The session cookie is sent over HTTPS only by default. If you reach Jocasta
over plain HTTP from anywhere but `localhost`, sign-in does not stick until
you set `server.auth.cookie_secure: false`. A reverse proxy with a
certificate is better; see [Serve over HTTPS](operate/https.md).

The database schema is embedded, and migrations run when the binary opens the
database. Before upgrading, see [Back up and upgrade](operate/backup.md).
