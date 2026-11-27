# API — reseñas posvisita (F13)

Base: `/api/v1`. Dos grupos: las **públicas** del enlace que viaja en el correo
(sin sesión) y las del **panel** (con `Authorization: Bearer <jwt>`).

Reglas transversales (las mismas de los otros módulos):

- Errores `{"error": "<mensaje>"}` en español: `400` validación, `401` sin token,
  `403` sin permiso, `404` no existe, `409` conflicto.
- Toda consulta del panel va scopeada por salón: la reseña de otro salón es `404`
  (no se filtra ni su existencia).
- Fechas `RFC3339` en UTC. El día de la reseña en la web pública va como fecha
  (`YYYY-MM-DD`) en la **zona horaria del salón**.
- UNA reseña por turno atendido: solo se escribe por el enlace del correo, nunca
  desde el panel. El salón puede **ocultar** una reseña, nunca editarla ni
  borrarla.
- Dinero: ninguno.

Contrato completo en
[`odd/tasks/fase6-roadmap.md`](../../odd/tasks/fase6-roadmap.md), sección
«Contrato F13 (resenas posvisita)» (D1–D10).

## El enlace del correo

El correo pide la reseña con `<APP_URL>/resena#<token>`. El token va en el
**fragmento** (no llega al servidor ni queda en un log) y son 32 bytes aleatorios
en base64url (43 caracteres). La base guarda **solo su SHA-256**; vence a los
**30 días** y sirve **una sola vez**.

Un token desconocido, vencido o ya usado responde **el mismo `404`**
(`{"error":"Enlace no válido"}`): la respuesta no dice cuál de los tres es.

### `GET /public/reviews/:token`

Público, con límite por IP. Sin sesión.

```json
{
  "salon_name": "Luna Studio",
  "service_name": "Corte y peinado",
  "starts_at": "2026-11-03T18:00:00Z",
  "timezone": "America/Mexico_City"
}
```

`service_name` puede ir vacío (turno sin servicio): la página oculta la línea.
Nunca lleva el nombre, el correo ni el teléfono de la clienta.

`404` uniforme: token con forma inválida (no consulta la base), desconocido,
vencido o usado.

### `POST /public/reviews/:token`

Público, con límite por IP (`20/min` por IP, cubo propio). Sin sesión.

```json
{ "rating": 5, "comment": "Excelente atención" }
```

- `rating`: obligatorio, entero de `1` a `5`.
- `comment`: opcional, hasta **500 caracteres** (runas, no bytes: un emoji cuenta
  uno), se recorta. Sin `NUL` ni caracteres de control (se aceptan `\n` y `\t`);
  un control, un `NUL` o un UTF-8 inválido es `400`, nunca `500`. El comentario se
  publica como **texto plano**.

`201` → `{"ok":true}`.

`400` con el texto del problema: `La calificación debe ser entre 1 y 5`,
`El comentario no puede superar los 500 caracteres`, `El comentario tiene
caracteres no válidos`, `body inválido`.

Un `400` **no consume** el enlace. El canje (consumir el token + crear la reseña)
es **una sola transacción**: si llegan dos `POST` a la vez, uno crea la reseña y el
otro recibe el `404` uniforme (`Enlace no válido`); nunca hay dos reseñas.

## Panel

Ver la lista y la configuración: **owner/admin/recepción**. Guardar la
configuración y ocultar/mostrar: **owner/admin**. Profesional, clienta y una
membresía dada de baja reciben `403` **antes** de mirar parámetros o cuerpo.

### `GET /salons/:id/reviews?limit=&before=`

```json
{
  "items": [
    {
      "id": "c7f0…",
      "rating": 5,
      "comment": "Excelente atención",
      "author_name": "Lía G.",
      "hidden": false,
      "created_at": "2026-11-04T12:00:00Z",
      "service_name": "Corte y peinado"
    }
  ],
  "next_cursor": "MjAyNi0xMS0wNFQxMjowMDowMFp8YzdmMA"
}
```

- `limit`: `1..100`, por defecto `20`. Otro valor → `400`
  (`El limite debe ser entre 1 y 100`).
- `before`: el `next_cursor` de la página anterior (cursor opaco, el mismo de la
  auditoría). Un cursor inválido → `400` (`Cursor invalido`).
- `next_cursor` es `null` en la última página. Orden: más nueva primero.
- `author_name` es el nombre público (nombre de pila + inicial del apellido),
  nunca el correo, el teléfono ni el turno.

### `GET /salons/:id/review-settings`

```json
{ "enabled": true, "delay_hours": 24 }
```

Sin configurar (o nunca guardada) → `{"enabled":false,"delay_hours":24}`.

### `PUT /salons/:id/review-settings`

Solo owner/admin (el permiso va **antes** del cuerpo). **Reemplaza**: las dos
claves son obligatorias.

```json
{ "enabled": true, "delay_hours": 24 }
```

- Falta una clave (o viene `null`) → `400`
  (`Faltan datos de las reseñas: activadas y demora`).
- `delay_hours` fuera de `1..72` o no entero → `400`
  (`La demora debe ser entre 1 y 72 horas`).
- Un tipo equivocado → `400` (`body inválido`).

Responde `200` con la configuración guardada. Con `enabled:false` el barrido no
pide reseñas nuevas; las ya publicadas se conservan.

### `POST /salons/:id/reviews/:rid/hide` y `.../unhide`

Solo owner/admin. Idempotente (ocultar dos veces responde lo mismo).
`200` → `{"ok":true}`.

`404` (`Reseña no encontrada`) si la reseña es de otro salón o el id no puede
existir. Ocultar una reseña la saca de la web pública **y del promedio**.

Auditoría (F10): `review.settings` (el `PUT`), `review.hide` y `review.unhide`.

## Web pública (aditiva)

`GET /public/salons/:slug` suma la clave `reviews` **solo cuando hay al menos una
reseña visible**; sin reseñas (o si la consulta falla) la clave **no viaja** y el
payload es exactamente el de antes. Nunca un `500` por esta sección.

```json
"reviews": {
  "average": 4.6,
  "count": 12,
  "items": [
    { "rating": 5, "comment": "Excelente atención", "author": "Lía G.", "date": "2026-11-04" }
  ]
}
```

- `average`: una decimal, calculado en el servidor sobre **todas** las reseñas
  visibles (no solo las que se muestran).
- `count`: total de visibles.
- `items`: como máximo las **6 más recientes** visibles.
- `author`: nombre de pila + inicial del apellido («Lía G.»), nunca el correo, el
  teléfono ni el turno.
- `date`: el día de la reseña en la **zona horaria del salón** (`YYYY-MM-DD`).

Una reseña oculta en el panel desaparece de la web y del promedio: lo que ve el
cliente coincide con el panel.
