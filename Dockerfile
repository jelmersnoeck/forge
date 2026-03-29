FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /forge-server ./cmd/server
RUN CGO_ENABLED=0 go build -o /forge-agent ./cmd/agent

# ── Runtime ───────────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache git bash ripgrep curl tmux

COPY --from=builder /forge-server /usr/local/bin/forge-server
COPY --from=builder /forge-agent /usr/local/bin/forge-agent

ENV GATEWAY_HOST=0.0.0.0
ENV AGENT_BIN=/usr/local/bin/forge-agent
CMD ["forge-server"]
