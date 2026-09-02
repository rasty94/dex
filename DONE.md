# DONE — Dex Fork (rasty94/dex)

> Tareas completadas. Se separan de [TODO.md](TODO.md) para que allí solo quede
> trabajo vivo. La numeración de secciones es la histórica del TODO original, así
> que las referencias antiguas siguen resolviendo.
>
> Última actualización: 2026-09-01

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

---

## 🚀 Mejoras entregadas

### 14. 📊 Métrica y Telemetría

- [x] Métricas Prometheus del conector Keystone en `/metrics` (`connector/keystone/metrics.go`),
      registradas desde `cmd/dex` porque `ConnectorConfig.Open` no ve el registry.
    - Contadores: `keystone_login_attempts_total` (por paso y resultado), `keystone_refresh_total`,
      `keystone_token_validations_total` y `keystone_token_cache_lookups_total`.
    - Histograma: `keystone_token_validation_duration_seconds`, latencia de validación contra la API de Keystone.
    - También `dex_login_rate_limited_total`, para distinguir un límite que frena un ataque
      de uno que está dejando fuera a usuarios reales.

### 15. ✅ Mejoras en Keystone Connector

- [x] **Application Credentials**: Permitir autenticación mediante `application_credential_id` y `application_credential_secret` como método alternativo a contraseñas o TOTP.
- [x] Soporte de caché local para tokens de Keystone (reducir llamadas a `GET /v3/auth/tokens` mediante Redis o memoria en caché LRU con TTL adaptativo).
- [x] Mapeo dinámico de Grupos: Permitir mapear roles específicos de un proyecto (tenant) de OpenStack a grupos de Dex en lugar de devolver solo los grupos nativos del usuario.

### 16. ✅ Seguridad y DevSecOps

- [x] Escaneo de dependencias en cada release con Trivy o SonarQube.
- [x] Análisis estático de código de seguridad (SAST) usando Gosec en las Actions.
- [x] Auditoría de logs estructurados: Guardar eventos de auditoría (ej. IP del intento de login fallido).

### 17. ✅ UI / UX

- [x] **Tematización Dinámica por Cliente**: `frontend.clientThemes[<client_id>]` permite logo y `primaryColor` por cliente; si no hay `logoURL` en el tema se usa el `logoURL` del propio cliente en storage. El color se valida como hex al arrancar (se inyecta en un `<style>`) y sobrescribe `--primary-color`.
    - `server.Brand` lleva ahora `ReqPath`, `Tr`, `LogoURL` y `PrimaryColor` a todas las plantillas
- [x] Checkbox "Recordar este dispositivo" (MFA Trust) — `mfaTrust.enabled`
    - ⚠️ **No son 30 días reales**: Keystone impone el segundo factor y no tiene concepto de dispositivo de confianza, así que Dex no puede pedir solo la contraseña. En su lugar guarda el token emitido tras el TOTP en una cookie `HttpOnly`/`SameSite=Lax` y lo revalida con `TokenIdentity()` en el siguiente login.
    - La confianza dura lo que dure el token en Keystone (`[token] expiration`, por defecto 1h), no lo que diga `mfaTrust.duration`, y se corta al revocar el token
- [x] Botón de "Mostrar Contraseña" (ojo) en el input de password.

### 18. 🚀 Rendimiento y Alta Disponibilidad (HA)

- [x] Rate Limiting en Backend: `loginRateLimit` (`enabled`, `attempts`, `window`) limita los intentos **fallidos** por pareja IP + usuario antes de llamar a Keystone, tanto en el formulario de login como en el grant `password`. Un login correcto pone el contador a cero.
    - ⚠️ Los buckets viven en memoria del proceso: con N réplicas el límite efectivo es `attempts × N`. Migrarlo a Redis sigue pendiente, va de la mano con la caché distribuida.

### 21. 🛠️ API de Gestión gRPC (con Autenticación)

- [x] Extender la API gRPC existente para permitir cambios de configuración en tiempo real sin reiniciar.
- [x] Añadir capa de autenticación y autorización al servidor gRPC (interceptor/middlewares).
- [x] Definir los protobufs necesarios para administrar clientes y configuraciones.

### 22. ✅ CI — Ejecución de tests

