# TODO — Dex Fork (rasty94/dex)

> Última actualización: 2026-02-25
> Imagen Docker: `ghcr.io/rasty94/dex:latest`

---

## 🔴 Prioridad Alta — Integración de mejoras de `dex_mod`

Las siguientes mejoras están implementadas en la carpeta `dex_mod/` pero **aún no se han integrado** en el código fuente principal. Son las pendientes más críticas.

### 1. ✅ Integrar mejoras del Keystone Connector (TOTP / MFA)

- [ ] Añadir soporte para **TOTP (Multi-Factor Authentication)** en `connector/keystone/keystone.go`
    - Nuevos tipos: `totp`, `userTOTP`, `ErrTOTPRequired`
    - Context keys: `TOTPContextKey`, `ReceiptContextKey`
    - TOTP como segundo método de autenticación en `getTokenResponse()`
    - Gestión del header `openstack-auth-receipt` para flujo MFA en dos pasos
- [ ] Añadir soporte para **multi-dominio dinámico** (dominio por usuario vs. dominio global)
- [ ] Añadir **`TokenIdentity()`** — nueva función para validar tokens existentes de Keystone (self-validation vía `GET /v3/auth/tokens`)
- [ ] Mejorar manejo de errores en `getAdminToken()` (verificación de status code, body logging)
- [ ] Corregir bug: `defer resp.Body.Close()` antes de `io.ReadAll()` en `getUserGroups()`
- [ ] Añadir campo `UserIDKey` a `Config` para permitir usar `email` o `username` como ID (con UUID derivado via SHA1)
- [ ] Copiar tests nuevos: `key_test.go` y `validate_test.go` desde `dex_mod/connector/keystone/`

### 2. ✅ Integrar i18n en el backend (templates.go)

- [ ] Modificar las funciones de renderizado en `server/templates.go` para inyectar traducciones (`GetTranslations`) via `Accept-Language` header
    - `device()` → añadir campo `Tr`
    - `deviceSuccess()` → añadir campo `Tr`
    - `login()` → añadir campo `Tr`
    - `password()` → añadir campos `Tr`, `ShowDomain`, `Domain`, `RequireTOTP`, `Receipt`, `Password`
    - `approval()` → añadir campo `Tr`
    - `oob()` → añadir campo `Tr`
    - `err()` → añadir campo `Tr`
- [ ] Verificar que `server/i18n.go` (ya copiado) está correctamente importado y utilizado

### 3. ✅ Adaptar `server/handlers.go` para TOTP

- [x] Modificar `handlePasswordLogin()` para detectar `ErrTOTPRequired` y re-renderizar el formulario en modo TOTP
- [x] Pasar los nuevos campos (`showDomain`, `domain`, `requireTOTP`, `receipt`, `lastPassword`) a `templates.password()`
- [x] Implementar lectura del campo `totp` y `receipt` del formulario POST y pasarlos via context al connector

---

## 🟠 Prioridad Media — CI/CD y DevOps

### 4. Configurar CI/CD para publicar a GHCR automáticamente

- [ ] Crear workflow `.github/workflows/ghcr-publish.yaml` que construya y publique `ghcr.io/rasty94/dex` en cada push a `master`
- [ ] Usar `docker/build-push-action` con login a GHCR via `GITHUB_TOKEN`
- [ ] Añadir tags de versión (`latest`, `vX.Y.Z`, commit SHA)
- [ ] Opcional: build multi-arquitectura (`linux/amd64`, `linux/arm64`)

### 5. Optimizar `.dockerignore`

- [ ] Añadir a `.dockerignore`:
    ```
    dex_mod/
    Ejemplos/
    documentacion/
    .github/
    .git/
    docs/
    *.md
    ```
    Para reducir el contexto de build y acelerar las construcciones Docker.

### 6. Configurar Dependabot / Renovate para Go modules

- [ ] Verificar que `dependabot.yaml` está configurado para este fork
- [ ] Asegurar actualizaciones automáticas de dependencias Go y GitHub Actions

---

## 🟡 Prioridad Media — UI / Frontend

### 7. Pulir las plantillas HTML

