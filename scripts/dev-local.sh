#!/usr/bin/env bash
# Levanta la API de salon-saas en tu maquina para probar (la API corre en el host;
# solo Postgres corre en Docker). Uso, desde cualquier carpeta:
#   bash scripts/dev-local.sh
# Variables opcionales: SALON_DB (base, por defecto salon_local), PORT (por defecto 8080).
set -euo pipefail
cd "$(dirname "$0")/.."
DB=${SALON_DB:-salon_local}
PORT=${PORT:-8080}
PG=salon-saas-postgres-1
say() { printf '\n==> %s\n' "$*"; }
die() { printf '\nERROR: %s\n' "$*" >&2; exit 1; }

say "1/5 Docker"
docker info >/dev/null 2>&1 || die "El demonio de Docker no esta corriendo. Ejecuta: sudo systemctl start docker"

say "2/5 Postgres (contenedor $PG)"
if ! docker ps --format '{{.Names}}' | grep -qx "$PG"; then
  docker start "$PG" >/dev/null 2>&1 || die "No existe el contenedor $PG. Crealo con: docker compose up -d postgres"
fi
for _ in $(seq 1 30); do docker exec "$PG" pg_isready -U salon >/dev/null 2>&1 && break; sleep 1; done
docker exec "$PG" pg_isready -U salon >/dev/null 2>&1 || die "Postgres no responde despues de 30 s. Mira: docker logs $PG"

say "3/5 Base de datos '$DB'"
if ! docker exec "$PG" psql -U salon -d postgres -Atc "select 1 from pg_database where datname='$DB'" | grep -q 1; then
  docker exec "$PG" psql -U salon -d postgres -qc "CREATE DATABASE \"$DB\""
  echo "creada"
else
  echo "ya existe"
fi

say "4/5 Puerto $PORT"
if ss -ltn | grep -qE ":$PORT\b"; then
  die "El puerto $PORT esta ocupado (otra API corriendo?). Cerrala o usa: PORT=8090 bash scripts/dev-local.sh"
fi

say "5/5 Arranque"
export DATABASE_URL="postgres://salon:salon@localhost:5432/$DB?sslmode=disable"
export JWT_SECRET="${JWT_SECRET:-$(openssl rand -hex 32)}"
export MEDIA_DIR="${MEDIA_DIR:-$HOME/.local/share/salon-saas-media}"
export APP_URL="${APP_URL:-http://localhost:5173}"
export PORT
mkdir -p "$MEDIA_DIR"
echo "API en http://localhost:$PORT  (Ctrl+C para parar). Aplica las migraciones sola al arrancar."
cd backend
exec go run ./cmd/api
