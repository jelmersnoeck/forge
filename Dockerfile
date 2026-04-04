FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /forge ./cmd/forge

# ── Runtime ───────────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache git bash ripgrep curl tmux

COPY --from=builder /forge /usr/local/bin/forge

ENV GATEWAY_HOST=0.0.0.0
ENV FORGE_BIN=/usr/local/bin/forge
CMD ["forge", "server"]
