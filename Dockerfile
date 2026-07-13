# syntax=docker/dockerfile:1
# Skinniest Quick image: static Go binary → scratch (+ CA certs for AI HTTPS)

# ---- build ----
FROM golang:alpine AS build
WORKDIR /src

# go.mod may require a newer toolchain than the image tag
ENV GOTOOLCHAIN=auto \
    CGO_ENABLED=0

RUN apk add --no-cache ca-certificates git \
 && update-ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Pure-Go SQLite (modernc) → CGO free. Strip symbols for size.
RUN go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/quick-server \
      .

# ---- runtime (scratch = empty rootfs; ~binary size only) ----
FROM scratch

# Outbound TLS (Anthropic / OpenAI proxy). Nothing else from alpine.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/quick-server /quick-server

# Runtime data lives on a volume (not in the image)
ENV QUICK_ENV=production \
    QUICK_PORT=8080 \
    QUICK_DB_PATH=/data/quick.db \
    QUICK_SITES_DIR=/data/sites \
    QUICK_UPLOADS_DIR=/data/uploads \
    QUICK_TRUST_PROXY=0

EXPOSE 8080
# Run as root in scratch so a mounted /data volume is always writable.
# Harden at the orchestrator (read_only rootfs + volume) via compose.
ENTRYPOINT ["/quick-server"]
