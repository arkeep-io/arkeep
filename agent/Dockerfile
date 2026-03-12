# deploy/agent/Dockerfile
#
# Multi-stage build for the Arkeep agent binary.
#
# The agent embeds restic and rclone binaries via go:embed. In Docker we
# run the same download script used locally (agent/scripts/download_deps.go)
# in a dedicated stage so the downloads are cached as a separate layer and
# are not re-fetched on every source change.
#
# Build context must be the repository root:
#
#   docker build -f deploy/agent/Dockerfile -t arkeep-agent .

# ─── Stage 1: download restic + rclone ────────────────────────────────────────
FROM golang:1.26-alpine AS downloader

RUN apk add --no-cache ca-certificates git

WORKDIR /src

# Copy only what the download script needs to run.
COPY go.work go.work.sum* ./
COPY shared/  ./shared/
COPY agent/   ./agent/

# Run from agent/ so that the script's binDir ("internal/restic/bin") resolves
# relative to the agent module root — matching the behaviour of `task deps:download`
# which also runs with dir: agent.
WORKDIR /src/agent
RUN go run ./scripts/download_deps.go

# ─── Stage 2: compile ─────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

COPY go.work go.work.sum* ./
COPY shared/  ./shared/
COPY agent/   ./agent/

# Bring in the downloaded binaries from stage 1 so go:embed can include
# them at compile time.
COPY --from=downloader /src/agent/internal/restic/bin/ ./agent/internal/restic/bin/

WORKDIR /src/agent
RUN go mod download

WORKDIR /src
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux \
    go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/arkeep-agent \
    ./agent/cmd/agent

# ─── Stage 3: runtime ─────────────────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates: TLS connections to the server and backup destinations
# tzdata: respect TZ env var for log timestamps
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S arkeep && \
    adduser  -S -G arkeep arkeep

COPY --from=builder /out/arkeep-agent /usr/local/bin/arkeep-agent

# State directory for agent-state.json and extracted restic/rclone binaries.
# ARKEEP_STATE_DIR defaults to ~/.arkeep — we override it to a predictable
# path inside the container backed by a named volume.
RUN mkdir -p /var/lib/arkeep-agent && \
    chown arkeep:arkeep /var/lib/arkeep-agent

USER arkeep

# The agent does not expose any ports — it initiates outbound gRPC
# connections to the server (pull architecture).

VOLUME ["/var/lib/arkeep-agent"]

# ARKEEP_STATE_DIR tells the agent where to write its state and extracted
# binaries. All other config (ARKEEP_SERVER_ADDR, ARKEEP_AGENT_SECRET) is
# set via environment variables in the compose files.
ENV ARKEEP_STATE_DIR=/var/lib/arkeep-agent

ENTRYPOINT ["arkeep-agent"]