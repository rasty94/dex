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

### 5. 🔄 Sincronización con upstream — bloque F

> Bloques A a E y G cerrados, con su detalle en [DONE.md](DONE.md).
>
> **Decidido: al terminar, la rama sustituye a `master`.** Eso convierte el re-port en la
> línea principal, así que todo lo que hoy vive solo en `master` — el panel y la cadena
> Docker — tiene que portarse antes de hacer el cambio.

- [ ] **Re-port sobre `upstream/master`.** 🚧 **En marcha** en la rama
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
      - [ ] i18n y theming por cliente → `server/templates/`.
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
      - [ ] **Portar el tooling del fork**: `.pre-commit-config.yaml` no está en la rama,
            así que ahora mismo los commits allí van sin hooks y el lint hay que lanzarlo
            a mano. Van con él los objetivos del `Makefile` que compilan los tres binarios.
      - [ ] `web/`: CSS, temas y plantillas.
      - [ ] **Portar `cmd/dex-dashboard` y la cadena Docker.** Decidido que la rama
            sustituye a `master`, así que el panel tiene que viajar con ella: el binario,
            `cmd/docker-entrypoint`, `config.dashboard.docker.yaml` y `Ejemplos/dashboard/`.
            No es mecánico: el panel usa `server.ConnectorsConfig` y `server.EncodeSubject`,
            y las dos se mueven de sitio en el re-port.

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
