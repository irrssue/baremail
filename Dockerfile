# syntax=docker/dockerfile:1
#
# Multi-stage build for baremail: the frontend (Vite/React) and the Go backend
# are each built in their own toolchain image, then copied into a tiny Alpine
# runtime. The homelab only needs Docker — no Go or Node toolchain installed.

# --- Stage 1: build the React frontend -> /app/dist ---
FROM node:22-alpine AS frontend
WORKDIR /app
COPY baremail-app/package.json baremail-app/package-lock.json ./
RUN npm ci
COPY baremail-app/ ./
RUN npm run build

# --- Stage 2: compile the Go backend -> static binary ---
FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
# CGO off so the binary is self-contained; -s -w strips debug info to shrink it.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /baremail .

# --- Stage 3: minimal runtime ---
FROM alpine:3.20
# ca-certificates: TLS to the Google APIs. wget: container healthcheck.
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=backend /baremail /app/baremail
COPY --from=frontend /app/dist /app/dist
# Paths inside the container. SESSIONS_FILE lives on a mounted volume so the
# token store survives image rebuilds and restarts.
ENV STATIC_DIR=/app/dist \
    SESSIONS_FILE=/data/sessions.json \
    PORT=3003
VOLUME /data
EXPOSE 3003
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:3003/auth/status >/dev/null 2>&1 || exit 1
ENTRYPOINT ["/app/baremail"]
