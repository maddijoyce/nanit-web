# Frontend build stage
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend-build

WORKDIR /app/frontend

# Copy frontend package files
COPY frontend/package.json frontend/package-lock.json* ./

# Install dependencies (including dev dependencies for build)
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi

# Copy frontend source
COPY frontend/ ./

# Build the frontend
RUN npm run build

# Backend build stage
FROM golang:1.24.0 AS backend-build

# Install build dependencies for SQLite
RUN apt-get update && apt-get install -y \
    gcc libc6-dev sqlite3 libsqlite3-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Dependencies first: source changes below must not invalidate the module cache
ADD go.mod /app/
ADD go.sum /app/
RUN go mod download

ADD cmd /app/cmd
ADD pkg /app/pkg
ADD scripts /app/scripts

# Copy built frontend files to replace old web directory
COPY --from=frontend-build /app/frontend/dist /app/web

ARG CI_COMMIT_SHORT_SHA

# CGO is required for SQLite. This stage runs on the target platform, so the
# build is native and needs no cross-compiler.
RUN CGO_ENABLED=1 go build -ldflags "-X main.GitCommit=$CI_COMMIT_SHORT_SHA" -o ./bin/nanit ./cmd/nanit/*.go

# Final production stage
FROM debian:bookworm-slim

COPY --from=backend-build /app/bin/nanit /app/bin/nanit
COPY --from=backend-build /app/scripts /app/scripts
COPY --from=backend-build /app/web /app/web

RUN apt-get -yqq update && \
    apt-get install -yq --no-install-recommends ca-certificates ffmpeg bash curl jq sqlite3 libsqlite3-0 && \
    apt-get autoremove -y && \
    apt-get clean -y

RUN mkdir -p /data && \
    chmod +x /app/scripts/*.sh

WORKDIR /app
ENTRYPOINT ["/app/bin/nanit"]