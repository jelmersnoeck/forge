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

RUN apk add --no-cache git ca-certificates tini

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
    GATEWAY_HOST=0.0.0.0

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/forge", "gateway"]
