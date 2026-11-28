#!/usr/bin/env bash
# Carga los datos de DEMO de Salón SaaS (envoltorio compatible del seed).
#
# La lógica vive en backend/cmd/seed: este script solo elige cómo ejecutarlo.
# Es idempotente: si la demo ya está sembrada no escribe nada.
#
# Uso:
#   bash scripts/seed-demo.sh                 # contra el stack de Docker (api:8080)
#   API=http://localhost:8090 bash scripts/seed-demo.sh
#   SEED_RUNNER=local DATABASE_URL=postgres://... bash scripts/seed-demo.sh
#
# Variables:
#   API            base de la API en el host (default http://localhost:8080)
#   SEED_RUNNER    docker (default) | local   (local = go run, sin Docker Compose)
#   DATABASE_URL   obligatoria con SEED_RUNNER=local
#   SEED_FORCE=1   permite una base que no es local (¡peligro!)
#
# Entrada al panel: dueno@salon.local / SalonDemo2026!  (ver docs/ops/demo-seed.md)
set -euo pipefail

cd "$(dirname "$0")/.."

API="${API:-http://localhost:8080}"
RUNNER="${SEED_RUNNER:-docker}"

die() { printf '\nERROR: %s\n' "$*" >&2; exit 1; }

case "$RUNNER" in
docker)
  command -v docker >/dev/null 2>&1 || die "no encuentro docker (o usá SEED_RUNNER=local)"
  docker compose version >/dev/null 2>&1 || die "falta Docker Compose v2 (docker compose)"
  [ -n "${JWT_SECRET:-}" ] || [ -f .env ] || die "falta .env con JWT_SECRET: ver docs/ops/environment.md"
  echo "==> seed de demo por Docker Compose (API interna: api:8080)"
  docker compose up -d --build postgres api
  # --rm: el seed es de una sola vez. La API tiene que estar sana (depends_on).
  docker compose run --rm seed
  ;;
local)
  command -v go >/dev/null 2>&1 || die "no encuentro go"
  [ -n "${DATABASE_URL:-}" ] || die "SEED_RUNNER=local necesita DATABASE_URL"
  echo "==> seed de demo con go run (API: $API)"
  ( cd backend && SEED_DEMO=true SEED_API_URL="$API" go run ./cmd/seed )
  ;;
*)
  die "SEED_RUNNER='$RUNNER' no es válido (docker o local)"
  ;;
esac

echo
echo "Listo. Panel:    $API/login"
echo "        Landing:  ${WEB_URL:-http://localhost:8081}/"
echo "        Dueña:    dueno@salon.local / SalonDemo2026!"
echo "Los otros logins (administración, recepción y profesionales) están en docs/ops/demo-seed.md."
