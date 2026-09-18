# --- Build stage ---
FROM golang:1.22-alpine AS build
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server

# --- Runtime stage ---
FROM alpine:3.20
RUN adduser -D -u 10001 appuser
WORKDIR /app

COPY --from=build /out/server ./server
COPY migrations ./migrations
COPY web ./web

ENV PORT=8080
ENV STATIC_DIR=/app/web/static
ENV MIGRATIONS_FILE=/app/migrations/001_init.sql

USER appuser
EXPOSE 8080

ENTRYPOINT ["./server"]
