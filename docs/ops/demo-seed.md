# Datos de demo

`backend/cmd/seed` carga un salón completo para que **todas** las pantallas del
panel y la landing pública se vean con datos reales. No es una migración: es un
programa de **una sola corrida** que corre como servicio `seed` del
`docker-compose.yml`, espera a que la API esté sana y termina.

> **Es solo para desarrollo y demo.** Los logins, los teléfonos y las clientas
> son ficticios y la clave es pública. No lo corras contra una base con datos de
> verdad.

## Cómo correrlo

```bash
make demo     # levanta el stack y el servicio `seed` carga la demo solo
make seed     # stack ya levantado: corre el seed otra vez (no duplica nada)
make seed-local   # sin Docker: go run ./cmd/seed contra la API del host
bash scripts/seed-demo.sh   # el envoltorio de siempre (elige docker o local)
```

Con Docker, el seed entra por la red del compose (`SEED_API_URL=http://api:8080`)
y usa la misma base que la API (`DATABASE_URL` con las piezas `POSTGRES_*`). El
progreso se ve con `make logs` (servicio `seed`) y **sin** `-f`:

```bash
docker compose logs seed        # el resumen del seed y las filas por tabla
docker compose ps seed          # Exit 0 = terminó bien
```

Sin Docker (API y Postgres en el host):

```bash
export DATABASE_URL='postgres://salon:salon@localhost:5432/salon?sslmode=disable'
SEED_DEMO=true SEED_API_URL=http://localhost:8080 \
  SEED_ASSETS_DIR=scripts/demo-assets \
  go run ./backend/cmd/seed          # desde la raíz del repo
```

## Los tres seguros

| Seguro | Qué hace | Cómo se pisa |
|---|---|---|
| `SEED_DEMO=true` | sin esto el binario no hace **nada** | — (a propósito) |
| host de `DATABASE_URL` | solo `localhost`, `127.0.0.1`, `::1`, `postgres` o `host.docker.internal` | `SEED_FORCE=1` |
| idempotencia | si la demo ya está sembrada (marca: las reseñas del seed) **no escribe nada** | — |

El compose deja `SEED_DEMO` en `true` por defecto, así el servicio `seed` no
molesta: es un one-shot con `restart: "no"`.

## Logins

Todos con la clave **`SalonDemo2026!`** y **sin 2FA**:

| Rol | Email | Atiende | Qué se ve |
|---|---|---|---|
| dueña | `dueno@salon.local` | sí | todo el panel, contratos de dinero y equipo |
| administración | `admin@salon.local` | no | todo el panel |
| recepción | `recepcion@salon.local` | no | agenda, clientas, POS, señas, reseñas, lista de espera |
| profesional | `camila@salon.local` | sí | Mi agenda y Mis comisiones |
| profesional | `lucia@salon.local` | sí | idem |
| profesional | `marcos@salon.local` | sí | idem |
| profesional | `sofia@salon.local` | sí | idem |

El salón es **Salón Aurelia** (`/salon/aurelia`), con perfil, dirección, teléfono,
WhatsApp, Instagram, galería de seis fotos, horario con pausa al mediodía, doce
servicios, tres paquetes, fidelidad, seña online y reseñas.

## Qué crea (corrida limpia)

| Qué | Cantidad |
|---|---|
| cuentas del equipo / miembros | 7 / 7 |
| servicios / paquetes / productos | 12 / 3 / 12 |
| puestos y equipamiento | 3 tipos, 6 unidades |
| clientas / con foto | 40 / 2 |
| turnos | ~173 (≈148 de los últimos 60 días + 25 de hoy y los próximos 14) |
| ventas (con productos a veces) | ~132 |
| cierres de caja | 12 (los últimos 14 días hábiles; hoy queda abierto) |
| gastos (60 días) | 25 |
| liquidaciones | 2 (una pagada del mes pasado y un período abierto con ajuste) |
| paquetes vendidos / movimientos | 3 / 4 |
| vales de fidelidad | 3 |
| lista de espera | 5 |
| señas | 13 (pagadas, pendientes, vencidas, devueltas y perdidas) |
| reseñas | 15 (una oculta, promedio 4,6) |
| avisos | 8 del seed + los que genera la API |

Las fechas son **relativas a hoy** en `America/Argentina/Buenos_Aires`: el
historial siempre queda en los últimos 60 días y la agenda en los próximos 14.
El dataset es determinista (`SEED_RNG_SEED`, por defecto `20260101`): la misma
semilla da los mismos turnos, ventas y comentarios.

## Cómo funciona

- **Por la API real** todo lo que la API sabe hacer: cuentas, salón, perfil,
  ajustes, horarios, ausencias, servicios, equipamiento, equipo y modelos de
  pago, clientas y fotos, productos y movimientos, paquetes, fidelidad, política
  de señas, reseñas (su configuración), gastos, liquidaciones del mes pasado,
  ventas de paquetes y reservas futuras (que crean la seña en el banco simulado).
  Así se ejercitan validaciones, hash de claves y auditoría de verdad.
- **Por SQL**, con ids deterministas y `ON CONFLICT DO NOTHING`, solo lo que la
  API no puede crear con fecha pasada: turnos atendidos/cancelados de los
  últimos 60 días, ventas y sus líneas, movimientos de stock, cierres de caja,
  señas vencidas/devueltas/perdidas, reseñas, avisos y el período abierto de
  nómina (la API rechaza liquidar un período sin terminar; las líneas salen de
  su propia vista previa).
- Las fotos se suben por la API (`multipart`) y el adaptador las normaliza a
  WebP. El seed **no descarga nada**: lee `scripts/demo-assets` (autor, licencia
  y origen en `scripts/demo-assets/ATTRIBUTION.md`).
- Respeta el límite por IP de la API (20 pedidos por minuto en `/auth/` y en las
  escrituras públicas): espacia las escrituras públicas y reintenta el `429`.

## Resetear

El seed no borra nada: solo agrega. Para volver a empezar de cero, borrá el
volumen de Postgres y levantá el stack otra vez:

```bash
docker compose down -v        # ¡borra la base y los medios del stack de demo!
docker compose up -d --build  # el servicio `seed` vuelve a sembrar
```

Para "refrescar" solo las fechas (que la agenda vuelva a estar poblada), lo
mismo: `down -v` y de nuevo. Si la demo ya está sembrada, una segunda corrida no
escribe **nada** (lo imprime en el resumen: `(sin cambios: la segunda corrida no
escribió nada)`).

## Problemas conocidos

- **El puerto 8080 ocupado.** Si otro proyecto ya usa `8080`, el `api` no
  arranca y el `seed` espera para siempre. Cambiá los puertos en `.env`:

  ```dotenv
  API_PORT=8090
  VITE_API_URL=http://localhost:8090
  ```

  `VITE_API_URL` se hornea en el build del frontend: hay que reconstruir
  (`make demo`) después de cambiarlo. Con la API en otro puerto, el seed del
  compose no cambia (habla con `api:8080` dentro de la red).
- **La agenda de "hoy" en el panel** usa el día UTC (es el criterio de la API del
  dashboard), así que entre las 21:00 y las 24:00 de Buenos Aires el panel de
  inicio cuenta el día siguiente. El calendario siempre abre con los turnos de
  hoy tal cual.
- **Mailpit**: si el stack no tiene Mailpit, el seed igual corre; los correos de
  confirmación simplemente no salen (la API loguea "email deshabilitado").
- **Fotos**: el servicio `seed` monta `./scripts/demo-assets` en `/assets`
  (read-only). El contexto de build de la imagen es `./backend`, así que las
  fotos no se hornean en la imagen: se montan.
