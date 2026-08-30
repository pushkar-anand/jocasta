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
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/jocasta ./cmd/jocasta

# The database directory is created here so it can be copied in with the right
# ownership; the runtime image has no shell to mkdir with.
RUN install -d -m 0755 -o 65532 -g 65532 /out/data

# ---- runtime ----------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/jocasta /usr/local/bin/jocasta
COPY --from=build --chown=65532:65532 /out/data /data

# Defaults for a container: bind on every interface (the application's own
# default is localhost, which would be unreachable from outside) and keep the
# SQLite file on /data so it can be given a volume.
ENV JOCASTA_SERVER__HOST=0.0.0.0 \
    JOCASTA_SERVER__PORT=8080 \
    JOCASTA_DB__PATH=/data \
    JOCASTA_DB__NAME=jocasta.db

VOLUME ["/data"]
EXPOSE 8080

USER nonroot:nonroot
WORKDIR /data

ENTRYPOINT ["/usr/local/bin/jocasta"]
