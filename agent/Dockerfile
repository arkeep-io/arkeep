# agent/Dockerfile  (build context: repository root)
#
#   docker build -f agent/Dockerfile -t arkeep-agent .

# ─── Stage 1: download restic + rclone ────────────────────────────────────────
FROM golang:1.26-alpine AS downloader

RUN apk add --no-cache ca-certificates git

WORKDIR /src

COPY go.work go.work.sum* ./
COPY shared/  ./shared/
COPY agent/   ./agent/

# Run from /src/agent with GOWORK=off — server/ is not in the build context.
WORKDIR /src/agent
RUN GOWORK=off go run ./scripts/download_deps.go

# ─── Stage 2: compile ─────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

COPY go.work go.work.sum* ./
COPY shared/  ./shared/
COPY agent/   ./agent/

COPY --from=downloader /src/agent/internal/restic/bin/ ./agent/internal/restic/bin/

# All Go commands run from /src/agent with GOWORK=off.
# server/ is not present in the build context so go.work cannot be satisfied.
# The replace directive in agent/go.mod (../shared) resolves to /src/shared
# which is present, so the build works correctly without the workspace.
WORKDIR /src/agent

RUN GOWORK=off go mod download

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off \
    go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/arkeep-agent \
    ./cmd/agent

# ─── Stage 3: runtime ─────────────────────────────────────────────────────────
FROM alpine:3.23

RUN apk upgrade --no-cache && \
    apk add --no-cache ca-certificates tzdata su-exec && \
    addgroup -S arkeep && \
    adduser  -S -G arkeep arkeep

COPY --from=builder /out/arkeep-agent /usr/local/bin/arkeep-agent
COPY agent/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

RUN mkdir -p /var/lib/arkeep-agent /arkeep-backups && \
    chown arkeep:arkeep /var/lib/arkeep-agent /arkeep-backups

VOLUME ["/var/lib/arkeep-agent"]

ENV ARKEEP_STATE_DIR=/var/lib/arkeep-agent

ENTRYPOINT ["entrypoint.sh"]
CMD ["arkeep-agent"]