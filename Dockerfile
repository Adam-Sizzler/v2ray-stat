# Multistage build for exodus panel (frontend + backend)
FROM node:24-alpine AS panel-frontend
WORKDIR /frontend
ENV NODE_OPTIONS=--max-old-space-size=4096

ARG SINGBOX_ASSETS_URL=https://adam-sizzler.github.io/s-validator
ARG SINGBOX_SCHEMA_URL=https://sing-box.sagernet.org/schema.json

COPY frontend/package*.json ./
RUN --mount=type=cache,target=/root/.npm npm install --legacy-peer-deps --no-audit --prefer-offline

COPY frontend/ ./

RUN if [ ! -f public/assets/main.wasm ] || [ ! -f public/assets/wasm_exec.js ] || [ ! -f public/assets/singbox.schema.json ]; then \
      apk add --no-cache curl \
      && mkdir -p public/assets \
      && curl -fsSL ${SINGBOX_SCHEMA_URL} -o public/assets/singbox.schema.json \
      && if [ ! -f public/assets/main.wasm ] || [ ! -f public/assets/wasm_exec.js ]; then \
           curl -fsSL ${SINGBOX_ASSETS_URL}/wasm_exec.js -o public/assets/wasm_exec.js \
           && curl -fsSL ${SINGBOX_ASSETS_URL}/main.wasm -o public/assets/main.wasm; \
         fi; \
    else \
      echo "Assets already present locally, skipping download"; \
    fi

ARG BUILD_BUST=1
RUN --mount=type=cache,target=/root/.npm --mount=type=cache,target=/frontend/node_modules/.vite/cache \
    rm -rf dist && npm run cb

FROM golang:1.27.1-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build/backend

# Copy go mod files and download dependencies
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Invalidate cache on build bust or code changes
ARG BUILD_BUST=1
ARG __EX_METADATA_GIT_BACKEND_COMMIT=unknown
ARG __EX_METADATA_VERSION=unknown

# Copy source code
COPY backend/ ./

# Build panel backend binary
RUN CGO_ENABLED=0 \
    GOOS=linux \
    go build \
        -tags "none" \
        -trimpath \
        -buildvcs=false \
        -o /build/exodus \
        .

# Final stage
FROM alpine:latest

LABEL org.opencontainers.image.title="Exodus" \
      org.opencontainers.image.description="Powerful proxy management panel" \
      org.opencontainers.image.url="https://github.com/exodus/backend" \
      org.opencontainers.image.source="https://github.com/exodus/backend" \
      org.opencontainers.image.vendor="Exodus" \
      org.opencontainers.image.licenses="AGPL-3.0" \
      org.opencontainers.image.documentation="https://docs.ex"

ARG BRANCH=main
ARG __EX_METADATA_VERSION=1.1.1
ARG __EX_METADATA_GIT_BACKEND_COMMIT=0f344f388807f5323b49024a563b3f8146d66857
ARG __EX_METADATA_GIT_FRONTEND_COMMIT=0f344f388807f5323b49024a563b3f8146d66857
ARG __EX_METADATA_GIT_BRANCH=dev
ARG __EX_METADATA_BUILD_TIME=2011-11-11T11:11:11Z
ARG __EX_METADATA_BUILD_NUMBER=0

ENV EXODUS_BRANCH=${BRANCH} \
    __EX_METADATA_VERSION=${__EX_METADATA_VERSION} \
    __EX_METADATA_GIT_BACKEND_COMMIT=${__EX_METADATA_GIT_BACKEND_COMMIT} \
    __EX_METADATA_GIT_FRONTEND_COMMIT=${__EX_METADATA_GIT_FRONTEND_COMMIT} \
    __EX_METADATA_GIT_BRANCH=${__EX_METADATA_GIT_BRANCH} \
    __EX_METADATA_BUILD_TIME=${__EX_METADATA_BUILD_TIME} \
    __EX_METADATA_BUILD_NUMBER=${__EX_METADATA_BUILD_NUMBER}

# Install runtime dependencies including curl for healthchecks
RUN apk add --no-cache curl ca-certificates tzdata

WORKDIR /opt/app

# Copy binary from builder
COPY --from=builder /build/exodus /opt/app/

# Expose CLI symlinks for `docker exec -it exodus cli` and `docker exec -it exodus exodus`
RUN ln -s /opt/app/exodus /usr/local/bin/exodus && \
    ln -s /opt/app/exodus /usr/local/bin/cli

# Copy built frontend
COPY --from=panel-frontend /frontend/dist /opt/app/frontend
COPY --from=builder /usr/local/go/lib/wasm/wasm_exec.js /opt/app/frontend/assets/wasm_exec.js

# Create directories for data and certificates
RUN mkdir -p /opt/app/data /opt/app/certs

EXPOSE 3000

ENTRYPOINT ["/opt/app/exodus"]
