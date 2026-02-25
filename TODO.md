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

## 🟠 Pendiente — Prioridad Media

### 7. CI/CD — Publicación automática a GHCR

- [ ] Crear workflow `.github/workflows/ghcr-publish.yaml` que construya y publique en cada push a `master`
- [ ] Usar `docker/build-push-action` con `GITHUB_TOKEN` para login a GHCR
- [ ] Tags automáticos: `latest`, SHA corto del commit, y `vX.Y.Z` en releases
- [ ] Opcional: build multi-arquitectura (`linux/amd64`, `linux/arm64`)

### 8. UI — Pulir plantillas HTML

- [ ] Traducir strings hardcoded en `password.html`: `"TOTP / App Code"`, `"Verify"`, `"Invalid TOTP code."`, `"Invalid credentials."`, `"Signing in..."`
- [ ] Añadir iconos SVG reales para los connectors en `login.html` (actualmente solo texto)
- [ ] Verificar diseño responsive en móvil para ambos temas

### 9. i18n — Ampliar idiomas

- [ ] Añadir traducciones para: `fr`, `pt`, `de`
- [ ] Evaluar externalizar traducciones a YAML/JSON en lugar de Go hardcodeado

---

## 🟢 Pendiente — Prioridad Baja

### 10. Seguridad

- [ ] Eliminar credenciales hardcodeadas en `Ejemplos/config.yaml`
- [ ] Añadir headers de seguridad por defecto en `config.docker.yaml`:
    ```yaml
    headers:
        X-Frame-Options: "DENY"
        X-Content-Type-Options: "nosniff"
        Content-Security-Policy: "default-src 'self'"
        Strict-Transport-Security: "max-age=31536000; includeSubDomains"
    ```
- [ ] Verificar que los tokens no se loguean completos

### 11. Testing

- [ ] Tests unitarios para flujo TOTP completo (mock del endpoint Keystone)
- [ ] Tests de `TokenIdentity()` con mocks

### 12. Documentación

- [ ] Ampliar `keystone_connector.md` con la configuración de TOTP/MFA
- [ ] Guía de despliegue con docker-compose + certificados TLS
- [ ] Añadir `CHANGELOG.md` para versiones del fork
- [ ] Actualizar `README.md`:
    - Imagen Docker: `ghcr.io/rasty94/dex`
    - Nuevas funcionalidades: TOTP, i18n, TokenIdentity
    - Elevar el conector Keystone de `alpha` a `beta`

### 13. Limpieza

- [ ] Eliminar `dex_mod/` cuando ya no sea necesario como referencia
- [ ] Revisar si `Ejemplos-Oasix/` puede eliminarse

---

## 📋 Resumen de Estado

| Área                                    | Estado | Notas                                  |
| --------------------------------------- | :----: | -------------------------------------- |
| `.gitignore`                            |   ✅   | `dex_mod` ignorado                     |
| `.dockerignore`                         |   ✅   | Excluye artefactos innecesarios        |
| Imagen Docker GHCR                      |   ✅   | `ghcr.io/rasty94/dex:latest` publicada |
| Templates HTML (UI)                     |   ✅   | Actualizados con TOTP y i18n           |
| CSS y themes                            |   ✅   | Estilos dark/light limpios             |
| i18n (EN + ES)                          |   ✅   | `server/i18n.go`, wired en templates   |
| Keystone TOTP/MFA                       |   ✅   | `ErrTOTPRequired`, flujo 2 pasos       |
| Keystone `TokenIdentity`                |   ✅   | Self-validation de tokens              |
| Keystone `UserIDKey` (email/username)   |   ✅   | UUID SHA1 derivado                     |
| Refactor `CallbackConnector`            |   ✅   | Todos los connectors actualizados      |
| Tests Keystone (`key_test`, `validate`) |   ✅   | Añadidos                               |
| Documentación Keystone/permisos         |   ✅   | En `documentacion/`                    |
| Dependabot                              |   ✅   | Go + Docker + Actions configurado      |
| CI/CD automático GHCR                   |   ❌   | Pendiente                              |
| Strings TOTP hardcoded en templates     |   ❌   | Pendiente traducción                   |
| Tests TOTP unitarios con mocks          |   ❌   | Pendiente                              |
| CHANGELOG.md                            |   ❌   | Pendiente                              |
| README.md actualizado                   |   ❌   | Pendiente                              |