- [x] `ci.yaml` ejecutaba lint + SonarCloud + build de Docker pero **ningún test**, así que los fallos pasaban desapercibidos. Añadido job `test` (`go test -race ./...`) del que ahora depende el job `docker`.
- [x] `TestHandlePassword`: el `// NOSONAR` de `dc6ebdcf` quedó dentro de un literal JSON, invalidando la config del conector mock. El conector no abría y el grant `password` respondía 400 en vez de 401 ante credenciales inválidas.
- [x] `TestVerifyUnsignedMessageAndSignedAssertionWithRootXmlNs`: `testdata/oam-ca.pem` caducó en junio de 2026 y no tenemos la clave privada para refirmar los fixtures. El reloj de validación se fija a 2020 para seguir verificando la firma.
    - ⏰ `testdata/idp-cert.pem` caduca en enero de 2027, pero ya queda cubierto por el mismo reloj fijo.

---

## 🔄 Sincronización con upstream — bloques cerrados

> Divergencia de partida: **313 commits de upstream / 52 nuestros** desde `99c4233`
> (último merge `5366eefa`, 2026-03-01). El bloque F sigue vivo en [TODO.md](TODO.md).

- [x] **Bloque A — Parche de seguridad inmediato.** Cinco fallos que upstream ya había arreglado: binding del auth code en el device callback (canje entre clientes), `client_secret` filtrado en el redirect del navegador, comparaciones de secreto a tiempo constante en refresh y device flow, y saneado del parámetro `back` (open redirect). Con test de inyección entre clientes.
- [x] **Bloque B — Dependencias de seguridad.** x/crypto, x/net, go-jose, goxmldsig, etree, go-ldap, go-sqlite3, grpc, go-oidc y mapstructure. Deliberadamente **sin** cel-go/webauthn/otp/gokrb5, que son features y no fixes.
- [x] **Bloque C — CI y build.** Quitado el `continue-on-error` del job de lint (el gate era decorativo), los tres pins de golangci-lint alineados en 2.13.1, e imágenes base al día.
- [x] **Bloque D — P1 y debilidades propias.** Doble decodificación de `redirect_uri`, nil-deref en `CreateConnector`, doble-submit de aprobación, y eliminación del campo oculto con la contraseña en el paso TOTP.
- [x] **Bloque E — El `sub` plano.** Decidido: cedemos al formato de upstream. El `sub` vuelve a ser el par `(userID, connectorID)` en protobuf-base64, y con él se va el escaneo de conectores de `api.go`, que solo existía para compensarlo. Es un cambio incompatible para quien indexe por `sub`; documentado en el `CHANGELOG`. A cambio quedamos alineados para heredar `sid`, back-channel logout y revocación con alcance de sesión en el bloque F.
- [x] **Bloque G — Devolver a upstream.** [dexidp/dex#4986](https://github.com/dexidp/dex/pull/4986) enviado: el fixture de `connector/oidc/oidc_test.go` publica `"RSA"` como `alg` y `kty`, lo que les romperá los tests en cuanto suban a go-oidc 3.20. El otro candidato (nuestro fix de token exchange, `21c99b5e`) resultó innecesario: el refactor de upstream a `server/grants/tokenexchange.go` ya no persiste refresh token ni offline session, así que el bug murió por el camino.

> Nota: es la última contribución al repo público. A partir de aquí los arreglos que
> encontremos en upstream se quedan en el fork.

### 🧹 Deuda técnica cerrada

- [x] ~~**Coste de resolver el `sub` plano**~~: resuelto de raíz en el bloque E. Al volver al `sub` de upstream el connector id viaja dentro del subject, así que `ListRefresh`/`RevokeRefresh` lo decodifican en lugar de recorrer los conectores.
- [x] ~~`newAuthInterceptor` comparaba el token de administración con `!=`~~: es un secreto compartido validado antes de autenticar nada, ahora usa `subtle.ConstantTimeCompare`.

---

### 26. 🎛️ Dashboard de administración

Panel de administración en `cmd/dex-dashboard/`, binario aparte que habla gRPC con Dex.
Cómo funciona: [documentacion/dashboard-administracion.md](documentacion/dashboard-administracion.md).
Ejemplo listo para levantar: `Ejemplos/dashboard/`.

- [x] **Fase 1 — Solo lectura.** Login OIDC contra el propio Dex con gate por grupo o email de
      rescate. Vistas de clientes, conectores, usuarios locales, sesiones y versión. El token de
      administración se queda en el servidor y nunca llega al navegador; las sesiones son
      opacas y viven en memoria.
- [x] **Fase 2 — Escritura con dos niveles.** `admin.groups` da entrada, `admin.writeGroups`
      habilita los cambios, y con el segundo vacío el panel es de solo lectura para todos.
      Revocación de refresh tokens, CRUD de clientes OAuth2 y de usuarios locales. Lo
      destructivo pasa por una página de confirmación que explica qué se rompe. Las contraseñas
      se hashean con bcrypt en el panel, así que el texto plano no llega a la API de Dex.
- [x] **Auditoría con nombre.** La API gRPC autentica un token compartido, no a una persona, así
      que su log solo podía decir «lo hizo el token». El panel manda `x-dex-actor` y Dex la
      registra en cada operación que muta. No es identidad verificada por Dex: es un cliente ya
      de plena confianza atestiguando quién pidió qué.
- [x] **Fase 3 — Conectores.** Alta, edición y baja, más `ReloadConfig`. Los secretos se
      muestran como `__unchanged__` y se restauran al guardar, de modo que editar un campo no
      borra una contraseña. La configuración se valida contra la estructura real del tipo de
      conector, porque Dex solo comprueba que el JSON parsea y una config equivocada rompe
      todos los logins de ese conector.
- [x] **Fase 4 — Operación.** Vista de Status con la salud de Dex, tráfico y errores por
      endpoint, logins frenados por el rate limiter y contadores de Keystone, leídos del
      endpoint de telemetría desde el servidor.
- [x] **Búsqueda de sesiones sin pegar el `sub`.** Acepta también usuario + conector y construye
      el subject con `server.EncodeSubject`, exportado para eso.
- [x] **Endurecimiento de sesión.** Caducidad por inactividad además de la absoluta,
      re-autenticación obligatoria para lo destructivo (`prompt=login`, con vuelta a donde
      ibas), prefijo `__Host-` en las cookies bajo HTTPS y límite de intentos en el login del
      panel, que hasta entonces no tenía freno.
- [x] **Esqueletos de conector por tipo**, sacados por reflexión del struct real y en orden de
      struct, para que lo esencial salga primero.
- [x] **Revocar todas las sesiones de un usuario** de una vez, que es el botón de un
      offboarding o un portátil perdido.
- [x] **Exportar la configuración** a YAML (clientes y conectores) para copia de seguridad,
      con confirmación y auditoría porque el fichero lleva credenciales vivas.
- [x] **Vista de Discovery**, lo que hay que entregar a quien integra una aplicación.
- [x] **Filtro por texto** en clientes, conectores y usuarios, distinguiendo una lista vacía de
      una lista filtrada.
- [x] **Docker.** El binario viaja en la misma imagen que Dex y corre como contenedor aparte;
      su configuración se renderiza con gomplate como la de Dex.

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
| Tematización por `client_id`                |   ✅   | `frontend.clientThemes`, logo + color     |
| MFA Trust (dispositivo de confianza)        |   ✅   | `mfaTrust`, limitado al TTL de Keystone   |
| Tests en CI                                 |   ✅   | Job `test` (`go test -race ./...`)        |
| Rate limiting de login                      |   ✅   | `loginRateLimit`, en memoria por réplica  |
| Fixes de seguridad del bloque A             |   ✅   | device callback, timing, open redirect    |
| Dependencias criptográficas al día          |   ✅   | x/crypto, x/net, go-jose, SAML            |
| Gate de lint real en CI                     |   ✅   | fuera el `continue-on-error`              |
| `sub` en formato de upstream                |   ✅   | ⚠️ incompatible, ver `CHANGELOG`          |
| Contraseña fuera del paso TOTP              |   ✅   | el receipt basta, campo oculto eliminado  |
| Re-port sobre upstream                      |   🚧   | keystone y rate limiter, ver `TODO.md`    |
| Panel de administración                     |   ✅   | `cmd/dex-dashboard`, 4 fases entregadas   |
| Auditoría con nombre en la API gRPC         |   ✅   | cabecera `x-dex-actor` registrada por dex |
