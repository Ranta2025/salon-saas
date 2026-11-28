# MinIO — instalación y operación local

## Estado actual (honesto)

La aplicación **todavía no usa MinIO**: las fotos de clientes se guardan en el
volumen local `media` (`MEDIA_DIR`) a través del puerto
`ports.PhotoStorage`, implementado por `adapters/storage.Local`. MinIO queda
como servicio opcional del Compose (`--profile files`) listo para el día que se
escriba el adaptador S3. Cambiar de almacenamiento no requiere tocar los casos
de uso: hay que implementar `Put/Get/Delete` contra S3 y elegir ese adaptador en
`backend/cmd/api/main.go`.

Tampoco hay que olvidar esto: las imágenes oficiales de MinIO dejaron de
publicarse y el binario de `dl.min.io` ya no está disponible (devuelve
`410 Gone`). Por eso el Compose no trae `minio/minio` como imagen por defecto:
usa **`pgsty/minio`**, un fork comunitario mantenido, con la misma API S3 y la
misma consola.

## Opción recomendada: `pgsty/minio` con el Compose

```bash
docker pull pgsty/minio:latest
docker compose --profile files up -d minio
docker compose --profile files logs -f minio
```

La consola queda en http://localhost:9001 y la API S3 en
http://localhost:9000. Credenciales por defecto del Compose (cambialas en
`.env`): usuario `salon`, clave `salon-secret-123`.

Si preferís un contenedor suelto, sin Compose:

```bash
docker run -d --name salon-minio -p 9000:9000 -p 9001:9001 -e MINIO_ROOT_USER=salon -e MINIO_ROOT_PASSWORD=salon-secret-123 -v salon_minio:/data pgsty/minio server /data --console-address ":9001"
```

Los comandos de esta página funcionan igual en **fish** y en **bash**: no usan
el prefijo `VAR=valor comando` ni sustitución `$(...)`.

## Alternativas si `pgsty/minio` no te sirve

Estas imágenes resuelven hoy. No todas exponen la misma consola; para un futuro
adaptador S3 alcanza con que hablen S3 en `localhost:9000`.

| Imagen | Nota |
|---|---|
| `pgsty/minio:latest` | **default del Compose**; fork mantenido de MinIO, misma API/consola |
| `bitnamilegacy/minio:latest` | copia congelada de Bitnami; el entrypoint maneja sus propios paths, revisá los logs |
| `cgr.dev/chainguard/minio:latest` | build endurecido de Chainguard |
| `rustfs/rustfs:latest` | implementación S3 en Rust; su propia consola |
| `chrislusf/seaweedfs:latest` | S3 + filesystem distribuido; requiere configurar `weed` |
| `dxflrs/garage:v1.0.1` | S3 compatible pensado para self-hosting; requiere config inicial |

Ejemplo con la copia legacy de Bitnami (sin `command`: la imagen trae su
entrypoint y su propio directorio de datos):

```bash
docker run -d --name salon-minio -p 9000:9000 -p 9001:9001 -e MINIO_ROOT_USER=salon -e MINIO_ROOT_PASSWORD=salon-secret-123 -v salon_minio:/bitnami/minio/data bitnamilegacy/minio:latest
docker logs -f salon-minio
```

### Compilar MinIO desde el código fuente

Último recurso: compilar el servidor (tarda varios minutos y necesita ~2 GB de
RAM libres).

```bash
go install github.com/minio/minio@latest
go env GOPATH
env MINIO_ROOT_USER=salon MINIO_ROOT_PASSWORD=salon-secret-123 ~/go/bin/minio server ~/minio-data --console-address ":9001"
```

Si `go env GOPATH` devuelve otra ruta, ajustá la del binario en el último
comando.

## Apuntar el Compose a la imagen que funcione

El servicio lee `MINIO_IMAGE` del `.env`. Editá esa línea y verificá que el
Compose la tome:

```bash
# .env
# MINIO_IMAGE=pgsty/minio:latest
# MINIO_IMAGE=bitnamilegacy/minio:latest
docker compose --profile files config | grep image
docker compose --profile files up -d minio
```

Si el contenedor falla al arrancar con una imagen que no es `pgsty/minio`,
revisá que los argumentos `server /data --console-address ":9001"` sean los de
esa imagen (`docker logs salon-minio`); algunas traen otro entrypoint.

## Crear el bucket

La app todavía no lee ni escribe en un bucket, pero cuando exista el adaptador
S3 va a necesitar uno (por ejemplo `salon-media`). Dos caminos:

### Consola web (siempre disponible)

1. Abrir http://localhost:9001 e iniciar sesión con `salon` / `salon-secret-123`
   (o lo que hayas puesto en `.env`).
2. **Buckets → Create Bucket**, nombre `salon-media`, sin acceso público.
3. Opcional: crear también `salon-media-dev` para pruebas.

### Con un cliente S3 instalado

Con `mc` (cliente de MinIO, si lo tenés instalado):

```bash
mc alias set local http://localhost:9000 salon salon-secret-123
mc mb --ignore-existing local/salon-media
mc anonymous set none local/salon-media
mc ls local
```

Con AWS CLI:

```bash
env AWS_ACCESS_KEY_ID=salon AWS_SECRET_ACCESS_KEY=salon-secret-123 aws --endpoint-url http://localhost:9000 s3 mb s3://salon-media
env AWS_ACCESS_KEY_ID=salon AWS_SECRET_ACCESS_KEY=salon-secret-123 aws --endpoint-url http://localhost:9000 s3 ls
```

## Qué falta para integrarlo de verdad

1. Nuevo adaptador en `backend/internal/adapters/` que implemente
   `ports.PhotoStorage` (`Put`, `Get`, `Delete`) sobre un cliente S3.
2. Variables nuevas para endpoint, credenciales y bucket (por ejemplo
   `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`), documentadas
   en `docs/ops/environment.md` y pasadas por `docker-compose.yml`.
3. Cambiar una línea en `backend/cmd/api/wire_media.go` (`photoStorage`): `storage.Local` →
   `storage.S3` (el composition root es el único que elige adaptador).
4. Tests del adaptador con MinIO levantado por el perfil `files`.
