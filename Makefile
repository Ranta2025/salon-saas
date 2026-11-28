# Salón SaaS — Makefile de atajos.
#
# Docker: `cp .env.example .env`, poner un JWT_SECRET y `make up`.
# Sin Docker: levantar postgres/redis, exportar DATABASE_URL y `make api`.
# Todos los targets corren desde la raíz del repo.

.PHONY: up down logs dev demo seed seed-reset api test test-it lint web-build

# ─── Docker Compose ──────────────────────────────────────────────────────────
up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f --tail=100

# Alias histórico: mismo stack completo en segundo plano.
dev: up

# Todo junto para probar: reconstruye las imagenes y levanta el stack. El
# servicio `seed` espera a que la API este sana y carga la demo solo (es
# idempotente: si ya esta sembrada no escribe nada). Web en http://localhost:8081
demo:
	docker compose up -d --build
	@echo "La demo se carga sola: seguí el progreso con  make logs  (servicio seed)."

# Solo cargar (o recargar) los datos de demo sobre un stack que ya corre.
# --rm: el contenedor es de una sola vez. Con el stack levantado, el seed entra
# por la red del compose (SEED_API_URL=http://api:8080).
seed:
	docker compose up -d --build postgres api
	docker compose run --rm seed

# Igual que `seed` pero por la API del host, sin Docker (necesita Go y
# DATABASE_URL exportada: ver docs/ops/demo-seed.md).
seed-local:
	cd backend && SEED_DEMO=true go run ./cmd/seed

# ─── Desarrollo sin Docker ───────────────────────────────────────────────────
# Necesita DATABASE_URL (por defecto postgres local) y JWT_SECRET exportados.
api:
	cd backend && go run ./cmd/api

# ─── Tests y linters ─────────────────────────────────────────────────────────
test:
	cd backend && go test -race -count=1 ./...

# Integración real contra Postgres: requiere DATABASE_URL a una base con las
# migraciones aplicadas y SALON_IT=1 (ver README).
test-it:
	cd backend && SALON_IT=1 go test -count=1 ./internal/adapters/postgres/ ./internal/bookings/postgres/ ./internal/deposits/postgres/ ./internal/auth/postgres/ ./internal/audit/postgres/ ./internal/packages/postgres/ ./internal/clients/postgres/ ./internal/payroll/postgres/ ./internal/pos/postgres/ ./internal/settings/postgres/ ./internal/staffing/postgres/ ./internal/schedules/postgres/ ./internal/products/postgres/ ./internal/simbank/ ./internal/loyalty/postgres/ ./internal/exports/postgres/ ./internal/notifications/postgres/ ./internal/finance/postgres/ ./internal/waitlist/postgres/ ./internal/team/postgres/ ./internal/reviews/postgres/

lint:
	cd backend && go vet ./...

# ─── Frontend ────────────────────────────────────────────────────────────────
web-build:
	cd frontend && npm run build
