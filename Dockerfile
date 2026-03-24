# ── Stage 1: Build Go binary ──────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.25-alpine3.21 AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -o /app/fsb -ldflags="-w -s" ./cmd/fsb

# ── Stage 2: Runtime — Python 3.12 Alpine ─────────────────────────────────────
# python:3.12-alpine gives us python3 + pip on a minimal image.
# tini handles signal forwarding so Ctrl-C / SIGTERM cleanly stops both
# the Go binary and the Python subprocess it manages.
FROM python:3.12-alpine3.21

RUN apk add --no-cache ca-certificates tini

WORKDIR /app

# Copy the Go binary from the builder stage
COPY --from=builder /app/fsb /app/fsb

EXPOSE ${PORT}

# tini as PID 1 → forwards SIGTERM to /app/fsb → Go calls pybot.Stop() → Python exits cleanly
ENTRYPOINT ["/sbin/tini", "--", "/app/fsb", "run"]
