# agent/Dockerfile  (build context: repository root)
#
#   docker build -f agent/Dockerfile -t arkeep-agent .

# ─── Stage 1: download restic + rclone ────────────────────────────────────────
FROM golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS downloader

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
FROM golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder

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
FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

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

# rclone reads its config from $HOME/.config/rclone, and HOME changes from
# /root to /home/arkeep when PUID is set. A fixed path lets one documented
# mount work in both modes (see deploy/docker/docker-compose.agent.yml).
ENV RCLONE_CONFIG=/etc/arkeep/rclone/rclone.conf

ENTRYPOINT ["entrypoint.sh"]
CMD ["arkeep-agent"]