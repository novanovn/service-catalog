# Build stage
FROM golang:1.24-alpine AS builder

# Add git and ca-certificates
RUN apk update && apk add --no-cache git ca-certificates tzdata

# Create a non-root user for security (Avoid running as root in production)
RUN adduser -D -g '' appuser

WORKDIR /app

# Cache dependencies layer
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary statically
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o devportal ./cmd/server

# Final stage (Minimal distroless-like environment)
FROM alpine:3.19

# Copy Trivy from the local aquasec/trivy image instead of downloading it on
# every build. The install.sh path was producing ~488MB BuildKit layers and
# filling the laptop disk.
COPY --from=aquasec/trivy:latest /usr/local/bin/trivy /usr/local/bin/trivy

# Import certificates and user from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Git is required by the worker for terraform/docs clones. Retry apk in case
# the Alpine CDN blips; do not pull Trivy over the network.
RUN for i in 1 2 3 4 5; do \
        apk add --no-cache git && break; \
        if [ "$i" = "5" ]; then exit 1; fi; \
        sleep 4; \
    done

# Create home directory for appuser with full read-write permissions for Trivy cache
RUN mkdir -p /home/appuser/.cache/trivy && chown -R appuser:appuser /home/appuser
ENV HOME=/home/appuser
ENV TRIVY_CACHE_DIR=/home/appuser/.cache/trivy

WORKDIR /app

# Copy the pre-built binary
COPY --from=builder /app/devportal .

# Copy HTML templates
COPY --from=builder /app/internal/templates ./internal/templates

# Copy OpenAPI docs
COPY --from=builder /app/docs ./docs

# Copy TechDocs cache files
COPY --from=builder /app/internal/docs/cache ./internal/docs/cache
RUN chown -R appuser:appuser /app /home/appuser

# Use the non-root user
USER appuser

EXPOSE 8080

ENTRYPOINT ["./devportal"]