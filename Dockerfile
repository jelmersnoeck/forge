FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /forge ./cmd/forge

# The gateway needs to drive git (worktrees, commits) and access bind-mounted
# host paths whose owner UID won't match a distroless nonroot user. Stay on
# alpine + root for the gateway image; the bridge image is the locked-down one.
FROM alpine:3.20

# tmux is required at runtime: the gateway uses tmux to host the per-session
# Forge agent processes (see internal/server/backend/tmux.go). Without it,
# EnsureAgent fails with "tmux: executable file not found in $PATH" and the
# whole service is silently broken from the outside. Caught the hard way on
# 2026-06-11 — the gateway image had been missing tmux for ~2 weeks and the
# only symptom was 5xx responses with a buried error string. Don't drop it.
RUN apk add --no-cache git ca-certificates tini tmux

COPY --from=builder /forge /usr/local/bin/forge

# Forge gateway listens here.
EXPOSE 3000

# Mounted volumes:
#   /workspace - host workspace dir (where Forge does code work)
#   /sessions  - persistent session state
VOLUME ["/workspace", "/sessions"]

ENV WORKSPACE_DIR=/workspace \
    SESSIONS_DIR=/sessions \
    GATEWAY_PORT=3000 \
    GATEWAY_HOST=0.0.0.0 \
    FORGE_BIN=/usr/local/bin/forge

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/forge", "gateway"]
