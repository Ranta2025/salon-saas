# Contexto de build: ./backend (ver compose). Incluye migraciones para migrate().
FROM golang:1.26 AS build
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /api ./cmd/api

FROM debian:12-slim
# webp: el adaptador de imagenes convierte las fotos con el binario cwebp.
# ca-certificates: para hablar con un S3 por https. curl: healthcheck.
RUN apt-get update \
    && apt-get install -y --no-install-recommends webp ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --no-create-home --uid 10001 salon \
    && mkdir -p /media && chown salon /media
COPY --from=build /api /api
COPY --from=build /app/migrations /migrations
WORKDIR /
USER salon
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8080/health || exit 1
CMD ["/api"]
