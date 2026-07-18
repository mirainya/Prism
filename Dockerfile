# ---- Frontend Build ----
FROM node:22-alpine AS frontend
WORKDIR /app/console
COPY console/package.json console/package-lock.json* ./
RUN npm ci
COPY console/ ./
RUN npm run build

# ---- Backend Build ----
FROM golang:1.25-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/console/dist ./console/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o prism ./cmd/server

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
