# Guía de Despliegue — Dex con Keystone y TLS

> Imagen: `ghcr.io/rasty94/dex:latest`

Esta guía cubre el despliegue completo de Dex como proveedor OIDC federado con Keystone, incluyendo HTTPS con certificados propios.

---

## Requisitos

- Docker >= 24 y Docker Compose v2
- `openssl` instalado en el host (para generar certificados)
- Acceso a un endpoint de Keystone v3 (OpenStack)

---

## 1. Estructura de directorios

```
despliegue-dex/
├── docker-compose.yml
├── config.yaml          ← configuración de Dex
├── server-cert.pem      ← certificado TLS (generado)
└── server-key.pem       ← clave privada TLS (generada)
```

---

## 2. Generar certificados TLS

### Opción A — Certificado autofirmado (desarrollo/pruebas)

```bash
# Generar clave privada y certificado autofirmado (válido 3 años)
openssl req -x509 -newkey rsa:4096 -keyout server-key.pem -out server-cert.pem \
  -days 1095 -nodes \
  -subj "/CN=dex.ejemplo.com" \
  -addext "subjectAltName=DNS:dex.ejemplo.com,IP:127.0.0.1"
```

### Opción B — Certificado firmado por CA propia

```bash
# 1. Crear CA
openssl genrsa -out ca-key.pem 4096
openssl req -new -x509 -days 3650 -key ca-key.pem -out ca-cert.pem \
  -subj "/CN=Mi-CA-Interna"

# 2. Crear clave y CSR para Dex
openssl genrsa -out server-key.pem 4096
openssl req -new -key server-key.pem -out server.csr \
  -subj "/CN=dex.ejemplo.com"

# 3. Firmar con la CA
cat > san.ext <<EOF
subjectAltName=DNS:dex.ejemplo.com,IP:127.0.0.1
EOF
openssl x509 -req -days 1095 -in server.csr \
  -CA ca-cert.pem -CAkey ca-key.pem -CAcreateserial \
  -out server-cert.pem -extfile san.ext

# 4. Limpiar
rm server.csr san.ext
```

### Opción C — Let's Encrypt (producción con dominio público)

```bash
# Con certbot standalone (detener Dex antes)
certbot certonly --standalone -d dex.ejemplo.com
cp /etc/letsencrypt/live/dex.ejemplo.com/fullchain.pem server-cert.pem
cp /etc/letsencrypt/live/dex.ejemplo.com/privkey.pem server-key.pem
```

---

## 3. `docker-compose.yml`

```yaml
version: "3.8"

services:
    dex:
        image: ghcr.io/rasty94/dex:latest
        container_name: dex
        restart: unless-stopped
        ports:
            - "5554:5554" # HTTPS (OIDC)
            - "5556:5556" # HTTP (redirección / health)
            - "5557:5557" # gRPC API (opcional)
            - "5558:5558" # Telemetría / Prometheus
        volumes:
            - ./config.yaml:/etc/dex/config.yaml:ro
            - ./server-cert.pem:/etc/dex/server-cert.pem:ro
            - ./server-key.pem:/etc/dex/server-key.pem:ro
            - dex-data:/var/dex
        command: ["dex", "serve", "/etc/dex/config.yaml"]
        environment:
            # Activar la API gRPC para gestión de clientes/passwords
            - DEX_API_CONNECTORS_CRUD=true
        networks:
            - dex-network
        healthcheck:
            test: ["CMD", "wget", "-qO-", "http://localhost:5556/healthz"]
            interval: 30s
            timeout: 5s
            retries: 3

volumes:
    dex-data:

networks:
    dex-network:
        driver: bridge
```

---

## 4. `config.yaml`

