# Análisis del Conector Keystone en Dex

> Versión de este fork: `ghcr.io/rasty94/dex:latest`
> Actualizado: 2026-02-25

---

## Integración

El conector de Keystone está implementado en `connector/keystone/keystone.go`.
Funciona como `PasswordConnector`, `RefreshConnector` y `TokenIdentityConnector`, lo que significa que puede:

- Autenticar con usuario/contraseña (con soporte opcional de **TOTP/MFA**)
- Refrescar tokens
- **Validar tokens Keystone existentes** (Token Exchange / TokenIdentity)

La integración se realiza a través de la API v3 de Keystone.

---

## Flujos de Autenticación

### 1. Login con usuario/contraseña (sin MFA)

```
Cliente → Dex → POST /v3/auth/tokens (user+pass) → Keystone
Keystone → 201 Created + X-Subject-Token
Dex → GET /v3/users/{id} (con admin token) → obtiene email
Dex → GET /v3/users/{id}/groups → obtiene grupos
Dex → ID Token JWT firmado → Cliente
```

### 2. Login con TOTP/MFA (flujo en dos pasos)

Cuando Keystone tiene MFA habilitado para el usuario:

```
Paso 1 — Credenciales:
  Cliente → Dex → POST /v3/auth/tokens (user+pass)
  Keystone → 401 + header "Openstack-Auth-Receipt: <receipt>"
  Dex → renderiza formulario TOTP al usuario

Paso 2 — Código TOTP:
  Usuario introduce código de su app autenticadora
  Cliente → Dex → POST (totp_code + receipt)
  Dex → POST /v3/auth/tokens (methods:[totp], receipt: <receipt>)
  Keystone → 201 Created + X-Subject-Token
  Dex → obtiene email y grupos → ID Token JWT → Cliente
```

**El `receipt` es el identificador de sesión MFA** emitido por Keystone en el primer paso. Dex lo guarda en un campo oculto del formulario y lo reenvía en el segundo paso.

### 3. Validación de Token Existente (TokenIdentity)

Permite que un cliente presente un token Keystone válido directamente a Dex (útil para Token Exchange):

```
Cliente → Dex (subjectToken=<keystoneToken>)
Dex → GET /v3/auth/tokens (X-Auth-Token: adminToken, X-Subject-Token: userToken)
Keystone → 200 OK + datos del usuario
Dex → ID Token JWT → Cliente
```

### 4. Refresco de Token (Refresh)

```
Cliente → Dex (refresh_token)
Dex → admin_token → GET /v3/users/{id} (verifica que el usuario sigue existiendo)
Dex → nuevo ID Token → Cliente
```

---

## Parámetros de Configuración

```yaml
connectors:
    - type: keystone
      id: keystone
      name: Keystone
      config:
          # URL base de Keystone (sin /v3)
          keystoneHost: https://keystone.example.com:5000

          # Dominio: puede ser el UUID del dominio, "default", o el nombre del dominio
          domain: default

          # Credenciales del usuario de servicio (admin/reader con system scope)
          keystoneUsername: dex-service
          keystonePassword: <contraseña-segura>

          # [Opcional] Cómo derivar el UserID del token Keystone.
          # Valores: "email" (por defecto: UUID SHA1 del email) o "name" (UUID SHA1 del username)
          # Si no se especifica, se usa el ID nativo de Keystone.
          userIDKey: email

          # [Opcional] Dominio multi-tenant: si se activa, el campo "domain"
          # se muestra en el formulario de login y el usuario puede introducir el suyo.
          # showDomain: true
```

### Parámetros opcionales de TOTP

No hay configuración adicional para activar TOTP. El conector detecta automáticamente si Keystone responde con un `401 + Openstack-Auth-Receipt`. En ese caso:

1. Muestra el formulario de TOTP al usuario
2. El usuario introduce el código de su app autenticadora (Google Authenticator, FreeOTP, etc.)
3. Dex completa la autenticación con el receipt

---

## Permisos Requeridos en OpenStack

Para que el conector funcione correctamente se necesita un **usuario de servicio** con los siguientes permisos sobre la API v3 de Keystone:

| Operación                 | Endpoint                    | Policy key                      |
| ------------------------- | --------------------------- | ------------------------------- |
| Autenticarse (self)       | `POST /v3/auth/tokens`      | `identity:create_token`         |
| Validar token de usuario  | `GET /v3/auth/tokens`       | `identity:validate_token`       |
| Leer detalles del usuario | `GET /v3/users/{id}`        | `identity:get_user`             |
| Listar grupos del usuario | `GET /v3/users/{id}/groups` | `identity:list_groups_for_user` |

### Opción A — Menor privilegio (recomendada)

```bash
# Crear usuario de servicio
openstack user create dex-service --password <pass> --domain default

# Asignar rol "reader" con scope de sistema
openstack role add --user dex-service --system all reader
```

Con `reader` + `system_scope:all` se satisfacen las operaciones 2, 3 y 4.

> ⚠️ El endpoint `POST /v3/auth/tokens` no suele requerir privilegios especiales para autenticación propia — está fuera de las políticas regulares en la mayoría de deployments Keystone.

### Opción B — Admin (más permisivo)

```bash
openstack role add --user dex-service --system all admin
```

Satisface todas las operaciones sin restricciones. Usar solo si Opción A no funciona en tu versión de Keystone.

### ⚠️ Advertencia sobre el rol `service`

Si solo se asigna el rol `service` al usuario de Dex, **fallarán** las llamadas `GET /v3/users/{id}` y `GET /v3/users/{id}/groups` porque `rule:service_role` no está incluido en las políticas `identity:get_user` ni `identity:list_groups_for_user` en la mayoría de configuraciones estándar.

---

## Activar TOTP en OpenStack

Para activar TOTP para un usuario en Keystone:

```bash
# 1. Activar el método TOTP en Keystone (keystone.conf)
# [auth]
# methods = password,token,totp

# 2. Crear credencial TOTP para el usuario
openstack credential create <user_id> totp --blob '{"seed": "<base32_secret>"}'

# 3. El usuario configura su app con el QR o seed
```

Una vez configurado, Keystone empezará a devolver `401 + Openstack-Auth-Receipt` si el usuario intenta autenticarse solo con contraseña. Dex lo detecta automáticamente.

---

## Policy de Keystone Ajustada para Dex

Ver `documentacion/policy_modificado.yml` para la política completa ajustada.
Ver `documentacion/Permisos base keystone.md` para el análisis detallado de reglas.
