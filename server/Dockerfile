# server/Dockerfile  (build context: repository root)
#
#   docker build -f server/Dockerfile -t arkeep-server .

# ─── Stage 1: build GUI ───────────────────────────────────────────────────────
FROM node:26.9.0-alpine3.24@sha256:dbaa92e5758cbbcf85d65d5403fdb530fe3442cbe8c6dbfb7ef23365450d5070 AS gui-builder

# Node >= 25 no longer bundles corepack; install it to get the pnpm version
# pinned by the packageManager field in gui/package.json. Corepack checks
# pnpm against the sha512 recorded there; corepack itself is pinned exactly
# and npm checks it against the registry's integrity hash (ADR 0013).
RUN npm install -g corepack@0.36.0 && corepack enable

WORKDIR /src/gui

COPY gui/package.json gui/pnpm-lock.yaml* gui/pnpm-workspace.yaml* ./
RUN pnpm install --frozen-lockfile

COPY gui/ ./
RUN pnpm build

# ─── Stage 2: build server ────────────────────────────────────────────────────
FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder

RUN apk add --no-cache git

WORKDIR /src

COPY go.work go.work.sum* ./
COPY shared/  ./shared/
COPY server/  ./server/

# Inject the GUI dist built in stage 1 into the go:embed path
COPY --from=gui-builder /src/gui/dist ./server/cmd/server/dist

# All Go commands run from /src/server with GOWORK=off.
# agent/ is not present in the build context so go.work cannot be satisfied.
# The replace directive in server/go.mod (../shared) resolves to /src/shared
# which is present, so the build works correctly without the workspace.
WORKDIR /src/server

RUN GOWORK=off go mod download

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off \
    go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/arkeep-server \
    ./cmd/server

# ─── Stage 2: runtime ─────────────────────────────────────────────────────────
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

RUN apk upgrade --no-cache && \
    apk add --no-cache ca-certificates tzdata && \
    addgroup -S arkeep && \
    adduser  -S -G arkeep arkeep

COPY --from=builder /out/arkeep-server /usr/local/bin/arkeep-server

RUN mkdir -p /var/lib/arkeep/data && \
    chown -R arkeep:arkeep /var/lib/arkeep

USER arkeep

EXPOSE 8080
EXPOSE 9090

VOLUME ["/var/lib/arkeep"]

ENTRYPOINT ["arkeep-server"]