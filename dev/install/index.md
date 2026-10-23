# Install

For a guided first run, see [Your first inventory](<https://pushkar-anand.github.io/jocasta/dev/tutorial/index.md>).

## Container image

Images for amd64 and arm64 are on the GitHub Container Registry. Each image has the tag `latest` and a release tag, such as `:v0.11.1`. The image is distroless and does not run as root. It listens on `0.0.0.0:8080`, keeps its database in `/data`, and reads `/data/jocasta.yaml`.

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

Use host networking. On a bridge network (`-p 8080:8080`), the sweep finds hosts but not their hardware addresses. Also, mDNS, DNS-SD and SSDP names do not arrive. A [router plugin](<https://pushkar-anand.github.io/jocasta/dev/plugins/index.md>) can supply the hardware addresses. Neither mode needs extra capabilities.

Host networking reaches your network only on Linux. On macOS and Windows, Docker runs containers in a VM, so use the [prebuilt binary](<#prebuilt-binaries>).

## Prebuilt binaries

Archives for Linux, macOS and Windows, on amd64 and arm64, are on the [releases page](<https://github.com/pushkar-anand/jocasta/releases>). A `checksums.txt` file is with them.

1. Extract `jocasta` from the archive for your system.
2. Put a `jocasta.yaml` next to it.
3. From that directory, run `jocasta serve`. It does not need root.

It listens on `localhost:8080`. To reach it from other machines, set `server.host: 0.0.0.0`.

## From source

You need Go 1.27 or newer.

```
git clone https://github.com/pushkar-anand/jocasta.git
cd jocasta
make build            # -> bin/jocasta
make docker           # -> jocasta:latest
```

## First run

On the first visit, Jocasta asks you to create the admin account. Until this account exists, anyone who reaches the page can create it. So create it immediately. To add more users, see [Manage users and sign-in](<https://pushkar-anand.github.io/jocasta/dev/operate/users/index.md>).

To sign in over plain HTTP, set `server.auth.cookie_secure: false`. See [Serve over HTTPS](<https://pushkar-anand.github.io/jocasta/dev/operate/https/index.md>).

Jocasta runs database migrations when it starts. Before you upgrade, see [Back up and upgrade](<https://pushkar-anand.github.io/jocasta/dev/operate/backup/index.md>).
