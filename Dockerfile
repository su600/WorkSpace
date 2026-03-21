# ── Stage 1: build ──────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Cache dependencies separately from source code
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source and build a fully static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /workspace-portal .

# ── Stage 2: runtime ─────────────────────────────────────────────────────────
FROM scratch

# Copy CA certificates so HTTPS outbound calls (if any) work
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary
COPY --from=builder /workspace-portal /workspace-portal

# Expose the default port
EXPOSE 3000

# Environment variable defaults (override PORTAL_USER and PORTAL_PASS at runtime)
ENV PORTAL_PORT=3000 \
    PORTAL_DIR=/workspace \
    PORTAL_TLS=false

# Mount point for the workspace directory
VOLUME ["/workspace"]

ENTRYPOINT ["/workspace-portal"]
