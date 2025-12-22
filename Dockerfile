# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o dploy-api ./cmd/api

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/dploy-api .

# Copy config directory
COPY --from=builder /app/config ./config

# Copy web files (frontend)
COPY --from=builder /app/web ./web

# Create non-root user
RUN adduser -D -u 1000 dploy && \
    chown -R dploy:dploy /app

USER dploy

EXPOSE 8080

CMD ["./dploy-api"]
