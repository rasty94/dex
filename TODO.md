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
      - [ ] Trusted device sobre la maquinaria de cookies de upstream (ya cifradas), no sobre
            la cookie propia `dex_mfa_trust_*`.
      - [ ] Flujo TOTP en el servidor (`ErrTOTPRequired`) → `server/authflow/`.
      - [ ] `.proto` y config dinámica de gRPC: regenerar y reaplicar nuestras extensiones.
      - [ ] `web/`: CSS, temas y plantillas.

  Por qué no es un `git merge`: upstream troceó `server/` en subpaquetes y los seis ficheros donde vive nuestro trabajo (`handlers.go`, `oauth2.go`, `api.go`, `templates.go`, `refreshhandlers.go`, `deviceflowhandlers.go`) ya no existen allí, así que git los ve como modify/delete. No activar de golpe lo nuevo de upstream (sesiones, `sid`, back-channel logout, PKCE configurable, CEL, Kerberos).

### 6. 🎛️ Dashboard de administración

> **Las cuatro fases están entregadas**, más un bloque de endurecimiento de seguridad
> (re-autenticación, caducidad por inactividad, `__Host-`, límite de login), esqueletos de
> conector, revocación masiva de sesiones, exportación de configuración y vista de discovery.
> Lo hecho, con sus decisiones de diseño, está en
> [DONE.md](DONE.md); cómo funciona, en
> [documentacion/dashboard-administracion.md](documentacion/dashboard-administracion.md).
> Aquí queda solo lo pendiente.

- [ ] **Tokens con nombre en la API gRPC de dex.** Hoy es un único token compartido. El panel
      manda `x-dex-actor` y dex lo registra, pero eso es el panel atestiguando, no una identidad
      que dex verifique. Con varios tokens con nombre, un cliente distinto del panel también
      quedaría identificado y se podrían revocar por separado.
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

### 7. 🧹 Deuda técnica conocida

- [ ] **Sesiones Keystone antiguas con `userIDKey: email`/`username`**: `Refresh()` usa ahora el id real de Keystone guardado en `identity.ConnectorData`. Las sesiones creadas antes de ese cambio no lo tienen y caen al fallback (`identity.UserID`, que es el UUID sintético), así que seguirán fallando el refresh hasta que el usuario vuelva a autenticarse. Anotarlo en el `CHANGELOG` de la próxima release.
- [ ] **`Ejemplos/` en solo lectura**: el directorio está como `dr-xr-xr-x` en algunos entornos de desarrollo, lo que hace fallar `git checkout` sobre `Ejemplos/config.yaml` con "Permission denied". Se arregla con `chmod u+w Ejemplos`.
