# Variables de entorno

Docker Compose lee un archivo de variables llamado `.env`, en la raíz del repo.
Ese archivo **lo creás vos** (está ignorado por git y puede contener secretos):
copiá el bloque de abajo, pegalo en un archivo nuevo con ese nombre y cambiá
`JWT_SECRET`.

En fish:

```fish
echo "JWT_SECRET="(openssl rand -base64 48) > .env
```

En bash:

```bash
echo "JWT_SECRET=$(openssl rand -base64 48)" > .env
```

Eso crea el archivo con un secreto real. Después agregá, si querés cambiar algo,
las líneas del bloque siguiente (todas son opcionales salvo `JWT_SECRET`).

## Bloque de referencia (solo placeholders)

```dotenv
# Obligatoria. Mínimo 32 caracteres cuando GIN_MODE=release.
JWT_SECRET=change-me-with-at-least-32-random-characters

# API
GIN_MODE=release
PORT=8080
# Orígenes permitidos por CORS y por el websocket, separados por comas.
CORS_ORIGINS=http://localhost:8081,http://localhost:5173,http://localhost:4173
MEDIA_DIR=/data/media

# Frontend (build arg): origen público de la API tal como lo ve el navegador,
# SIN el sufijo /api/v1 (el frontend ya agrega /api/v1 a cada ruta).
VITE_API_URL=http://localhost:8080

# Puertos del host
API_PORT=8080
WEB_PORT=8081
POSTGRES_PORT=5432
REDIS_PORT=6379
MAILPIT_PORT=8025
MAILPIT_SMTP_PORT=1025

# Base de datos
POSTGRES_IMAGE=postgres:16
POSTGRES_USER=salon
POSTGRES_PASSWORD=salon
POSTGRES_DB=salon

# Otros servicios
REDIS_IMAGE=redis:7
MAILPIT_IMAGE=axllent/mailpit:latest

# Opcional: perfil "files" (ver docs/ops/minio.md)
MINIO_IMAGE=pgsty/minio:latest
MINIO_ROOT_USER=salon
MINIO_ROOT_PASSWORD=change-me-minio-secret
MINIO_PORT=9000
MINIO_CONSOLE_PORT=9001
```

Compose arma `DATABASE_URL` solo con las piezas `POSTGRES_*`. Si corrés la API
sin Docker, exportá `DATABASE_URL` y `JWT_SECRET` en tu shell (en fish:
`set -x JWT_SECRET ...`; en bash: `export JWT_SECRET=...`).
