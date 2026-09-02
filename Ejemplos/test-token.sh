#!/usr/bin/env bash
set -euo pipefail

# Intercambia un token y valida el ID token resultante según OpenID (firma + claims JWT)
# Usa el Python del venv por defecto; ajusta PYTHON_EXEC si es necesario.

# UPSTREAM_TOKEN="gAAAAABplxqwuGIDcSfDqCV3Zj_iujFjX9FYxvL3Gs0_x680-T_BfiEk9cf0FL2mDN8oYgX8rGFo-hJO0yt4xfdFkRpefjTU2-Ubl4NvwYBSGlbFkKUh94vaUYhlXxJMWC3NuciA9TdPJ987HUGUhkFCUvN8qeVUotm8-tYD_GDvPN8DponXbOE"
# UPSTREAM_TOKEN="gAAAAABplt7Z-yg_fIfUIbvGRH5XZc1_TnAWuFPLF1gWkb2VwDBGzh-Btx_pKjRnlES_ByK_jxG1I_stOMm0KAURo91kUB-Vkk1qAgaHJ_R8YMYP9TrdJ1Ue0gdUgiBoCjHt1x_4kkYR81SBg_1kg0Vy3O7huvD_DDSpEyZKEu1Mub1Tde7s9Oc"

UPSTREAM_TOKEN="gAAAAABpl0AqWsTJ0ZhwjZoXZS9uaIR3BcJCSWGdWhRIc9M2XKu1iu20wyBYXRTgjR1cvW7U7SplLShhRkylvnkYuH1R8IQaRBbKztOr746tuy1wdaXzVZsQskG9pq2IhDvU5RHDWXw8HIYHt1Ddygcrld8HsxUMNnX5oVMDy4c6IZN9ptSlT5o"
# Configuración (se pueden sobrescribir vía entorno)
# DEX_ISSUER="${DEX_ISSUER:-https://keystone-federadolab.frikiteam.es:5554/dex}"
DEX_ISSUER="${DEX_ISSUER:-https://soax2-centdev.frikiteam.es:5556/dex}"
CLIENT_ID="${CLIENT_ID:-commvault-client}"
CLIENT_SECRET="${CLIENT_SECRET:-C0mmv4ultS3cr3t}"
VERIFY_SSL="${VERIFY_SSL:-true}"
PYTHON_EXEC="${PYTHON_EXEC:-/root/dex/venv/bin/python}"

CURL_OPTS=(--silent --show-error)
if [ "${VERIFY_SSL,,}" != "true" ]; then
  CURL_OPTS+=(--insecure)
fi

# CLI options
JSON_OUTPUT=0
DRY_RUN=0
EXPECTED_NONCE=""
REQUIRE_AT_HASH=0

usage() {
  cat <<'USAGE'
Usage: ./test-token.sh [options]

Options:
  --json               Output result in JSON (for CI/integration)
  --dry-run            Print the curl command and exit (no network)
  -n, --nonce VALUE     Expected nonce value to validate (when token contains nonce)
  --require-at-hash     Fail if ID token does not include at_hash claim
  -h, --help           Show this help

Examples:
  ./test-token.sh --json
  ./test-token.sh --dry-run
  ./test-token.sh -n mynonce --require-at-hash
USAGE
}

# Parse CLI args
while [ "$#" -gt 0 ]; do
  case "$1" in
    --json) JSON_OUTPUT=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -n|--nonce) EXPECTED_NONCE="$2"; shift 2 ;;
    --require-at-hash) REQUIRE_AT_HASH=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1"; usage; exit 2 ;;
  esac
done

if [ "$DRY_RUN" -eq 1 ]; then
  echo "DRY RUN: would call: curl ${CURL_OPTS[@]} '$DEX_ISSUER/token' --user '$CLIENT_ID:***' --data-urlencode connector_id=keystone --data-urlencode grant_type=urn:ietf:params:oauth:grant-type:token-exchange --data-urlencode subject_token='<redacted>'"
  exit 0
fi

# Comprueba si Python del venv existe
if ! command -v "$PYTHON_EXEC" >/dev/null 2>&1; then
  echo "❌ Python executable no encontrado en: $PYTHON_EXEC" >&2
  exit 1
fi

echo "📌 Intercambiando token en $DEX_ISSUER (verify_ssl=$VERIFY_SSL)"
RESP=$("curl" "${CURL_OPTS[@]}" "$DEX_ISSUER/token" \
  --user "$CLIENT_ID:$CLIENT_SECRET" \
  --data-urlencode connector_id=keystone \
  --data-urlencode grant_type=urn:ietf:params:oauth:grant-type:token-exchange \
  --data-urlencode scope="openid email profile" \
  --data-urlencode requested_token_type=urn:ietf:params:oauth:token-type:id_token \
  --data-urlencode subject_token="$UPSTREAM_TOKEN" \
  --data-urlencode subject_token_type=urn:ietf:params:oauth:token-type:access_token)

