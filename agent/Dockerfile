# agent/Dockerfile  (build context: repository root)
#
#   docker build -f agent/Dockerfile -t arkeep-agent .

# ─── Stage 1: download restic + rclone ────────────────────────────────────────
FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS downloader

RUN apk add --no-cache ca-certificates git

WORKDIR /src

COPY go.work go.work.sum* ./
COPY shared/  ./shared/
COPY agent/   ./agent/

# Run from /src/agent with GOWORK=off — server/ is not in the build context.
# The optional github_token secret authenticates the restic download through
# the GitHub API (CI passes it); a secret mount never ends up in a layer.
WORKDIR /src/agent
RUN --mount=type=secret,id=github_token,env=GITHUB_TOKEN \
    GOWORK=off go run ./scripts/download_deps.go

# ─── Stage 2: compile ─────────────────────────────────────────────────────────
FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder

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
FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

RUN apk upgrade --no-cache && \
    apk add --no-cache ca-certificates tzdata su-exec docker-cli docker-cli-compose && \
    addgroup -S arkeep && \
    adduser  -S -G arkeep arkeep

COPY --from=builder /out/arkeep-agent /usr/local/bin/arkeep-agent
COPY agent/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

RUN mkdir -p /var/lib/arkeep-agent /arkeep-backups && \
    chown arkeep:arkeep /var/lib/arkeep-agent

VOLUME ["/var/lib/arkeep-agent"]

ENV ARKEEP_STATE_DIR=/var/lib/arkeep-agent

ENTRYPOINT ["entrypoint.sh"]
CMD ["arkeep-agent"]