#!/usr/bin/env bash
# Carga datos de demo por la API para que la pagina principal muestre un salon:
# una duena, el salon "aurelia" (el que abre la home), su perfil y tres servicios.
# Se puede repetir sin romper nada. Uso:
#   bash scripts/seed-demo.sh                      # contra http://localhost:8080
#   API=http://localhost:8090 bash scripts/seed-demo.sh
# Entrada al panel: dueno@salon.local / SalonDemo2026!
set -euo pipefail
API="${API:-http://localhost:8080}/api/v1"
EMAIL="${DEMO_EMAIL:-dueno@salon.local}"
PASS="${DEMO_PASS:-SalonDemo2026!}"
die() { printf '\nERROR: %s\n' "$*" >&2; exit 1; }
json() { python3 -c "import sys,json
try: d=json.load(sys.stdin)
except Exception: d={}
print(d.get('$1') or '')"; }

curl -fsS "$API/../health" >/dev/null 2>&1 || curl -fsS "${API%/api/v1}/health" >/dev/null 2>&1 || die "La API no responde en ${API%/api/v1}. Levantala primero (docker compose up -d --build, o bash scripts/dev-local.sh)."

echo "==> Cuenta de la duena"
curl -s -o /dev/null -X POST "$API/auth/register" -H 'Content-Type: application/json' \
  -d "{\"name\":\"Dueña Demo\",\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" || true
TOKEN=$(curl -s -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" | json token)
[ -n "$TOKEN" ] || die "No pude iniciar sesion con $EMAIL (si la cuenta ya existia con otra clave, usa DEMO_EMAIL y DEMO_PASS)."
H="Authorization: Bearer $TOKEN"

echo "==> Salon 'aurelia'"
SID=$(curl -s -X POST "$API/salons" -H "$H" -H 'Content-Type: application/json' \
  -d '{"name":"Salón Aurelia","slug":"aurelia"}' | json id)
if [ -z "$SID" ]; then
  # Ya existia (o la cuenta ya tenia un salon): usar el de la cuenta.
  SID=$(curl -s "$API/me" -H "$H" | python3 -c "import sys,json
d=json.load(sys.stdin); m=d.get('memberships') or d.get('salons') or []
print((m[0].get('salon_id') or m[0].get('id')) if m else '')")
fi
[ -n "$SID" ] || die "No pude crear ni encontrar el salon."

echo "==> Perfil del salon (si esta version de la API lo soporta)"
curl -s -o /dev/null -w "perfil: HTTP %{http_code}\n" -X PUT "$API/salons/$SID/profile" -H "$H" -H 'Content-Type: application/json' \
  -d '{"name":"Salón Aurelia","description":"Peluquería y estética en el centro de la ciudad.","address":"Av. Principal 123","phone":"+54 9 11 5555-1234","email":"hola@aurelia.test","instagram":"aurelia.salon"}' || true

echo "==> Servicios"
UID_=$(curl -s "$API/salons/$SID/members" -H "$H" | python3 -c "import sys,json
d=json.load(sys.stdin); l=d.get('members') if isinstance(d,dict) else d
print((l[0].get('id') or l[0].get('user_id')) if l else '')")
EXISTING=$(curl -s "$API/salons/$SID/services" -H "$H" | python3 -c "import sys,json
d=json.load(sys.stdin); l=d.get('services') if isinstance(d,dict) else d
print(len(l or []))" 2>/dev/null || echo 0)
if [ "${EXISTING:-0}" = "0" ] && [ -n "$UID_" ]; then
  for s in 'Corte de pelo|45|180000' 'Color completo|120|650000' 'Manicura|40|120000'; do
    IFS='|' read -r n d p <<< "$s"
    curl -s -o /dev/null -X POST "$API/salons/$SID/services" -H "$H" -H 'Content-Type: application/json' \
      -d "{\"name\":\"$n\",\"duration_min\":$d,\"price_cents\":$p,\"commission_pct\":0,\"description\":\"$n con productos profesionales.\",\"professional_ids\":[\"$UID_\"]}"
  done
  echo "tres servicios creados"
else
  echo "el salon ya tiene servicios: no toco nada"
fi

echo
echo "Listo. Pagina publica: http://localhost:${WEB_PORT:-8081}/  (con el servidor de desarrollo: http://localhost:5173/)"
echo "Panel: /login  ->  $EMAIL / $PASS"
