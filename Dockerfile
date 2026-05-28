# syntax=docker/dockerfile:1

# Frontend build stage — runs on the build host (output is arch-independent).
FROM --platform=$BUILDPLATFORM node:20-alpine AS frontend
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Go build stage — runs on the build host and cross-compiles for the target
# arch (no QEMU emulation).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w" -o dploy-api ./cmd/api

# Runtime stage — per-target image, COPY only (nothing executes here at build).
FROM scratch
WORKDIR /app
# CA certificates for HTTPS (arch-independent data)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/dploy-api .
COPY --from=builder /app/config ./config
COPY --from=frontend /app/web/dist ./web/dist
# Run as non-root user (numeric UID since no passwd file in scratch)
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/app/dploy-api"]
