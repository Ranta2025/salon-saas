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

# Base pública del frontend: con ella se arma el enlace de la invitación de
# equipo ("<APP_URL>/invitacion#<token>"). Tiene que ser una URL http(s)
# absoluta. En producción, el dominio real del panel.
APP_URL=http://localhost:8081

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

# Cada cuánto la API barre la lista de espera (duración de Go: 60s, 2s...).
# 0 la DESHABILITA: no arranca la tarea y la lista queda solo como registro, sin
# avisos por email.
WAITLIST_INTERVAL=60s

# Cada cuánto la API pide las reseñas de los turnos atendidos (F13). Duración de
# Go: 300s por defecto. 0 la DESHABILITA; un valor negativo o ilegible impide
# arrancar.
REVIEW_INTERVAL=300s

# De qué peers se acepta X-Forwarded-For (IP del cliente para el rate limit y
# el log). Vacío = no se confía en nadie: la IP es el peer directo.
# "private" = loopback + rangos privados (comportamiento de la fase 5).
# Detrás de un reverse proxy hay que setearlo (IP del proxy o CIDR de su red).
TRUSTED_PROXIES=

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

## Enlaces de invitación (`APP_URL`)

Cuando alguien invita a una persona al equipo (Fase 6, F2g1), la API manda un
correo con un enlace del tipo:

```
<APP_URL>/invitacion#<token>
```

| Variable | Default en Compose | Qué hace |
|---|---|---|
| `APP_URL` | `http://localhost:8081` | Base pública del frontend. Debe ser una URL `http(s)` absoluta con host; un valor inválido **impide arrancar** y el error nombra la variable. La barra final se quita sola. |

El token viaja en el **fragmento** (`#`), no en la ruta: el navegador no lo manda
al servidor, así que no aparece en ningún log de la API ni de nginx. Se muestra
una sola vez (en la respuesta del alta) y en la base queda solo su SHA-256.
Con `APP_URL` apuntando a `localhost` el enlace solo sirve en tu máquina: en
producción poné el dominio real del panel.

## IP del cliente y proxies confiables (`TRUSTED_PROXIES`)

El rate limit por IP y el log de acceso usan la IP del peer TCP. `X-Forwarded-For`
la manda el cliente, así que solo se mira si el peer está en la lista de proxies
confiables.

| Valor de `TRUSTED_PROXIES` | Qué hace |
|---|---|
| vacío (default, también en Compose) | No se confía en ningún proxy: la IP del cliente es el peer directo y `X-Forwarded-For` se ignora. Es lo correcto cuando la API escucha directo (el `API_PORT` publicado del compose). |
| `private` | Loopback + rangos privados (`127.0.0.1`, `::1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`): el comportamiento de la fase 5. |
| IPs/CIDR separados por comas | Solo esos peers son proxies de confianza (`172.18.0.0/16,203.0.113.7`). |

**Detrás de un reverse proxy (nginx, Caddy, Traefik, un balanceador) hay que
setearla**: si no, todas las personas llegan con la IP del proxy y comparten el
mismo cubo del rate limit (20 intentos por minuto para todo el mundo). Poné la IP
del proxy o el CIDR de su red:

```dotenv
# nginx en otra máquina
TRUSTED_PROXIES=203.0.113.7
# nginx/contenedor en la red de Docker
TRUSTED_PROXIES=172.18.0.0/16
```

Un valor que no sea una IP, un CIDR ni `private` **impide arrancar**, con un error
que nombra la variable. Al iniciar, la API loguea la lista con la que quedó (o
"trusted proxies: ninguno").


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
| `WAITLIST_INTERVAL` | `60s` | Cada cuánto se barre la lista de espera (F7) y se avisa a quien esperaba un turno de un día que se liberó. Duración de Go (`60s`, `2s` para probar). **`0` deshabilita la tarea**; un valor negativo o ilegible impide arrancar. |
| `REVIEW_INTERVAL` | `300s` | Cada cuánto se piden las reseñas posvisita (F13, D3): turnos `done` de salones con las reseñas activas, con correo, terminados entre la demora configurada (1..72 h) y 14 días atrás. Duración de Go (`5m`, `2s` para probar). **`0` deshabilita la tarea**; un valor negativo o ilegible impide arrancar. Con `SMTP_HOST` vacío la tarea no pide nada (no se puede mandar el enlace). |

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

## Seña online y banco simulado (`PAYMENT_SIM_SECRET`, `PAYMENT_CALLBACK_URL`, `DEPOSIT_INTERVAL`)

La seña (F11) se cobra a través del puerto `ports.PaymentGateway`; el adaptador de
hoy es un **banco simulado** (paquete `internal/adapters/simbank`, montado en el
mismo binario bajo `/api/v1/simbank/`). No se cobra dinero real: la página
`/pagar` lo avisa con un cartel permanente.

