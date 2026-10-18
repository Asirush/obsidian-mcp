# syntax=docker/dockerfile:1

# --- Build Stage ---
FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/obsidian-mcp ./cmd/obsidian-mcp

# --- Runtime Stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /out/obsidian-mcp /usr/local/bin/obsidian-mcp

RUN mkdir -p /vault && chmod 777 /vault

ENV OBSIDIAN_VAULT_PATH=/vault
ENV OBSIDIAN_MCP_HTTP=0.0.0.0:8080

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/health >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/usr/local/bin/obsidian-mcp"]