```yaml
# ============================================================
# Dex — Configuración con Keystone + TLS
# ============================================================

issuer: https://dex.ejemplo.com:5554/dex

storage:
    type: sqlite3
    config:
        file: /var/dex/dex.db
    # Para producción con alta disponibilidad, usar PostgreSQL:
    # type: postgres
    # config:
    #   host: postgres.ejemplo.com
    #   port: 5432
    #   database: dex
    #   user: dex
    #   password: <contraseña>
    #   ssl:
    #     mode: require

web:
    https: 0.0.0.0:5554
    tlsCert: /etc/dex/server-cert.pem
    tlsKey: /etc/dex/server-key.pem
    http: 0.0.0.0:5556
    # Cabeceras de seguridad recomendadas:
    headers:
        X-Frame-Options: "DENY"
        X-Content-Type-Options: "nosniff"
        X-XSS-Protection: "1; mode=block"
        Content-Security-Policy: "default-src 'self'"
        Strict-Transport-Security: "max-age=31536000; includeSubDomains"

grpc:
    addr: 0.0.0.0:5557
    tlsCert: /etc/dex/server-cert.pem
    tlsKey: /etc/dex/server-key.pem

telemetry:
    http: 0.0.0.0:5558

logger:
    level: info
    format: json

expiry:
    signingKeys: "6h"
    idTokens: "24h"
    refreshTokens:
        validIfNotUsedFor: "2160h" # 90 días
        absoluteLifetime: "3960h" # 165 días

oauth2:
    responseTypes: ["code"]
    skipApprovalScreen: false
    alwaysShowLoginScreen: true

connectors:
    - type: keystone
      id: keystone
      name: Keystone
      config:
          keystoneHost: https://keystone.ejemplo.com:5000
          domain: default
          keystoneUsername: dex-service
          # ⚠️ En producción usar variable de entorno:
          # keystonePassword: $KEYSTONE_PASSWORD
          keystonePassword: <contraseña-segura>
          # userIDKey: email   # Opcional: "email" o "name"

# Base de datos local de usuarios (opcional, Dex nativo)
enablePasswordDB: false

# Clientes OIDC registrados estáticamente
# (también se pueden gestionar vía API gRPC)
staticClients:
    - id: mi-aplicacion
      secret: <client-secret>
      name: "Mi Aplicación"
      redirectURIs:
          - "https://mi-app.ejemplo.com/callback"
```

---

## 5. Arrancar el servicio

```bash
# Verificar los certificados
openssl verify -CAfile server-cert.pem server-cert.pem 2>/dev/null || echo "Autofirmado (normal)"
openssl x509 -in server-cert.pem -noout -dates

# Arrancar Dex
docker compose up -d

# Verificar que arranca correctamente
docker compose logs -f dex

# Probar el endpoint OIDC discovery
curl -k https://localhost:5554/dex/.well-known/openid-configuration | jq .
```

---

## 6. Verificar la autenticación con Keystone

```bash
# Si tienes habilitada la API gRPC, registrar un cliente de prueba:
docker run --rm --network host \
  ghcr.io/rasty94/dex:latest \
  dexctl --issuer https://localhost:5554/dex clients create \
    --id test-client --secret test-secret \
    --redirect-uri http://localhost:5555/callback

# Obtener un token de prueba con el example-app incluido en el repo
# Ver: examples/example-app/
```

---

## 7. Renovación de certificados

Para Let's Encrypt con renovación automática:

```bash
# /etc/cron.d/dex-renew-cert
0 3 * * 1 root certbot renew --quiet --deploy-hook \
  "cp /etc/letsencrypt/live/dex.ejemplo.com/fullchain.pem /opt/dex/server-cert.pem && \
   cp /etc/letsencrypt/live/dex.ejemplo.com/privkey.pem /opt/dex/server-key.pem && \
   docker compose -f /opt/dex/docker-compose.yml restart dex"
```

---

## 8. Solución de problemas

| Síntoma                                         | Causa probable                   | Solución                                           |
| ----------------------------------------------- | -------------------------------- | -------------------------------------------------- |
| `x509: certificate signed by unknown authority` | Certificado autofirmado          | Añadir `ca-cert.pem` al trust store del cliente    |
| `connection refused` en puerto 5554             | Dex no arrancó                   | `docker compose logs dex`                          |
| `401 Unauthorized` desde Keystone               | Usuario de servicio sin permisos | Ver sección de permisos en `keystone_connector.md` |
| Formulario TOTP no aparece                      | MFA no habilitado en Keystone    | Activar método `totp` en `keystone.conf`           |
| Token refresh falla                             | Usuario eliminado de Keystone    | Comportamiento esperado                            |
