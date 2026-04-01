# ── Stage 1: build ──────────────────────────────────────────────────────────
# TARGETOS/TARGETARCH are forwarded to the Go toolchain for cross-compilation.
FROM golang:1.24-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

# Cache dependencies separately from source code
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source and build a fully static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /workspace-portal .

# ── Stage 2: runtime ─────────────────────────────────────────────────────────
FROM scratch

# Copy CA certificates so HTTPS outbound calls (if any) work
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary
COPY --from=builder /workspace-portal /workspace-portal

# Expose the default port
EXPOSE 3000

# Environment variable defaults.
# NOTE: The application has built-in default credentials if PORTAL_USER/PORTAL_PASS
# are not provided. For secure deployments you MUST set PORTAL_USER and PORTAL_PASS
# as environment variables at runtime.
ENV PORTAL_PORT=3000 \
    PORTAL_DIR=/workspace \
    PORTAL_TLS=false

# At runtime, mount a host directory or volume at /workspace, for example:
# docker run -v /host/workspace:/workspace -p 3000:3000 image-name

ENTRYPOINT ["/workspace-portal"]
