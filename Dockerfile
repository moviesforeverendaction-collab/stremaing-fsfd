# ── Stage 1: Build Go binary ──────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.25-alpine3.21 AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /app/fsb -ldflags="-w -s" ./cmd/fsb

# ── Stage 2: Runtime image with Python for the embedded UI bot ────────────────
# We use python:3.12-alpine so both the Go binary AND Python are available.
FROM python:3.12-alpine3.21

# Install CA certs (needed by Go TLS) and tini (proper signal handling)
RUN apk add --no-cache ca-certificates tini

WORKDIR /app

# Copy Go binary
COPY --from=builder /app/fsb /app/fsb

# Pre-install Python dependencies so startup is instant
# (Go will also run pip install on first boot, but this layer caches it)
COPY internal/pybot/requirements.txt /app/pybot_requirements.txt
RUN pip install --no-cache-dir -r /app/pybot_requirements.txt

EXPOSE ${PORT}

# tini ensures SIGTERM is forwarded to Go binary → Go stops Python subprocess cleanly
ENTRYPOINT ["/sbin/tini", "--", "/app/fsb", "run"]
