# server/Dockerfile  (build context: repository root)
#
#   docker build -f server/Dockerfile -t arkeep-server .

# ─── Stage 1: build ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

COPY go.work go.work.sum* ./
COPY shared/  ./shared/
COPY server/  ./server/

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
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
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