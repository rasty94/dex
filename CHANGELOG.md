# CHANGELOG — Dex Fork (rasty94/dex)

Versiones de este fork sobre la base de [dexidp/dex](https://github.com/dexidp/dex).

El formato sigue [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

---

## [Unreleased]

### ⚠️ Cambios incompatibles

- **El claim `sub` vuelve al formato de upstream.** Este fork emitía un `sub` plano
  (el user id tal cual); ahora vuelve a ser el par `(userID, connectorID)` codificado
  en protobuf-base64, igual que dexidp/dex. Motivo: el `sub` plano no lleva el
  connector, y todo lo que resuelve un subject contra una sesión offline lo necesita
  — la API gRPC de refresh, y la revocación con alcance de sesión que upstream ha
  construido encima. Mantenerlo nos dejaba fuera de esas features para siempre.
  - **Impacto:** cualquier consumidor que guarde o compare el `sub` de un ID token
    verá un valor distinto para el mismo usuario. `ListRefresh`/`RevokeRefresh` ahora
    exigen el subject codificado y rechazan un user id plano con error, en lugar de
    buscar al usuario recorriendo los conectores.
  - **Migración:** los clientes que indexen por `sub` deben re-mapear a los usuarios
    en su siguiente autenticación. No hay cambio de esquema en storage.

- **Las sesiones Keystone anteriores a esta versión no refrescan.** `Refresh()` resuelve
  ahora el usuario por el id real de Keystone, que se guarda en `identity.ConnectorData`
  al autenticarse. Las sesiones creadas antes de ese cambio no lo llevan y caen al
  fallback (`identity.UserID`), que con `userIDKey: email` o `username` es un UUID
  sintético derivado del correo o del nombre, no un id que Keystone reconozca.
  - **Impacto:** el refresh de esas sesiones falla contra Keystone. Afecta solo a los
    despliegues que hubieran configurado `userIDKey`; con el valor por defecto el
    `UserID` ya era el id real y no hay diferencia.
  - **Migración:** ninguna automática. El usuario vuelve a autenticarse una vez y la
    sesión nueva ya guarda el `ConnectorData` correcto.

- **`mfaTrust` exige ahora una clave de cifrado.** La cookie de dispositivo de confianza
  guardaba el token de Keystone en claro. Ese token no es un identificador de sesión de
  dex: es una credencial viva para todo el despliegue de OpenStack. Ahora va sellada con
  AES-GCM, con las mismas primitivas que la cookie de sesión de upstream.
  - **Impacto:** con `mfaTrust.enabled: true` y sin `mfaTrust.encryptionKey`, dex se
    niega a arrancar. Es deliberado: la alternativa era seguir escribiendo la credencial
    en claro. Las cookies emitidas por versiones anteriores no se pueden abrir, así que
    los dispositivos ya confiados vuelven a pedir el segundo factor una vez.
  - **Migración:** añadir `mfaTrust.encryptionKey` con 16, 24 o 32 bytes.

### Seguridad

- Binding del auth code en el callback del device flow (canje entre clientes)
- `client_secret` ya no viaja en el redirect del navegador
- Comparaciones de secreto a tiempo constante en refresh y device flow
- Saneado del parámetro `back` (open redirect en la pantalla de login)
- La contraseña ya no se re-inyecta en un campo oculto durante el paso TOTP
- Dependencias de criptografía, JOSE y SAML al día con upstream

### En progreso

- Tests unitarios TOTP con mocking del endpoint Keystone
- Externalización opcional de traducciones desde volumen en tiempo de ejecución

---

## [1.0.0] — 2026-02-25

Primera release consolidada del fork. Integra todas las mejoras sobre el upstream de dexidp.

### ✨ Añadido

#### Conector Keystone — TOTP / MFA

- Soporte completo de autenticación en **dos pasos con TOTP** (RFC 6238)
- Detección automática de `ErrTOTPRequired` cuando Keystone responde `401 + Openstack-Auth-Receipt`
- Flujo MFA: credenciales → formulario TOTP → validación con receipt
- Context keys: `TOTPContextKey`, `ReceiptContextKey` para pasar estado entre capas

#### Conector Keystone — Nuevas funcionalidades

- **`TokenIdentity()`**: validación de tokens Keystone existentes vía `GET /v3/auth/tokens` (Token Exchange)
- **`UserIDKey`**: campo de config para derivar el UserID como UUID SHA1 de `email` o `name`
- **Multi-dominio dinámico**: el dominio puede venir del formulario de login (`showDomain: true`)
- Mejorado manejo de errores en `getAdminToken()` y `getTokenResponse()`
- Corrección de bug: `defer resp.Body.Close()` correctamente ordenado en `getUserGroups()`

#### Internacionalización (i18n)

- Sistema i18n completo con 5 idiomas: **EN, ES, FR, DE, PT**
- Ficheros YAML embebidos en el binario con `//go:embed` (`server/i18n/*.yaml`)
- Añadir idioma = crear `.yaml` y recompilar, sin modificar Go
- Parser de `Accept-Language` completo (soporta `es-ES,es;q=0.9,en;q=0.8`)
- Nueva función `SupportedLanguages()` para introspección
- Claves: login, password, TOTP, dominio, approval, device, OOB, error, footer

#### UI / Frontend

- Iconos SVG para los 12 tipos de connector en la pantalla de login:
  `github`, `gitlab`, `google`, `microsoft`, `linkedin`, `bitbucket`, `gitea`, `ldap`, `keystone`, `saml`, `oidc`, `atlassiancrowd`
- Formulario de password completamente traducido (0 strings hardcoded)
- Temas dark/light actualizados (CSS, logos, favicons)
- Nuevo `robots.txt`

#### Refactor de interfaz

- `CallbackConnector`: eliminado `connData []byte` de `LoginURL` y `HandleCallback` (simplificación)
- `connector.go`: eliminado `UserNotInRequiredGroupsError` (movido a cada connector)
- Todos los connectors adaptados: bitbucketcloud, gitea, github, gitlab, google, linkedin, microsoft, mock, oidc, openshift

#### DevOps

- Workflow CI/CD: `.github/workflows/ghcr-publish.yaml`
    - Build + push automático a `ghcr.io/rasty94/dex` en cada push a `master`
    - Multi-arch: `linux/amd64` + `linux/arm64`
    - Tags automáticos: `latest`, `sha-<short>`, `vX.Y.Z`, `vX.Y`, `vX`
    - GitHub Actions Cache para builds más rápidos
- `.dockerignore` ampliado: excluye `dex_mod/`, `Ejemplos/`, `docs/`, tests, IDE, OS artifacts
- Imagen Docker: `ghcr.io/rasty94/dex:latest`

#### Tests

- `connector/keystone/key_test.go`: tests de generación de claves UUID/SHA1
- `connector/keystone/validate_test.go`: tests de validación de tokens

#### Documentación

- `documentacion/keystone_connector.md`: análisis completo con TOTP y permisos OpenStack
- `documentacion/Permisos base keystone.md`: referencia de políticas Keystone
- `documentacion/policy_modificado.yml`: política Keystone ajustada para Dex
- `documentacion/despliegue-docker-tls.md`: guía de despliegue con TLS

### 🔧 Cambiado

- `connector/keystone`: conector elevado de `alpha` a `beta` (estabilidad demostrada en producción)
- `go.mod`/`go.sum`: actualización de dependencias

### 🐛 Corregido

- Bug: `defer resp.Body.Close()` se llamaba antes de `io.ReadAll()` en `getUserGroups()`
- Encoding issue: ficheros de templates con CRLF normalizados

---

## Base upstream

Este fork está basado en [dexidp/dex](https://github.com/dexidp/dex) commit `2ecf64e8` (rama `master`, ~Feb 2026).