# Mostrar respuesta (intenta formatear con jq si está disponible)
if [ "${JSON_OUTPUT:-0}" -eq 0 ]; then
  echo "\n▶ Token endpoint response:"
  if command -v jq >/dev/null 2>&1; then
    echo "$RESP" | jq .
  else
    echo "$RESP"
  fi
fi

# Exportar variables para el validador Python
export VERIFY_SSL DEX_ISSUER CLIENT_ID JSON_OUTPUT EXPECTED_NONCE REQUIRE_AT_HASH
# Pasar la respuesta JSON al validador vía variable de entorno (evita problemas con stdin/heredoc)
export RESP_JSON="$RESP"

# Validación completa con Python (firma + claims OpenID)
echo "\n🔐 Validando ID token con Python (firma + claims OIDC)..."

"$PYTHON_EXEC" - <<'PY'
import sys, json, os, time, base64, hashlib
import requests
import jwt
from cryptography.hazmat.primitives.asymmetric.rsa import RSAPublicNumbers
from cryptography.hazmat.primitives import serialization

# Resultado estructurado (para --json)
JSON_OUTPUT = os.environ.get('JSON_OUTPUT', '0').lower() in ('1', 'true', 'yes')
EXPECTED_NONCE = os.environ.get('EXPECTED_NONCE')
result = { 'valid': False, 'errors': [], 'messages': [], 'claims': None, 'token_response': None }

# Leer respuesta JSON desde la variable de entorno RESP_JSON
try:
    raw = os.environ.get('RESP_JSON')
    if not raw:
        raise ValueError('RESP_JSON vacío')
    data = json.loads(raw)
    result['token_response'] = data
except Exception as e:
    result['errors'].append(f"Error leyendo RESP_JSON: {e}")
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print(f"❌ Error leyendo JSON de entrada desde RESP_JSON: {e}")
    sys.exit(2)

id_token = data.get('id_token') or data.get('access_token')
access_token = data.get('access_token')
if not id_token:
    msg = "No se encontró 'id_token' ni 'access_token' en la respuesta del token endpoint."
    result['errors'].append(msg)
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print(f"❌ {msg}")
    sys.exit(2)

VERIFY_SSL = os.environ.get('VERIFY_SSL', 'true').lower() == 'true'
DEX_ISSUER = os.environ.get('DEX_ISSUER')
CLIENT_ID = os.environ.get('CLIENT_ID')

# helpers
def b64u_decode(s):
    s = s.encode('ascii')
    rem = len(s) % 4
    if rem:
        s += b'=' * (4 - rem)
    return base64.urlsafe_b64decode(s)

# extraer header para obtener kid y alg
try:
    header_b64 = id_token.split('.')[0]
    header = json.loads(b64u_decode(header_b64))
    kid = header.get('kid')
    alg = header.get('alg', 'RS256')
except Exception as e:
    result['errors'].append(f"Error decodificando cabecera JWT: {e}")
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print(f"❌ Error decodificando cabecera JWT: {e}")
    sys.exit(2)

# discovery
try:
    disc = requests.get(f"{DEX_ISSUER}/.well-known/openid-configuration", verify=VERIFY_SSL, timeout=10).json()
    issuer = disc.get('issuer', DEX_ISSUER)
    jwks_uri = disc.get('jwks_uri')
except Exception as e:
    result['errors'].append(f"Error consultando discovery endpoint: {e}")
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print(f"❌ Error consultando discovery endpoint: {e}")
    sys.exit(2)

if not jwks_uri:
    result['errors'].append('jwks_uri no encontrado en discovery')
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print("❌ jwks_uri no encontrado en discovery.")
    sys.exit(2)

# obtener JWK
try:
    jwks = requests.get(jwks_uri, verify=VERIFY_SSL, timeout=10).json()
    keys = jwks.get('keys', [])
    jwk = next((k for k in keys if k.get('kid') == kid), None)
    if jwk is None:
        raise ValueError(f"JWK con kid={kid} no encontrada en {jwks_uri}")
except Exception as exc:
    result['errors'].append(f"Error procesando JWKS: {exc}")
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print(f"❌ Error procesando JWKS: {exc}")
    sys.exit(2)

# verificar firma y comprobaciones temporales
try:
    # Usamos PyJWKClient (PyJWT) para obtener la clave de firma desde el JWKS
    jwk_client = jwt.PyJWKClient(jwks_uri)
    signing_key = jwk_client.get_signing_key_from_jwt(id_token).key

    # Decodificar y validar firma + issuer + audience + exp
    decoded = jwt.decode(
        id_token,
        signing_key,
        algorithms=[alg],
        audience=CLIENT_ID if CLIENT_ID else None,
        issuer=issuer,
    )
    result['messages'].append('Firma y verificación temporal OK')

except jwt.exceptions.ExpiredSignatureError as e:
    result['errors'].append(f"Token expirado: {e}")
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print(f"❌ Token expirado: {e}")
    sys.exit(2)
