# Frontend build stage
FROM node:25-alpine AS frontend

WORKDIR /app/web

# Copy package files
COPY web/package*.json ./

# Install dependencies
RUN npm ci

# Copy source code
COPY web/ ./

# Build React app
RUN npm run build

# Go build stage
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
FROM scratch

WORKDIR /app

# Copy CA certificates for HTTPS
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy binary from builder
COPY --from=builder /app/dploy-api .

# Copy config directory
COPY --from=builder /app/config ./config

# Copy built frontend from frontend stage
COPY --from=frontend /app/web/dist ./web/dist

# Run as non-root user (numeric UID since no passwd file in scratch)
USER 1000:1000

EXPOSE 8080

ENTRYPOINT ["/app/dploy-api"]
