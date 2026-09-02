# TODO — Dex Fork (rasty94/dex)

> Solo trabajo pendiente. Lo ya entregado vive en [DONE.md](DONE.md), incluida la
> tabla de estado y los bloques cerrados de la sincronización con upstream.
>
> Última actualización: 2026-09-01
> Imagen Docker: `ghcr.io/rasty94/dex:latest`
> Repositorio: <https://github.com/rasty94/dex>

---

## 🚀 Futuras Mejoras (Propuestas)

### 1. 📊 Telemetría

- [ ] Trazabilidad distribuida (OpenTelemetry) para peticiones hacia OpenStack.

### 2. 🚀 Rendimiento y Alta Disponibilidad (HA)

- [ ] Cache Distribuida: Expandir la actual caché nativa en RAM de Keystone para soportar Redis de backend, permitiendo despliegues de Dex multi-réplica compartir estado de validación de tokens.
    - Los buckets del rate limiter de login tienen el mismo techo y se migran con ella: hoy el límite efectivo es `attempts × réplicas`.

### 3. ☁️ Ecosistema Cloud Native e Integraciones

- [ ] Provider para HashiCorp Vault: Leer el `adminPassword` y los app-credentials nativamente de Vault sin exponerlos en el `config.yaml`.
- [ ] Helm Chart u Operator Kubernetes Mejorado: Adaptar configuraciones del Fork directamente en los values nativos del chart oficial de la comunidad.

### 4. 🔐 Autenticación Avanzada (Beyond TOTP)

- [ ] WebAuthn / Passkeys: Empezar a sentar las bases para la autenticación sin contraseña (Passwordless) en Keystone, como segundo factor apoyándose en WebAuthn o llaves físicas FIDO2 (Yubikey).
- [ ] Políticas Condicionales: Permitir bloquear el login basado en roles o dominios específicos de OpenStack directamente en el Connector antes de emitir claims JWT.

### 5. ✅ Sincronización con upstream — bloque F (cerrado)

> Bloques A a E y G cerrados, con su detalle en [DONE.md](DONE.md).
>
> ✅ **Cerrado.** El re-port es ya la línea principal. El master anterior está
> respaldado en `master-pre-upstream-sync` y en la etiqueta
> `pre-upstream-sync-2026-09-02`.

- [x] **Re-port sobre `upstream/master`.** ✅ **Todas las rebanadas cerradas** en la rama
      `feat/upstream-sync-2026-08` (empujada a `origin`, partiendo de `upstream/master`).
      Worktree en `../dex-upstream-sync`, para no mezclarla con `master`.
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
      - [ ] **Decidir qué hacer con `api_connectors_crud`.** Upstream mete las cuatro RPC
            de conector detrás de ese feature flag, que viene apagado. Sin él la pestaña
            de Conectores del panel **no funciona**: es el mismo error que ya vimos en el
            despliegue. Hay que activarlo en la cadena Docker cuando el panel esté
            encendido, o documentarlo como requisito.
      - [ ] **Campos nuevos de `Client` y `Connector` en los formularios del panel.**
            Upstream ha añadido `allowedConnectors`, `ssoSharedWith`,
            `backchannelLogoutURI`, `postLogoutRedirectURIs`, `refreshTokenLifetime` y
            `grantTypes`. No rompen nada — el handler de upstream solo aplica los campos
            presentes — pero el panel no los sabe editar.

  Por qué no es un `git merge`: upstream troceó `server/` en subpaquetes y los seis ficheros donde vive nuestro trabajo (`handlers.go`, `oauth2.go`, `api.go`, `templates.go`, `refreshhandlers.go`, `deviceflowhandlers.go`) ya no existen allí, así que git los ve como modify/delete. No activar de golpe lo nuevo de upstream (sesiones, `sid`, back-channel logout, PKCE configurable, CEL, Kerberos).

### 6. 🎛️ Dashboard de administración

> **Las cuatro fases están entregadas**, más un bloque de endurecimiento de seguridad
> (re-autenticación, caducidad por inactividad, `__Host-`, límite de login), esqueletos de
> conector, revocación masiva de sesiones, exportación de configuración y vista de discovery.
> Lo hecho, con sus decisiones de diseño, está en
> [DONE.md](DONE.md); cómo funciona, en
> [documentacion/dashboard-administracion.md](documentacion/dashboard-administracion.md).
> Aquí queda solo lo pendiente.

- [x] **Tokens con nombre en la API gRPC de dex.** Hecho en la rama, dentro de la rebanada
      `.proto`. `grpc.tokens` acepta pares nombre/token, la línea de auditoría lleva ya
      `caller`, y el `grpc.token` de siempre sigue valiendo como `default`. Queda pendiente
      **enseñárselo al panel**: hoy manda un único token y no sabe de nombres.
- [ ] **Visor de intentos de login fallidos.** No se puede hacer con la API actual: dex los
      escribe en su log y no hay forma de consultarlos. Necesita un recolector de logs, no una
      vista más. La de Status ya dice *cuántos*; falta el *quién* y el *cuándo*.
- [ ] **Paginación en los listados.** Ya hay filtro por texto en clientes, conectores y
      usuarios, que resuelve el caso de «encontrar uno». Con miles de filas haría falta además
      paginar, y eso sí necesita que la API de dex lo soporte: hoy `ListClients` devuelve todo.
- [ ] **Sesiones compartidas entre réplicas.** Hoy viven en memoria del proceso: un reinicio
      pide login otra vez y el panel no sobrevive a estar replicado. Va de la mano de la caché
      distribuida de la sección 2.
- [ ] **htmx.** El panel no sirve JavaScript y la CSP está en `default-src 'none'`. Entra
      cuando alguna pantalla gane algo real con actualización parcial, no antes.
