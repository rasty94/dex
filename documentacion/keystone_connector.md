# Análisis del Conector Keystone en Dex

## Integración

El conector de Keystone está implementado en `connector/keystone/keystone.go`.
Funciona como un "PasswordConnector" y "RefreshConnector", lo que significa que puede manejar autenticación con usuario/contraseña y refresco de tokens.

La integración se realiza a través de la API v3 de Keystone.

### Flujo de Autenticación

1.  **Inicio de Sesión (Login):**
    - Cuando un usuario intenta iniciar sesión, Dex recibe el nombre de usuario y contraseña.
    - Dex realiza una petición `POST` a `/v3/auth/tokens` con las credenciales del usuario para obtener un token.
    - Si la autenticación es exitosa, Dex obtiene el ID del usuario del token.
    - Posteriormente, usa las credenciales de **admin** (configuradas en el conector) para obtener detalles adicionales del usuario (email) y sus grupos.

2.  **Validación de Token (TokenIdentity):**
    - Permite validar un token existente.
    - Primero, obtiene un token de admin.
    - Luego, valida el token del usuario usando `GET /v3/auth/tokens` pasando el token del usuario en el header `X-Subject-Token` y el token de admin en `X-Auth-Token`.

## Permisos Requeridos en OpenStack

Para que el conector funcione correctamente, se necesitan unas credenciales de servicio (Service Account) con permisos suficientes para realizar las operaciones de validación e inspección de usuarios.

En la configuración se definen:

- `keystoneHost`: URL de Keystone.
- `domain`: Dominio del usuario de servicio.
- `keystoneUsername`: Usuario de servicio (Admin).
- `keystonePassword`: Contraseña.

### Operaciones y Permisos

Basado en la lista de políticas (`policy.json` o `policy.yaml`) y los requerimientos funcionales para la integración con Dex, aquí están las reglas exactas que aplican a cada operación:

#### 1. Autenticación (Self)

**Operación:** `POST /v3/auth/tokens`
El usuario de servicio necesita obtener un token para sí mismo.

- **Permiso relacionado:** `identity:get_access_token`
- **Regla configurada:**
    ```json
    "identity:get_access_token": "rule:admin_required"
    ```

    - **Observación:** Esta regla es muy restrictiva en la configuración actual. Requiere que el usuario sea `admin` incluso para obtener su propio token, a menos que el endpoint de autenticación pública (`issue_token`) esté exento de validación de políticas en la versión de Keystone.

#### 2. Validar tokens de otros usuarios

**Operación:** `GET /v3/auth/tokens` (Introspección/Validación)
Dex recibe un token de un usuario y pregunta a Keystone si es válido.

- **Permiso relacionado:** `identity:validate_token`
- **Regla configurada:**
    ```json
    "identity:validate_token": "rule:admin_required or (role:reader and system_scope:all) or rule:service_role or rule:token_subject"
    ```

    - **Análisis:** Esta es la regla crítica para integraciones. La configuración permite explícitamente `rule:service_role`.
    - **Requisito para Dex:** El usuario de Dex debe tener el rol `service` (definido como `"service_role": "role:service"`).

#### 3. Obtener detalles de usuarios

**Operación:** `GET /v3/users/{user_id}`
Dex necesita leer la información (email, nombre) del usuario que se está autenticando.

- **Permiso relacionado:** `identity:get_user`
- **Regla configurada:**
    ```json
    "identity:get_user": "(rule:admin_required) or (role:reader and system_scope:all) or (role:reader and token.domain.id:%(target.user.domain_id)s) or user_id:%(target.user.id)s"
    ```

    - **Análisis:** Aquí **NO** está incluido `rule:service_role`.
    - **Requisito para Dex:** Dado que Dex está consultando un usuario ajeno (target != self), el usuario de servicio de Dex necesitará ser `admin` O tener el rol `reader` con `system_scope:all`. El rol `service` por sí solo **no será suficiente**.

#### 4. Listar grupos de un usuario

**Operación:** `GET /v3/users/{user_id}/groups`
Dex necesita saber a qué grupos pertenece el usuario.

- **Permiso relacionado:** `identity:list_groups_for_user`
- **Regla configurada:**
    ```json
    "identity:list_groups_for_user": "(rule:admin_required) or (role:reader and system_scope:all) or (role:reader and domain_id:%(target.user.domain_id)s) or user_id:%(user_id)s"
    ```

    - **Análisis:** Igual que el punto anterior. No incluye `service_role`.
    - **Requisito para Dex:** Requiere `admin` o `reader` (system scoped).

### Resumen de Configuración para el Usuario Dex

Para cumplir con los 4 requisitos con **esta lista de políticas exacta**, el usuario de servicio que se configure en Dex debe tener asignados los siguientes roles/scopes:

1.  **Opción A (Recomendada/Menor Privilegio):**
    - Rol: `reader`
    - Scope: `system` (all)
    - _Nota:_ Esto satisface los puntos 2, 3 y 4.

2.  **Opción B (Permisiva):**
    - Rol: `admin`
    - Scope: `project` o `system`
    - _Nota:_ Esto satisface todo, incluido el punto 1 si se aplica estrictamente.

**Advertencia:** Si solo se asigna el rol `service` al usuario de Dex, fallarán las llamadas 3 y 4 (`get_user` y `list_groups_for_user`) porque `rule:service_role` no está presente en esas líneas de la política.
