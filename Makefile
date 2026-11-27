# Salón SaaS — Makefile de atajos.
#
# Docker: `cp .env.example .env`, poner un JWT_SECRET y `make up`.
# Sin Docker: levantar postgres/redis, exportar DATABASE_URL y `make api`.
# Todos los targets corren desde la raíz del repo.

.PHONY: up down logs dev demo seed api test test-it lint web-build

# ─── Docker Compose ──────────────────────────────────────────────────────────
up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f --tail=100

# Alias histórico: mismo stack completo en segundo plano.
dev: up

# Todo junto para probar: reconstruye las imagenes (para no ver una version vieja),
# levanta el stack y carga un salon de demo. Web en http://localhost:8081
demo:
	docker compose up -d --build
	@echo "Esperando a la API..."
	@for i in $$(seq 1 60); do curl -fsS http://localhost:8080/health >/dev/null 2>&1 && break; sleep 2; done
	bash scripts/seed-demo.sh

# Solo cargar (o recargar) los datos de demo en una API que ya corre.
seed:
	bash scripts/seed-demo.sh

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
	cd backend && SALON_IT=1 go test -count=1 ./internal/adapters/postgres/ ./internal/loyalty/postgres/

lint:
	cd backend && go vet ./...

# ─── Frontend ────────────────────────────────────────────────────────────────
web-build:
	cd frontend && npm run build
