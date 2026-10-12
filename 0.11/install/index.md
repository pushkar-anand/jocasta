# Install

For a guided first run, see [Your first inventory](<https://pushkar-anand.github.io/jocasta/0.11/tutorial/index.md>).

## Container image

Images for amd64 and arm64 are on the GitHub Container Registry, tagged `latest` and per release, such as `:v0.11.1`. The image is distroless and runs as non-root. It listens on `0.0.0.0:8080`, keeps its database in `/data`, and reads `/data/jocasta.yaml`.

```
docker run -d --name jocasta --network host \
  -v jocasta-data:/data \
  -v ./jocasta.yaml:/data/jocasta.yaml:ro \
  ghcr.io/pushkar-anand/jocasta:latest
```

```
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

Use host networking. On a bridge network (`-p 8080:8080`) the sweep finds hosts but no hardware addresses, and mDNS, DNS-SD and SSDP names never arrive. A [router plugin](<https://pushkar-anand.github.io/jocasta/0.11/plugins/index.md>) fills the first gap. Neither mode needs extra capabilities.

## Prebuilt binaries

Archives for Linux, macOS and Windows, on amd64 and arm64, are on the [releases page](<https://github.com/pushkar-anand/jocasta/releases>), with a `checksums.txt`.

1. Extract `jocasta` from the archive for your system.
2. Put a `jocasta.yaml` next to it.
3. From that directory, run `jocasta serve`. No root needed.

It listens on `localhost:8080`. Set `server.host: 0.0.0.0` to reach it from other machines.

## From source

Needs Go 1.27 or newer.

```
git clone https://github.com/pushkar-anand/jocasta.git
cd jocasta
make build            # -> bin/jocasta
make docker           # -> jocasta:latest
```

## First run

The first visit asks for the admin account. Until it exists, anyone who reaches the page can create it, so do it straight away. More users: [Manage users and sign-in](<https://pushkar-anand.github.io/jocasta/0.11/operate/users/index.md>).

Over plain HTTP, sign-in needs `server.auth.cookie_secure: false`; see [Serve over HTTPS](<https://pushkar-anand.github.io/jocasta/0.11/operate/https/index.md>).

Migrations run at startup. Before upgrading, see [Back up and upgrade](<https://pushkar-anand.github.io/jocasta/0.11/operate/backup/index.md>).
