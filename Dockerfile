ARG VERSION=2.0.0
ARG BUILD_TIME
ARG SOURCE_REVISION=unknown

# ---- Frontend Build ----
FROM node:22-alpine AS frontend
WORKDIR /app/console
COPY console/package.json console/package-lock.json ./
RUN npm ci
COPY console/ ./
RUN npm run build

# ---- Backend Build ----
FROM golang:1.26.6-alpine AS backend
ARG VERSION
ARG BUILD_TIME
ARG SOURCE_REVISION
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/console/dist ./console/dist
RUN build_time="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
      -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildTime=${build_time} -X github.com/mirainya/Prism/internal/gateway/adapter.BuildRevision=${SOURCE_REVISION}" \
      -o prism ./cmd/server

# ---- Runtime ----
FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S prism \
    && adduser -S -D -H -G prism prism
WORKDIR /app
COPY --from=backend --chown=prism:prism /app/prism .
COPY --chown=prism:prism configs/config.docker.yaml configs/config.yaml
USER prism
EXPOSE 23523
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:23523/health || exit 1
CMD ["./prism"]
