# deploy/server/Dockerfile
#
# Multi-stage build for the Arkeep server binary.
#
# Build context must be the repository root so that the go.work workspace
# can resolve the local `shared` module (replace directive in server/go.mod).
#
#   docker build -f deploy/server/Dockerfile -t arkeep-server .

# ─── Stage 1: build ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

# git is needed by `go build` to stamp VCS info when using go.work.
RUN apk add --no-cache git

WORKDIR /src

# Copy the workspace descriptor and all modules so go.work can resolve
# the shared module replace directive without a network round-trip.
COPY go.work go.work.sum* ./
COPY shared/  ./shared/
COPY server/  ./server/

# Download server dependencies as a separate layer so Docker can cache
# them across source-only changes.
WORKDIR /src/server
RUN go mod download

# Build a fully static binary.
# CGO_ENABLED=0 is safe because the SQLite driver (modernc.org/sqlite) is
# a pure-Go reimplementation — no GCC or glibc required.
# -s -w strips debug symbols to reduce binary size.
WORKDIR /src
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux \
    go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/arkeep-server \
    ./server/cmd/server

# ─── Stage 2: runtime ─────────────────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates: required for TLS (OIDC providers, SMTP over TLS)
# tzdata: allows the server to respect TZ env var for log timestamps
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S arkeep && \
    adduser  -S -G arkeep arkeep

COPY --from=builder /out/arkeep-server /usr/local/bin/arkeep-server

# /var/lib/arkeep      — SQLite database (default ARKEEP_DB_DSN path)
# /var/lib/arkeep/data — RSA JWT key pair (ARKEEP_DATA_DIR)
# Both are backed by the named volume declared in the compose files.
RUN mkdir -p /var/lib/arkeep/data && \
    chown -R arkeep:arkeep /var/lib/arkeep

USER arkeep

# REST API + GUI
EXPOSE 8080
# gRPC — agents connect here
EXPOSE 9090

VOLUME ["/var/lib/arkeep"]

# All configuration is read from environment variables (see .env.example).
# No CLI flags needed — cobra reads ARKEEP_* env vars automatically.
ENTRYPOINT ["arkeep-server"]