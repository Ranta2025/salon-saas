# Contexto de build: ./backend (ver compose). Incluye migraciones para migrate().
FROM golang:1.22 AS build
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . ./
RUN go build -o /api ./cmd/api

FROM debian:12-slim
COPY --from=build /api /api
COPY --from=build /app/migrations /migrations
WORKDIR /
EXPOSE 8080
CMD ["/api"]
