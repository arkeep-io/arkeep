# server/Dockerfile  (build context: repository root)
#
#   docker build -f server/Dockerfile -t arkeep-server .

# ─── Stage 1: build GUI ───────────────────────────────────────────────────────
FROM node:26.13.1-alpine3.24@sha256:6bd9ab43c0ec0190ca7b2d193906cb8b7edf6e7f4afa43ddeccd6452710bffb5 AS gui-builder

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
FROM golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder

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
FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

# sqlite provides sqlite3 for the documented online backup of the database
# (docker exec arkeep-server sqlite3 … ".backup …", scripts/backup-arkeep-db.sh).
RUN apk upgrade --no-cache && \
    apk add --no-cache ca-certificates tzdata sqlite && \
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