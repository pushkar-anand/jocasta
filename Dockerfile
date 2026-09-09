# syntax=docker/dockerfile:1

# ---- build ------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build

WORKDIR /src

# Dependencies resolve from go.mod/go.sum alone, so this layer is only
# invalidated when they change, not on every source edit.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# The binary is fully static: the sqlite driver is pure Go and the migrations,
# templates and statics are embedded, so the runtime image needs nothing but
# the binary itself.
#
# TARGETOS/TARGETARCH are supplied by buildx; they default to the build
# platform when building with plain `docker build`.
ARG TARGETOS
ARG TARGETARCH

# The build context excludes .git, so the toolchain cannot stamp the version
# itself. The release workflow passes it in; a plain `docker build` leaves these
# empty and `jocasta version` reports "dev".
ARG VERSION
ARG COMMIT
ARG DATE
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w \
      -X github.com/pushkar-anand/jocasta/internal/version.tag=${VERSION} \
      -X github.com/pushkar-anand/jocasta/internal/version.commit=${COMMIT} \
      -X github.com/pushkar-anand/jocasta/internal/version.date=${DATE}" \
    -o /out/jocasta ./cmd/jocasta

# The database directory is created here so it can be copied in with the right
# ownership; the runtime image has no shell to mkdir with.
RUN install -d -m 0755 -o 65532 -g 65532 /out/data

# ---- runtime ----------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/jocasta /usr/local/bin/jocasta
COPY --from=build --chown=65532:65532 /out/data /data

# EXPOSE metadata only, and not mirrored into JOCASTA_SERVER__PORT: a baked ENV
# outranks the config file, so pinning the port here would keep server.port in a
# mounted jocasta.yaml from ever taking effect. The app's own default is 8080.
ARG PORT=8080

# Bind on every interface (the app's own default is localhost, unreachable from
# outside a container) and keep the SQLite file on the /data volume.
ENV JOCASTA_SERVER__HOST=0.0.0.0 \
    JOCASTA_DB__PATH=/data \
    JOCASTA_DB__NAME=jocasta.db

VOLUME ["/data"]
EXPOSE ${PORT}

USER nonroot:nonroot
WORKDIR /data

ENTRYPOINT ["/usr/local/bin/jocasta"]
