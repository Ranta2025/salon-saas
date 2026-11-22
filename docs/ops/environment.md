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

# Fotos en S3/MinIO (opcional). Vacío = se guardan en el volumen local.
# Con el perfil "files" de Compose: S3_ENDPOINT=http://minio:9000
# Con el MinIO suelto en tu máquina, desde Docker: http://host.docker.internal:9000
# Sin Docker (go run): http://127.0.0.1:9000
S3_ENDPOINT=
S3_ACCESS_KEY=salon
S3_SECRET_KEY=change-me-minio-secret
S3_BUCKET=salon-media
S3_USE_SSL=false

# Email a clientes: confirmación de la reserva pública y recordatorio 24 h antes.
# Con Docker Compose los defaults ya apuntan al Mailpit del stack (UI en
# http://localhost:8025). SMTP_HOST vacío = email deshabilitado (la API arranca
# igual y loguea "email deshabilitado"; no se manda ni se reclama nada).
SMTP_HOST=mailpit
SMTP_PORT=1025
SMTP_FROM=turnos@salon.local
# Solo si tu servidor pide autenticación.
SMTP_USER=
SMTP_PASS=
# none | starttls | tls (tls = TLS implícito, puerto 465 típico).
SMTP_TLS=none
# Cada cuánto la API mira la cola de recordatorios (duración de Go: 60s, 2s...).
REMINDER_INTERVAL=60s

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

## Email a clientes (`SMTP_*` y `REMINDER_INTERVAL`)

La API manda dos correos a la persona que reserva desde la landing: la
**confirmación** apenas entra el turno y un **recordatorio** unas 24 h antes.
Todo sale por SMTP detrás del puerto `ports.MailSender`; hoy no hay WhatsApp ni
push.

| Variable | Default en Compose | Qué hace |
|---|---|---|
| `SMTP_HOST` | `mailpit` (en la API sin Docker: vacío) | Servidor SMTP. **Vacío = email deshabilitado**: no se manda nada y la tarea de recordatorios no reclama pendientes. |
| `SMTP_PORT` | `1025` | Puerto SMTP (25 si no se define). |
| `SMTP_FROM` | `turnos@salon.local` | Remitente de todos los correos. |
| `SMTP_USER` / `SMTP_PASS` | vacíos | Autenticación SMTP (PLAIN). Sin usuario no se autentica. |
| `SMTP_TLS` | `none` | `none` (plano, Mailpit/relay interno), `starttls` (587) o `tls` (TLS implícito, 465). Un valor desconocido impide arrancar. |
| `REMINDER_INTERVAL` | `60s` | Cada cuánto se mira la cola de recordatorios. Es una duración de Go: `60s`, `2s` para probar. Tiene que ser > 0. |

Abrí `http://localhost:8025` (Mailpit) para ver lo que se envió. Con la API
corriendo fuera de Docker, el Mailpit del stack escucha en `127.0.0.1:1025`
(si el contenedor está levantado con el mapeo del compose).

Los correos salen en español, con el nombre del salón, el nombre de la persona
y la fecha y hora en la zona del salón (con el nombre de la zona, por ejemplo
`01/12 09:00 (America/Mexico_City)`). No llevan ids, tokens ni imágenes remotas.
La confirmación viaja en segundo plano: un SMTP lento o caído no demora ni
tumba la reserva. Un recordatorio que no se pudo enviar se reintenta en ticks
posteriores (hasta 3 intentos; después de 5 minutos de un reclamo sin
respuesta vuelve a estar disponible).