- [ ] Revisar que todas las plantillas usan claves de traducción `{{ .Tr.xxx }}` de forma consistente
- [ ] ~~Hardcoded strings~~ en `password.html`: traducir `"TOTP / App Code"`, `"Verify"`, `"Invalid TOTP code."`, `"Invalid credentials."`, `"Signing in..."`
- [ ] Añadir iconos SVG para cada tipo de connector en `login.html` (actualmente tiene placeholder comment `<!-- GitHub Icon could go here -->`)
- [ ] Responsive: verificar que los temas `dark` y `light` se ven correctamente en móvil

### 8. Añadir más idiomas al sistema i18n

- [ ] Añadir traducciones para: `fr` (francés), `pt` (portugués), `de` (alemán)
- [ ] Mover traducciones a archivos YAML/JSON externos en lugar de hardcodearlas en `server/i18n.go`
- [ ] Permitir configurar el idioma por defecto via `config.yaml`

### 9. Eliminar archivos obsoletos de themes

- [ ] Borrar `faviconOLD.png` y `logoOLD.png` de `web/themes/dark/` y `web/themes/light/`
- [ ] Borrar `web/themes/light/.!3520!faviconOLD.png` (archivo corrupto/residual)

---

## 🟢 Prioridad Baja — Calidad de Código y Testing

### 10. Testing del conector Keystone mejorado

- [ ] Escribir tests unitarios para:
    - `Login()` con TOTP habilitado
    - `Login()` con multi-dominio
    - `TokenIdentity()` (validación de token existente)
    - `getTokenResponse()` con receipts
- [ ] Integrar `key_test.go` y `validate_test.go` de `dex_mod`
- [ ] Añadir mocking del endpoint Keystone para tests sin dependencia externa

### 11. Seguridad

- [ ] Eliminar credenciales hardcodeadas en `Ejemplos/config.yaml` (password de admin está en claro)
    - Usar variables de entorno o archivos secretos
- [ ] Añadir headers de seguridad por defecto en `config.docker.yaml`:
    ```yaml
    headers:
        X-Frame-Options: "DENY"
        X-Content-Type-Options: "nosniff"
        X-XSS-Protection: "1; mode=block"
        Content-Security-Policy: "default-src 'self'"
        Strict-Transport-Security: "max-age=31536000; includeSubDomains"
    ```
- [ ] Verificar que los tokens de Keystone se manejan de forma segura en logs (no loguear tokens completos)

### 12. Limpieza general del repositorio

- [ ] Decidir si `dex_mod/` se elimina tras integrar todas las mejoras
- [ ] Eliminar archivos `test_output*.txt` residuales dentro de `dex_mod/`
- [ ] Evaluar si `Ejemplos-Oasix/` (presente en dex_mod) es necesario
- [ ] Actualizar `README.md` para reflejar:
    - La nueva imagen Docker (`ghcr.io/rasty94/dex`)
    - Las mejoras del connector Keystone (TOTP, multi-dominio, token validation)
    - El soporte de internacionalización (i18n)
    - El estado del connector Keystone como "beta" en la tabla de connectores

### 13. Documentación

- [ ] Documentar la configuración del TOTP/MFA para Keystone en `documentacion/`
- [ ] Crear guía de despliegue con docker-compose incluyendo certificados TLS
- [ ] Documentar los permisos OpenStack necesarios (ampliar `keystone_connector.md` con TOTP)
- [ ] Añadir CHANGELOG.md para trackear versiones del fork

---

## 📋 Resumen de Estado

| Área                        | Estado | Notas                                  |
| --------------------------- | ------ | -------------------------------------- |
| `.gitignore` actualizado    | ✅     | `dex_mod` ignorado                     |
| Imagen Docker GHCR          | ✅     | `ghcr.io/rasty94/dex:latest` publicada |
| Templates HTML (UI)         | ✅     | Copiados desde `dex_mod`               |
| CSS y themes                | ✅     | Estilos dark/light actualizados        |
| `server/i18n.go`            | ✅     | Archivo copiado (EN + ES)              |
| i18n wiring en templates.go | ❌     | Pendiente de integrar                  |
| Keystone TOTP/MFA           | ❌     | Pendiente de integrar                  |
| Keystone TokenIdentity      | ❌     | Pendiente de integrar                  |
| CI/CD GHCR automático       | ❌     | Pendiente                              |
| Tests Keystone              | ❌     | Pendiente                              |
