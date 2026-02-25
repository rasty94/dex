# TODO — Dex Fork (rasty94/dex)

> Última actualización: 2026-02-25
> Imagen Docker: `ghcr.io/rasty94/dex:latest`
> Repositorio: https://github.com/rasty94/dex

---

## ✅ Completado — Alta Prioridad

### 1. ✅ Keystone Connector — TOTP/MFA y mejoras

- [x] Soporte completo de **TOTP (Multi-Factor Authentication)**: `ErrTOTPRequired`, `TOTPContextKey`, `ReceiptContextKey`
- [x] Flujo MFA en dos pasos: detección de `openstack-auth-receipt`, re-renderizado del formulario con campo TOTP
- [x] **Multi-dominio dinámico**: el dominio puede venir del formulario o estar fijo en config
- [x] **`TokenIdentity()`**: validación de tokens Keystone existentes vía `GET /v3/auth/tokens`
- [x] **`UserIDKey`**: permite usar `email` o `username` como identificador (UUID SHA1)
- [x] Manejo mejorado de errores en `getAdminToken()` y `getTokenResponse()`
- [x] Corrección de bug: `defer resp.Body.Close()` reordenado correctamente
- [x] Tests añadidos: `key_test.go` y `validate_test.go`

### 2. ✅ i18n — Sistema de internacionalización

- [x] `server/i18n.go`: mapa de traducciones EN/ES con fallback automático
- [x] `server/templates.go`: todos los renders inyectan `Tr` via `Accept-Language`
- [x] `web/templates/`: plantillas HTML actualizadas para usar `{{ .Tr.xxx }}`
- [x] `web/templates/password.html`: campos TOTP, dominio, receipt, backlick integrados

### 3. ✅ Refactor interfaz `CallbackConnector`

- [x] Eliminado parámetro `connData []byte` de `LoginURL` y `HandleCallback`
- [x] Eliminado `UserNotInRequiredGroupsError` de `connector/connector.go`
- [x] Todos los connectors adaptados a la nueva interfaz:
    - bitbucketcloud, gitea, github, gitlab, google, linkedin, microsoft, mock, oidc, openshift

### 4. ✅ UI / Themes

- [x] CSS actualizado para temas `dark` y `light`
- [x] Nuevo `robots.txt`
- [x] SVG icons actualizados en `web/static/img/`
- [x] Archivos `*OLD.png` eliminados de los themes

### 5. ✅ Docker y distribución

- [x] Imagen publicada en `ghcr.io/rasty94/dex:latest`
- [x] `.dockerignore` ampliado para excluir artefactos innecesarios
- [x] `Ejemplos/docker-compose.yml` apunta a la imagen GHCR

### 6. ✅ Documentación y repositorio

- [x] `documentacion/keystone_connector.md`: análisis de permisos OpenStack
- [x] `documentacion/Permisos base keystone.md`: referencia completa de políticas
- [x] `documentacion/policy_modificado.yml`: política Keystone ajustada para Dex
- [x] `.gitignore` actualizado con `dex_mod`
- [x] `Dependabot` ya configurado para Go, Docker y GitHub Actions

---

## 🟠 Completado — Prioridad Media

### 7. ✅ CI/CD — Publicación automática a GHCR

- [x] Workflow `.github/workflows/ghcr-publish.yaml` creado
- [x] Login a GHCR con `GITHUB_TOKEN` nativo (sin secretos extra)
- [x] Tags automáticos: `latest`, `sha-<short>`, `vX.Y.Z`, `vX.Y`, `vX`
- [x] Build multi-arquitectura: `linux/amd64` + `linux/arm64`
- [x] Caché de build con GitHub Actions Cache

### 8. ✅ UI — Pulir plantillas HTML

- [x] Todos los strings hardcoded en `password.html` sustituidos por claves i18n
      (`totp_label`, `totp_verify_button`, `totp_invalid`, `invalid_credentials`, `signing_in`, `domain_label`)
- [x] Iconos SVG añadidos en `login.html` para todos los connectors:
      github, gitlab, google, microsoft, linkedin, bitbucket, gitea, ldap, keystone, saml, oidc, atlassiancrowd
- [x] Verificar diseño responsive en móvil para ambos temas
    - `main.css` reescrito mobile-first: `clamp()`, media queries, `100dvh`, tamaños táctiles correctos
    - `dark/styles.css` y `light/styles.css`: tokens via CSS variables, iconos SVG de connectors
    - `header.html`: `viewport-fit=cover`, `theme-color` dual (claro/oscuro), `lang`, SEO meta

### 9. ✅ i18n — Ampliar idiomas

- [x] Añadidos 3 idiomas: `fr` (francés), `de` (alemán), `pt` (portugués)
- [x] Nuevas claves TOTP (`totp_label`, `totp_verify_button`, `totp_invalid`, `signing_in`) en los 5 idiomas
- [x] `domain_label` añadida en todos los idiomas
- [x] Mejorado parsing de `Accept-Language` (soporta cabeceras completas: `es-ES,es;q=0.9,en;q=0.8`)
- [x] Evaluar externalizar traducciones a YAML/JSON en lugar de Go hardcodeado
    - Implementado: `server/i18n/*.yaml` embebidos con `//go:embed`
    - Añadir idioma = soltar un `.yaml` y recompilar, sin tocar Go

---

### 10. ✅ Seguridad

- [x] Eliminar credenciales hardcodeadas en `Ejemplos/config.yaml`
- [x] Añadir headers de seguridad por defecto en `config.docker.yaml`:
    ```yaml
    headers:
        X-Frame-Options: "DENY"
        X-Content-Type-Options: "nosniff"
        Content-Security-Policy: "default-src 'self'"
        Strict-Transport-Security: "max-age=31536000; includeSubDomains"
    ```
