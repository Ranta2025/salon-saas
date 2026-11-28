# Contexto de build: ./backend (ver compose). Incluye migraciones para migrate().
FROM golang:1.26 AS build
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . ./
# Dos binarios: la API y el seed de DEMO (cmd/seed). El seed es una herramienta
# de desarrollo que corre como servicio de una sola vez en docker-compose.yml;
# la imagen de la API es la misma, así el seed usa el MISMO código y el mismo
# go.mod que la API (y no hay una segunda imagen que mantener).
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /seed ./cmd/seed

FROM debian:12-slim
# webp: el adaptador de imagenes convierte las fotos con el binario cwebp.
# ca-certificates: para hablar con un S3 por https. curl: healthcheck.
RUN apt-get update \
    && apt-get install -y --no-install-recommends webp ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --no-create-home --uid 10001 salon \
    && mkdir -p /media && chown salon /media
COPY --from=build /api /api
COPY --from=build /seed /seed
COPY --from=build /app/migrations /migrations
WORKDIR /
USER salon
EXPOSE 8080
# El seed NO se ejecuta solo: el CMD sigue siendo la API. En el compose de demo,
# el servicio `seed` lo corre con entrypoint /seed y las fotos de la demo
# montadas en /assets (ver docs/ops/demo-seed.md).
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
  CMD curl -fsS "http://127.0.0.1:${PORT:-8080}/health" || exit 1
CMD ["/api"]