except jwt.exceptions.InvalidAudienceError as e:
    result['errors'].append(f"Audience inválido: {e}")
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print(f"❌ Audience inválido: {e}")
    sys.exit(2)
except jwt.exceptions.InvalidIssuerError as e:
    result['errors'].append(f"Issuer inválido: {e}")
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print(f"❌ Issuer inválido: {e}")
    sys.exit(2)
except jwt.exceptions.DecodeError as e:
    result['errors'].append(f"JWT decode error: {e}")
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print(f"❌ Error validando JWT: {e}")
    sys.exit(2)
except Exception as e:
    result['errors'].append(f"Error inesperado al verificar la firma JWT: {e}")
    if JSON_OUTPUT:
        print(json.dumps(result, indent=2, default=str))
        sys.exit(2)
    print(f"❌ Error inesperado al verificar la firma JWT: {e}")
    sys.exit(2)

# comprobaciones OIDC (issuer, audience, required claims)
if decoded.get('iss') != issuer:
    result['errors'].append(f"Issuer (iss) inválido - esperado: {issuer} encontrado: {decoded.get('iss')}")

aud = decoded.get('aud')
if isinstance(aud, list):
    aud_ok = CLIENT_ID in aud
else:
    aud_ok = (aud == CLIENT_ID)
if not aud_ok:
    result['errors'].append(f"Audience inválido — debe contener: {CLIENT_ID} (found: {aud})")

required_claims = ['exp', 'iss', 'sub', 'aud']
missing = [c for c in required_claims if c not in decoded]
if missing:
    result['errors'].append(f"Claims requeridos ausentes: {missing}")

# comprobaciones OpenID adicionales (nonce, at_hash, email_verified, iat/exp)
now = int(time.time())
if decoded.get('exp', 0) <= now:
    result['errors'].append('exp en el pasado')
if 'iat' not in decoded:
    result['errors'].append('iat ausente')

# nonce
if 'nonce' in decoded:
    if EXPECTED_NONCE:
        if decoded.get('nonce') != EXPECTED_NONCE:
            result['errors'].append('nonce mismatch')
    else:
        result['errors'].append('nonce presente en token pero no se proporcionó EXPECTED_NONCE (usa -n)')

# at_hash validation & reporting
result['at_hash'] = {'present': False, 'valid': None, 'expected': None, 'actual': None}
REQUIRE_AT_HASH = os.environ.get('REQUIRE_AT_HASH', '0').lower() in ('1', 'true', 'yes')
if 'at_hash' in decoded:
    result['at_hash']['present'] = True
    result['at_hash']['actual'] = decoded.get('at_hash')
    if not access_token:
        result['errors'].append('at_hash presente pero falta access_token en la respuesta')
        result['at_hash']['valid'] = False
    else:
        hash_fn = None
        if alg.endswith('256'):
            hash_fn = hashlib.sha256
        elif alg.endswith('384'):
            hash_fn = hashlib.sha384
        elif alg.endswith('512'):
            hash_fn = hashlib.sha512
        if not hash_fn:
            result['errors'].append(f"Algoritmo {alg} no soportado para validación de at_hash")
            result['at_hash']['valid'] = False
        else:
            digest = hash_fn(access_token.encode('utf-8')).digest()
            left = digest[:len(digest)//2]
            at_hash_expected = base64.urlsafe_b64encode(left).decode('ascii').rstrip('=')
            result['at_hash']['expected'] = at_hash_expected
            if at_hash_expected == decoded.get('at_hash'):
                result['at_hash']['valid'] = True
                result['messages'].append('at_hash validado correctamente')
            else:
                result['at_hash']['valid'] = False
                result['errors'].append('at_hash mismatch')
else:
    # at_hash no presente
    if REQUIRE_AT_HASH:
        result['errors'].append('at_hash ausente en ID token (se requiere --require-at-hash)')
# email_verified tipo
if 'email' in decoded and 'email_verified' in decoded:
    if not isinstance(decoded['email_verified'], bool):
        result['errors'].append('email_verified no es booleano')

# set claims
result['claims'] = decoded

# Output
if JSON_OUTPUT:
    result['valid'] = (len(result['errors']) == 0)
    print(json.dumps(result, indent=2, default=str))
    sys.exit(0 if result['valid'] else 2)

# Human-friendly output (cuando no --json)
if result['errors']:
    print('\n❌ Errores de validación:')
    for e in result['errors']:
        print('  -', e)
    sys.exit(2)

print('\n✅ Validación completa OK — firma y claims OIDC válidos')
print('\n🔎 Claims extraídos:')
for k, v in decoded.items():
    print(f"  {k}: {v}")

# at_hash status (human readable)
if result.get('at_hash') is not None:
    ah = result['at_hash']
    print('\n🔐 at_hash:')
    print(f"  present: {ah['present']}, valid: {ah['valid']}")
    if ah.get('expected') is not None:
        print(f"  expected: {ah['expected']}")
    if ah.get('actual') is not None:
        print(f"  actual: {ah['actual']}")

sys.exit(0)
PY
