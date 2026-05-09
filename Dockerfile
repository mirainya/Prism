# ---- Frontend Build ----
FROM node:18-alpine AS frontend
WORKDIR /app/console
COPY console/package.json console/package-lock.json* ./
RUN npm install
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
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=backend /app/prism .
COPY configs/config.docker.yaml configs/config.yaml
EXPOSE 23523
CMD ["./prism"]