| Variable | Default | Qué hace |
|---|---|---|
| `PAYMENT_SIM_SECRET` | vacío (sin Docker) / un valor de DESARROLLO en Compose | Secreto HMAC del banco simulado. **Vacío = `NoopGateway`**: no se pueden activar señas, las rutas del banco y el callback responden **404** y el barrido no corre. En release exige al menos 32 caracteres (igual que `JWT_SECRET`). El valor del compose es SOLO para desarrollo: **no sirve para producción**. |
| `PAYMENT_CALLBACK_URL` | `http://127.0.0.1:<PORT>/api/v1/payments/callback` (en Compose: `http://api:8080/api/v1/payments/callback`) | A dónde llama el banco simulado para avisar el resultado del pago. Es una URL absoluta http(s); timeout de 5 s y 3 intentos con espera corta. |
| `DEPOSIT_INTERVAL` | `60s` | Cada cuánto barre las señas pendientes vencidas (cancela la reserva) y las pagadas de reservas canceladas (reembolsa o pierde la seña según la ventana). Duración de Go. **`0` deshabilita la tarea**; un valor negativo o ilegible impide arrancar. |

Rotar `PAYMENT_SIM_SECRET` invalida los callbacks en vuelo (el comercio rechaza
la firma y el banco reintenta): el cambio se hace con el barrido detenido y sin
pagos a medias. El secreto no se loguea nunca, y el número de tarjeta tampoco:
la base solo guarda `last4` y la marca.

Los tres valores viajan en `docker-compose.yml` (servicio `api`). Para probar el
circuito completo en local: activá la seña en `/panel/senas`, reservá desde la
landing, pagá en `/pagar` con `4242 4242 4242 4242` y mirá el correo de
confirmación en Mailpit. Con `DEPOSIT_INTERVAL=1s` se ve el barrido en acción.

## Reseñas posvisita (`REVIEW_INTERVAL`)

F13: cada turno **atendido** (`done`) de una clienta **con correo** puede pedir una
reseña, una sola vez. El módulo (`internal/reviews`) NO toca la agenda, el POS, la
caja ni la nómina: lee `bookings` por su propio adaptador de solo lectura.

| Variable | Default en Compose | Qué hace |
|---|---|---|
| `REVIEW_INTERVAL` | `300s` (5 min) | Cada cuánto barre los turnos atendidos que ya pueden pedir reseña. Duración de Go (`5m`, `2s` para probar). **`0` deshabilita la tarea**; un valor negativo o ilegible impide arrancar. |

El salón decide **si** pide reseñas y **cuándo** en el panel «Reseñas»
(`PUT /api/v1/salons/:id/review-settings`, `enabled` + `delay_hours` de 1 a 72).
Sin fila guardada las reseñas están **apagadas** (`enabled:false`,
`delay_hours:24`). Con la tarea apagada o con `SMTP_HOST` vacío no se crea ningún
pedido: no se pide una reseña que nunca va a llegar.

Cómo funciona el envío (**al menos una vez**, con arriendo de 15 minutos):

- El pedido es UNO por turno (`review_requests.booking_id` es UNIQUE), así que dos
  barredores (dos réplicas, o un tick que se solapa) no crean dos pedidos.
- El envío se toma como un **ARRIENDO**: `sent_at` es el último reclamo (no la
  entrega) y `delivered_at` se sella DESPUÉS de que el correo salió bien. Solo un
  proceso puede tener el arriendo a la vez y solo un pedido **entregado** sale de
  la cola para siempre.
- Si el envío falla, el arriendo se **deshace** (`sent_at` vuelve a NULL) y el
  próximo tick lo reintenta. Si el proceso **muere** entre el reclamo y el envío,
  el arriendo vence a los **15 minutos** y otro tick lo reintenta: así ningún turno
  se queda sin enlace por un proceso que se cayó.
- El precio de esa resiliencia: un correo puede salir **dos veces** (por ejemplo,
  si el proceso murió justo después de entregarlo al SMTP y antes de sellar la
  entrega). Cada correo lleva un token **distinto**, los dos enlaces sirven y
  sigue habiendo **UNA sola reseña por turno** (`reviews.booking_id` es UNIQUE).
- El enlace es un token de 32 bytes aleatorios; la base guarda **solo su
  SHA-256**. Vence a los **30 días** y sirve **una sola vez** (el canje es un
  `UPDATE` condicional en la misma transacción que crea la reseña).
- Configuración del correo: el mismo `SMTP_*` de la confirmación de reserva. Abrí
  Mailpit (`http://localhost:8025`) para ver el correo; el enlace es
  `<APP_URL>/resena#<token>` (el token va en el fragmento, nunca en el query).
