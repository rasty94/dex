# CHANGELOG — Dex Fork (rasty94/dex)

Versiones de este fork sobre la base de [dexidp/dex](https://github.com/dexidp/dex).

El formato sigue [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

---

## [Unreleased]

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
