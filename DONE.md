# DONE — Dex Fork (rasty94/dex)

> Tareas completadas. Se separan de [TODO.md](TODO.md) para que allí solo quede
> trabajo vivo. La numeración de secciones es la histórica del TODO original, así
> que las referencias antiguas siguen resolviendo.
>
> Última actualización: 2026-09-03

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
> (último merge `5366eefa`, 2026-03-01). **Todos los bloques cerrados**, F incluido: el
> re-port es ya la línea principal del fork.

- [x] **Bloque A — Parche de seguridad inmediato.** Cinco fallos que upstream ya había arreglado: binding del auth code en el device callback (canje entre clientes), `client_secret` filtrado en el redirect del navegador, comparaciones de secreto a tiempo constante en refresh y device flow, y saneado del parámetro `back` (open redirect). Con test de inyección entre clientes.
- [x] **Bloque B — Dependencias de seguridad.** x/crypto, x/net, go-jose, goxmldsig, etree, go-ldap, go-sqlite3, grpc, go-oidc y mapstructure. Deliberadamente **sin** cel-go/webauthn/otp/gokrb5, que son features y no fixes.
- [x] **Bloque C — CI y build.** Quitado el `continue-on-error` del job de lint (el gate era decorativo), los tres pins de golangci-lint alineados en 2.13.1, e imágenes base al día.
- [x] **Bloque D — P1 y debilidades propias.** Doble decodificación de `redirect_uri`, nil-deref en `CreateConnector`, doble-submit de aprobación, y eliminación del campo oculto con la contraseña en el paso TOTP.
- [x] **Bloque E — El `sub` plano.** Decidido: cedemos al formato de upstream. El `sub` vuelve a ser el par `(userID, connectorID)` en protobuf-base64, y con él se va el escaneo de conectores de `api.go`, que solo existía para compensarlo. Es un cambio incompatible para quien indexe por `sub`; documentado en el `CHANGELOG`. A cambio quedamos alineados para heredar `sid`, back-channel logout y revocación con alcance de sesión en el bloque F.
- [x] **Bloque G — Devolver a upstream.** [dexidp/dex#4986](https://github.com/dexidp/dex/pull/4986) enviado: el fixture de `connector/oidc/oidc_test.go` publica `"RSA"` como `alg` y `kty`, lo que les romperá los tests en cuanto suban a go-oidc 3.20. El otro candidato (nuestro fix de token exchange, `21c99b5e`) resultó innecesario: el refactor de upstream a `server/grants/tokenexchange.go` ya no persiste refresh token ni offline session, así que el bug murió por el camino.

> Nota: es la última contribución al repo público. A partir de aquí los arreglos que
> encontremos en upstream se quedan en el fork.

### Bloque F — El re-port sobre el layout nuevo de upstream

> El más largo de todos, y el único que no se podía hacer con un `git merge`. Terminó
> siendo la línea principal del fork: `master` es hoy este re-port, y el anterior queda
> respaldado en la rama `master-pre-upstream-sync` y en la etiqueta
> `pre-upstream-sync-2026-09-02`, ambas en `origin`.

- [x] **Re-port sobre `upstream/master`.** Se hizo en la rama
      `feat/upstream-sync-2026-08`, partiendo de `upstream/master` y en un worktree
      aparte para no mezclarla con la línea viva. Rebanada a rebanada:
      - [x] `connector/keystone/` completo, más el registro de sus colectores en `cmd/dex`.
            Entró **sin una sola edición**: compila contra el layout nuevo tal cual. Confirma la
            estimación de coste cero para esta pieza.
      - [x] Rate limiting de login, ahora en un paquete propio `server/ratelimit` compartido
            por el flujo interactivo y el grant de password, con test de extremo a extremo del
            throttling en el grant. Dos mejoras sobre el original: la IP se lee del resolutor
            de upstream (nuestro `clientIP` se fiaba del primer `X-Forwarded-For`, spoofeable),
            y los valores por defecto viven dentro de `ratelimit.New`. El router adjunta ahora
            siempre una IP al contexto, si no todos los clientes compartirían bucket.
      - [x] **i18n** → `server/templates/`. Las traducciones se aplican al marcado de
            upstream en vez de sustituirlo por el de master, así se conservan su tema,
            el `remember_me` y las páginas que el fork no tenía (logout, home,
            totp_verify). El juego de claves se rehace: donde el fork concatenaba en la
            plantilla ahora hay cadenas con marcador y `printf`, porque concatenar
            funcionaba en inglés y español por casualidad y se rompe en cuanto un idioma
            cambia el orden. 54 claves × 5 idiomas, con respaldo al inglés clave a clave.
      - [x] **Traducir `webauthn_verify.html`.** El mecanismo ya estaba en el propio
            fichero (`const mode = {{ .Mode }}`): `html/template` trata ese bloque como
            contexto JavaScript, así que un apóstrofo sale como apóstrofo dentro de
            comillas dobles en vez de como `&#39;`. 66 claves en total.
      - [x] **Theming por cliente** (`LogoURL`, `PrimaryColor` por `client_id`) →
            `server/templates/`. El `client_id` viaja por contexto y solo lo marcan los
            tres sitios que renderizan una página de un cliente concreto; reproducir el
            `Brand` posicional de master habría obligado a cambiar las doce firmas para
            que nueve pasaran un cliente vacío. El paquete de plantillas no depende de
            storage: el respaldo al `logoURL` del cliente entra como función estrecha.
            `primaryColor` se valida al cargar la config, porque se interpola en un
            `<style>` y CSS no es un contexto que `html/template` escape.
      - [x] Trusted device sobre la maquinaria de cookies de upstream. El token de
            Keystone ya no viaja en claro: va sellado con AES-GCM, reutilizando las
            primitivas de la cookie de sesión de upstream (exportadas, para no escribir
            un segundo cifrado). **Cambio incompatible**: con `mfaTrust` activado y sin
            `encryptionKey`, dex se niega a arrancar. La clave no puede salir de
            `sessions.cookieEncryptionKey` porque esa config vive tras el feature flag
            de sesiones, y los dispositivos de confianza son independientes de él.
      - [x] Flujo TOTP en el servidor (`ErrTOTPRequired`) → `server/authflow/`. El
            `server/mfa` de upstream **no sirve** para esto: su MFA es posterior a la
            identidad (corre tras `finalizeLogin`, con un usuario de dex y el secreto TOTP
            guardado por dex), mientras que el nuestro pasa dentro del intercambio de
            credenciales, sin identidad todavía y sin que dex vea nunca el secreto. Son
            ortogonales, así que el flujo Keystone se queda en `handlePasswordLogin` y el
            de upstream sigue intacto.
            El estado propio de la pantalla viaja en un `templates.PasswordForm`, porque
            juntar las dos firmas daba trece parámetros posicionales. El bloque TOTP entra
            con el marcado de upstream y en inglés: portar la plantilla de master habría
            revertido su tema nuevo y perdido `remember_me`.
      - [x] `.proto` y config dinámica de gRPC. Nuestra única extensión del `.proto`
            resultó ser `ReloadConfig`; el resto de la rebanada fueron los actualizadores
            de `storage/static.go` (upstream devuelve el envoltorio por valor y sin forma
            de cambiarlo) y la autenticación por token, que upstream no tiene en absoluto
            — se apoya solo en mTLS. Dos mejoras sobre el original: `mutatingPrefixes`
            suma `Terminate` y `Reset`, métodos nuevos de upstream que destruyen sesiones
            y segundos factores y habrían pasado sin auditoría; y tanto los actualizadores
            como los interceptores tienen ya test, que no tenían en `master`.
      - [x] **Portar el tooling del fork**: `.pre-commit-config.yaml`, el objetivo del
            `Makefile` que compila los tres binarios y `sonar-project.properties`.
      - [x] **Portar la documentación del fork**: `CHANGELOG.md`, `TODO.md`, `DONE.md`,
            `SECURITY_FIXES.md` y `documentacion/`. **No** se restauran `.envrc`,
            `.gitpod.yml`, `flake.nix`, `flake.lock` ni `MAINTAINERS`: son ficheros de un
            upstream antiguo que el fork heredó y que upstream ya sustituyó por `devenv`.
            Tampoco `fix_signatures.py`, un script de migración de un solo uso ya
            consumido — conviene borrarlo también de `master`.
      - [x] **Hecho el cambio: la rama es ya `master`.** Antes, un repaso de extremo a
            extremo sobre la imagen construida desde la rama: login completo hasta el
            panel, las siete vistas respondiendo, Conectores listando de verdad, Estado
            leyendo la telemetría real de dex, y un POST destructivo sin token CSRF
            rechazado con 403. Destapó una regresión, ya corregida: la etiqueta de
            usuario salía en inglés por haber adoptado el marcado de upstream.
            El master anterior queda recuperable por nombre, no solo por reflog, en la
            rama `master-pre-upstream-sync` y la etiqueta `pre-upstream-sync-2026-09-02`,
            ambas en `origin`. Son 87 commits que no están en esta línea, porque el
            re-port se rehízo sobre el layout nuevo en vez de fusionarse.
      - [x] `web/`: CSS, temas y plantillas. **Casi nada había que portar.** Los doce
            SVG de conector ya están en upstream byte a byte idénticos, y su mecanismo
            para pintarlos (reglas CSS por tipo con `dex-btn-icon--{{ $c.Type }}`) es
            mejor que la cadena `if/else` del fork, que no cubría `local` ni los mock y
            perdía los colores de marca. El resto del CSS del fork es de su propio
            maquetado, el que la reescritura de upstream sustituyó, y no lo referencia
            ninguna plantilla. Solo entró el pie de página, con un fallo corregido: en
            `master` la cadena lleva `%d` y la plantilla no lo interpola.
      - [x] **Arreglado un defecto propio**: el `primaryColor` por cliente no pintaba
            nada. `header.html` definía `--primary-color` y ningún selector del CSS de
            upstream la leía. El test comprobaba el mecanismo (que el `<style>` aparece)
            en vez del efecto. Ahora el botón primario, su hover y el foco del campo leen
            las variables con el hex del tema como respaldo, y el test lo exige por
            selector.
      - [x] **Portar `cmd/dex-dashboard` y la cadena Docker.** Inventariado: ~30
            ficheros, ~5100 líneas, de las que ~4800 son acarreo directo. `api/v2` no se
            mueve y los mensajes nuevos son aditivos, así que los literales con campos
            nombrados siguen compilando. Lo que necesita decisión real:
            - `server.EncodeSubject` **no existe** en upstream. Su equivalente exacto es
              `server/tokens.GenSubject(userID, connID)` — verificado, misma firma y
              mismo `internal.Marshal`. Es cambiar un import y una llamada.
            - `server.ConnectorsConfig` **sí** sigue en `github.com/dexidp/dex/server`;
              lo que se movió es la interfaz `ConnectorConfig` a `server/connectors`. El
              panel nunca la nombra, solo llama a la factoría, así que no le afecta.
            - **`config.docker.yaml` choca de verdad**: upstream sustituyó
              `expiry.signingKeys` por un bloque `signer:` completo. Hay que rehacer la
              mezcla sobre su estructura, sin perder nuestro `web.headers` ni el bloque
              `grpc:`, del que el panel depende para existir.
            - `cmd/docker-entrypoint` **ya existe** en upstream: es una mezcla, no un
              alta. Hay que llevar `needsTemplating`/`isCommand` y reconciliar los dos
              `main_test.go`.

            Resuelto todo. Dos hallazgos no previstos: el linter de la rama destapó que
            `handleLogin` era código muerto sin ruta que lo montara, y `.gitignore`
            ignoraba `.pre-commit-config.yaml` **a propósito** — no faltaba por descuido,
            upstream no lo versiona. Al pasarlo a versionado, `go mod tidy` pidió
            reclasificar tres dependencias de indirectas a directas; no entra ninguna
            nueva. Verificado construyendo la imagen y arrancando dex con ella.
      - [x] **`api_connectors_crud`.** Upstream mete las cuatro RPC de conector detrás
            de ese feature flag, que viene apagado, así que sin él la pestaña de
            Conectores no funciona. No se toca el valor por defecto —activarlo para todo
            el que encienda gRPC sería ampliar la superficie más de lo necesario—: lo
            activa el compose de ejemplo, y el panel ya explica el error con todas las
            letras cuando está apagado. Verificado en el despliegue: lista conectores de
            verdad.

  Por qué no es un `git merge`: upstream troceó `server/` en subpaquetes y los seis ficheros donde vive nuestro trabajo (`handlers.go`, `oauth2.go`, `api.go`, `templates.go`, `refreshhandlers.go`, `deviceflowhandlers.go`) ya no existen allí, así que git los ve como modify/delete. No activar de golpe lo nuevo de upstream (sesiones, `sid`, back-channel logout, PKCE configurable, CEL, Kerberos).

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
      el subject con `tokens.GenSubject`; el camino inverso, para un `sub` pegado, usa
      `tokens.ParseSubject`, exportada porque `server/internal` no es importable desde `cmd/`.
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
- [x] **Campos nuevos de `Client` y `Connector` en los formularios.** El cliente edita ya
      `allowedConnectors`, `ssoSharedWith`, `backchannelLogoutURI`,
      `postLogoutRedirectURIs` y `refreshTokenLifetime`; el conector, sus `grantTypes`.
      Son justo los mandos del logout y del SSO que acabamos de encender.
      **La asimetría importa**: los dos campos de valor único se vacían —upstream los
      declaró `optional` para eso— pero las listas no, porque una lista repetida vacía no
      viaja en protobuf y es indistinguible de «no toques este campo». El formulario lo
      avisa. Los grant types del conector sí se vacían: ahí la lista va envuelta en un
      mensaje. Comprobado sobre el despliegue, incluidos los dos casos de vaciar.

---

## 🎁 Heredado del re-port, ya encendido

> Lo que upstream construyó desde la divergencia y el re-port trajo apagado tras
> feature flags. Encenderlo era una decisión, no trabajo: aquí está tomada, medida y
> expuesta en el panel. La numeración es la que tenían estas secciones en el TODO.

### 0. 🔑 Sesiones de navegador (`sessions_enabled`)

Medido antes de encenderlo. Lo que enciende el flag:

- Cookie de sesión (`dex_session`, 24 h absolutas y 1 h de inactividad por defecto).
- Se salta la **pantalla de selección de conector** cuando hay sesión válida y el cliente
  no pide `prompt=select_account`. Sigue autenticando contra el conector: no es un
  inicio de sesión silencioso.
- Páginas nuevas, ya traducidas a los cinco idiomas durante el re-port y hasta entonces
  inertes: la de sesión (`/`), la de logout y la casilla de *remember me*.
- Claim `sid`, back-channel logout y revocación con alcance de sesión.
- **Es requisito duro del MFA nativo**: dex se niega a arrancar con autenticadores
  configurados y el flag apagado. No son dos decisiones, es una.

Riesgo acotado: el **SSO entre clientes es opt-in**, `ssoSharedWithDefault` viene en
`none`, así que encenderlo no empieza a compartir sesiones entre aplicaciones por su
cuenta. El back-channel logout solo dispara para clientes con `backchannelLogoutURI`.

- [x] **Probado en `Ejemplos/dashboard`**, que queda con el flag encendido. Verificado
      sobre el despliegue: cookie `dex_session` con `HttpOnly`, casilla de *recordarme*
      marcada por defecto, página de sesión en `/dex` mostrando conector, hora de inicio,
      caducidad por inactividad, IP, grupos y navegador —toda en español, con las claves
      que se tradujeron durante el re-port y que hasta ahora no se veían— y página de
      logout que termina la sesión de verdad: después queda «Sin sesión iniciada» y la
      siguiente petición de auth vuelve a la lista de conectores.
- [x] **Medido el radio de impacto, y es pequeño.** Con una sesión viva, una petición de
      otro cliente **se salta la pantalla de selección de conector** y va directa al
      conector — pero **sigue pidiendo la contraseña**. No hay inicio de sesión silencioso
      entre clientes mientras `ssoSharedWith` esté en `none`, que es el valor por defecto.
      Es decir: encender el flag ahorra un clic y añade páginas nuevas, no cambia quién
      puede entrar dónde.
- [x] **Encendidas por defecto en la imagen.** `DEX_SESSIONS_ENABLED=true` va en el
      `Dockerfile`, no en el binario: upstream sigue con su valor por defecto y quien
      quiera el comportamiento de antes pone la variable a `false`. `config.docker.yaml`
      renderiza el bloque `sessions:` con la misma variable y parseada igual —dex se
      niega a arrancar con el bloque puesto y el flag apagado, así que no pueden ir por
      separado—. Comprobado sobre la imagen en cinco configuraciones.
      Un aviso: poner la variable a **cadena vacía** no es lo mismo que a `false`.
      gomplate la trata como no definida y renderiza el bloque; el flag la trata como
      apagada. Dex falla al arrancar diciendo exactamente qué poner, así que se queda así.
- [x] **La clave de cifrado no se puede repartir, así que se avisa.** No hay clave que
      pueda venir en una imagen pública, y generar una al arrancar cerraría la sesión de
      todo el mundo en cada reinicio y no valdría con réplicas. Se cablea
      `DEX_SESSIONS_COOKIE_ENCRYPTION_KEY` y dex **avisa al arrancar** cuando no hay
      clave, diciendo qué se gana con ella y qué no.
      De paso, corregido lo que dábamos por hecho: sin clave la cookie **no va firmada**,
      va en base64 en claro. Y sellarla no impide reutilizar una cookie robada —cifrada o
      no, la cookie *es* la credencial—: lo que evita es que quien la lea o la registre en
      un log vea el id de sesión que lleva dentro.

### 0.1 🗂️ API de sesiones e identidades (`api_sessions_identities_crud`)

- [x] **Expuesta en el panel, lo principal.** La vista de Sesiones muestra ahora, en
      secciones separadas: la identidad del usuario (correo, grupos, último acceso,
      segundos factores y **si la cuenta está bloqueada**), sus consentimientos con la
      acción de retirarlos por cliente, sus navegadores con sesión, y los refresh tokens
      de siempre. Navegadores y tokens van aparte a propósito: cerrar una sesión revoca
      los tokens que salieron de ella, revocar un token no cierra la sesión. Acciones:
      cerrar un navegador, cerrarlos todos, y **cerrar todos los de un conector** desde la
      página de Conectores, para retirar un proveedor de identidad.
      Hizo falta exportar `tokens.ParseSubject`: las sesiones se buscan por el par
      `(userID, connectorID)` y `server/internal` no es importable desde `cmd/`.
- [x] **Purga RGPD de una identidad**, con inventario en la confirmación. Cuenta lo que
      va a desaparecer y nombra aparte la consecuencia que el título de la acción no
      sugiere: el almacén de contraseñas está indexado **por correo, sin conector**, así
      que purgar una identidad borra también la cuenta local con ese mismo correo.
- [x] **La purga ya no deja el trabajo a medias.** La cascada recorre varios almacenes sin
      transacción entre ellos, así que `server/apiserver` comprueba primero el único paso
      que falla por un motivo predecible —una contraseña del fichero de configuración, que
      la API no puede borrar— y se niega sin destruir nada. Antes se enteraba al llegar a
      ella, con las sesiones ya cerradas y los tokens revocados.
      Hizo falta exponer `IsStaticPassword` en el envoltorio de almacenamiento estático:
      la pregunta «¿esto se puede borrar?» no se podía hacer sin intentarlo. Es una
      aserción de interfaz en el apiserver, no parte de `storage.Storage`: solo la purga
      necesita preguntarlo, y solo para negarse.
      **La trampa**: los tres envoltorios estáticos incrustan `Storage` como *interfaz*,
      así que no se promocionan métodos entre ellos. La primera versión solo funcionaba si
      el de contraseñas quedaba el más externo —en `serve.go` no lo es— y pasó el test
      mientras no hacía nada en el despliegue. Lo destapó probarlo de verdad. Ahora la
      pregunta se reenvía hacia dentro y el test apila los envoltorios como `serve.go`.
      El test falla por lo que importa —la sesión sigue viva— antes que por el texto.
- [x] **«He perdido el móvil»: quitar un segundo factor desde el panel.** Los factores
      registrados tienen ahora su propia sección con una fila por autenticador y un botón
      que llama a `DeleteMFASecret`; el siguiente login ofrece darse de alta otra vez con
      un secreto nuevo. Exige login reciente, como el resto de lo destructivo, porque
      quitar el último deja la cuenta con la contraseña sola.
      Verificado de punta a punta contra el despliegue de ejemplo: alta de TOTP, retirada
      desde el panel, secreto fuera del almacén y alta nueva con otro secreto en el
      siguiente login.

### 4. 🔐 Autenticación avanzada — el MFA nativo junto al de Keystone

> WebAuthn y TOTP nativos llegaron hechos con el re-port. Lo que había que resolver
> era cómo conviven con un conector que impone su propio segundo factor.

- [x] **Convivencia del MFA nativo con el de Keystone: resuelta por configuración.**
      No hacía falta mecanismo nuevo — `chainForClient` ya resuelve la cadena mirando el
      conector y cada autenticador acepta `connectorTypes`. La trampa es que
      **`connectorTypes` vacío significa *todos* los conectores**, así que el valor por
      defecto es el peligroso. Dex avisa ahora al arrancar cuando un autenticador alcanza
      un conector que ya impone su propio segundo factor, y solo si ese conector existe
      en el despliegue.

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
| Re-port sobre upstream                      |   ✅   | cerrado, es ya la línea principal          |
| i18n sobre el marcado de upstream           |   ✅   | 66 claves × 5 idiomas, respaldo al inglés  |
| Tokens con nombre en la API gRPC            |   ✅   | `grpc.tokens`, `caller` en la auditoría    |
| Cookie de dispositivo de confianza cifrada  |   ✅   | ⚠️ AES-GCM, clave obligatoria; `CHANGELOG` |
| Panel y cadena Docker sobre upstream        |   ✅   | verificado construyendo la imagen          |
| Panel de administración                     |   ✅   | `cmd/dex-dashboard`, 4 fases entregadas   |
| Auditoría con nombre en la API gRPC         |   ✅   | cabecera `x-dex-actor` registrada por dex |
| Sesiones de navegador                       |   ✅   | encendidas en la imagen, aviso sin clave  |
| API de sesiones e identidades               |   ✅   | expuesta en el panel: identidad, factores |
| Purga RGPD atómica                          |   ✅   | se niega antes de romper nada             |
| MFA nativo junto al de Keystone             |   ✅   | por `connectorTypes`, con aviso al arrancar |