- [x] Verificar que los tokens no se loguean completos (Keystone logs omiten secretos)

### 11. ✅ Testing

- [x] Tests unitarios para flujo TOTP completo (mock del endpoint Keystone) en `keystone_test.go`
- [x] Tests de `TokenIdentity()` con mocks en `keystone_test.go`
- [x] Arreglado mismatch de asignación en `introspectionhandler_test.go`
- [x] Sincronizado `go.mod` y `go.sum` tras implementar YAML i18n
- [x] Añadida validación de URL en `redirectedAuthErr.Handler()`

### 12. ✅ Documentación

- [x] Ampliar `keystone_connector.md` con flujo TOTP/MFA, `TokenIdentity`, `UserIDKey`, `showDomain` y guia de permisos OpenStack
- [x] Guía de despliegue en `documentacion/despliegue-docker-tls.md`: TLS (autofirmado, CA, Let's Encrypt), docker-compose con healthcheck, config.yaml anotada, troubleshooting
- [x] `CHANGELOG.md` creado con historial completo desde el upstream
- [x] `README.md` actualizado:
    - Badges del fork (CI + imagen Docker GHCR)
    - Sección "Fork Enhancements": TOTP/MFA, TokenIdentity, i18n, imagen Docker
    - Keystone elevado de `alpha` a `beta` con notas de funcionalidades

- [x] Eliminar `dex_mod/` (Completado)
- [x] Eliminar `Ejemplos-Oasix/` (Completado)

---

## 🚀 Futuras Mejoras (Propuestas)

### 14. 📊 Métrica y Telemetría

- [ ] Exportar métricas en `/metrics` (Prometheus) específicas del conector Keystone.
    - Contadores: `keystone_totp_success`, `keystone_totp_failures`, `keystone_login_success`.
    - Histogramas: Latencia de validación de tokens contra la API de Keystone.
- [ ] Trazabilidad distribuida (OpenTelemetry) para peticiones hacia OpenStack.

### 15. ✅ Mejoras en Keystone Connector

- [x] **Application Credentials**: Permitir autenticación mediante `application_credential_id` y `application_credential_secret` como método alternativo a contraseñas o TOTP.
- [x] Soporte de caché local para tokens de Keystone (reducir llamadas a `GET /v3/auth/tokens` mediante Redis o memoria en caché LRU con TTL adaptativo).
- [x] Mapeo dinámico de Grupos: Permitir mapear roles específicos de un proyecto (tenant) de OpenStack a grupos de Dex en lugar de devolver solo los grupos nativos del usuario.

### 16. 🛡️ Seguridad y DevSecOps

- [ ] Escaneo de dependencias en cada release con Trivy o SonarQube.
- [ ] Análisis estático de código de seguridad (SAST) usando Gosec en las Actions.
- [ ] Auditoría de logs estructurados: Guardar eventos de auditoría (ej. IP del intento de login fallido).

### 17. 🎨 UI / UX

- [ ] **Tematización Dinámica por Cliente**: Permitir que un `client_id` inyecte su propio Logo o color principal en la pantalla de login (Feature nativa de Dex pero mejorable en las plantillas).
- [ ] Añadir un checkbox de "Recordar este dispositivo durante 30 días" (MFA Trust) para evitar pedir el TOTP todos los días en IPs conocidas.
- [ ] Botón de "Mostrar Contraseña" (ojo) en el input de password.

---

## 📋 Resumen de Estado

| Área                                        | Estado | Notas                                     |
| ------------------------------------------- | :----: | ----------------------------------------- |
| `.gitignore`                                |   ✅   | -                                         |
| `.dockerignore`                             |   ✅   | Limpiado (dex_mod, Oasix eliminados)      |
| Imagen Docker GHCR                          |   ✅   | `ghcr.io/rasty94/dex:latest` publicada    |
| Templates HTML (UI)                         |   ✅   | Actualizados con TOTP y i18n              |
| CSS y themes                                |   ✅   | Estilos dark/light limpios                |
| i18n (EN + ES)                              |   ✅   | `server/i18n.go`, wired en templates      |
| Keystone TOTP/MFA                           |   ✅   | `ErrTOTPRequired`, flujo 2 pasos          |
| Keystone `TokenIdentity`                    |   ✅   | Self-validation de tokens                 |
| Keystone `UserIDKey` (email/username)       |   ✅   | UUID SHA1 derivado                        |
| Refactor `CallbackConnector`                |   ✅   | Todos los connectors actualizados         |
| Tests Keystone (`key_test`, `validate`)     |   ✅   | Añadidos                                  |
| Documentación Keystone/permisos             |   ✅   | En `documentacion/`                       |
| Dependabot                                  |   ✅   | Go + Docker + Actions configurado         |
| CI/CD automático GHCR (`ghcr-publish.yaml`) |   ✅   | multi-arch amd64+arm64, semver + SHA tags |
| Strings TOTP hardcoded en templates         |   ✅   | Traducidos vía i18n (5 idiomas)           |
| i18n ampliado (FR, DE, PT)                  |   ✅   | 5 idiomas: EN, ES, FR, DE, PT             |
| Iconos SVG en `login.html`                  |   ✅   | 12 connectors con icono SVG               |
| Tests TOTP unitarios con mocks              |   ✅   | En `keystone_test.go`                     |
| CHANGELOG.md                                |   ✅   | Historial completo desde upstream         |
| README.md actualizado                       |   ✅   | Fork badges, mejoras, Keystone beta       |
| `keystone_connector.md` ampliado            |   ✅   | TOTP, TokenIdentity, permisos, comandos   |
| Guía despliegue TLS                         |   ✅   | `despliegue-docker-tls.md`                |
