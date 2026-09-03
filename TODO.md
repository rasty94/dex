# TODO — Dex Fork (rasty94/dex)

> Solo trabajo pendiente. Lo ya entregado vive en [DONE.md](DONE.md), incluida la
> tabla de estado y la sincronización con upstream, cerrada por completo: el re-port
> sobre el layout nuevo es ya la línea principal del fork, publicada como `fork-v2.0.0`.
>
> Lo que el re-port trajo apagado tras feature flags —sesiones de navegador, API de
> sesiones e identidades, MFA nativo— está decidido y encendido; también en
> [DONE.md](DONE.md).
>
> Última actualización: 2026-09-03
> Imagen Docker: `ghcr.io/rasty94/dex:latest`
> Repositorio: <https://github.com/rasty94/dex>

---

## ⏳ En manos de otros

> Lo enviado a upstream. Nada de esto bloquea al fork: los dos arreglos ya están aquí.
> La política sigue siendo no contribuir al repo público; estas dos son excepciones
> decididas una a una.

- [ ] **[dexidp/dex#5000](https://github.com/dexidp/dex/pull/5000) — la purga RGPD que se
      quedaba a medias.** El fallo destruye datos y luego no se puede terminar desde su
      API, que es lo que justificó la excepción. Portado a la forma que `storage/static.go`
      tiene allí —receptores por valor, sin recarga dinámica— y con `NewAPI` de seis
      argumentos; sus tests pasan sobre su árbol. DCO ✅, Snyk ✅, sin conflictos.
      Abierta el 2026-09-03, esperando revisión.
- [ ] **[dexidp/dex#4986](https://github.com/dexidp/dex/pull/4986) — el `alg` del JWKS de
      prueba.** ⚠️ **Bloqueada por nuestra culpa desde el 2026-08-26**: su commit no lleva
      `Signed-off-by`, así que el check de DCO está en *action required* y nadie la va a
      mirar. Se arregla con `git commit --amend -s` y un force-push a la rama
      `fix/oidc-test-jwks-alg` de nuestro fork. Ocho días parada sin que nadie lo dijera:
      un PR abierto hay que volver a mirarlo, no solo abrirlo.

---

## 🚀 Futuras Mejoras (Propuestas)

### 1. 📊 Telemetría

- [ ] Trazabilidad distribuida (OpenTelemetry) para peticiones hacia OpenStack.

### 2. 🚀 Rendimiento y Alta Disponibilidad (HA)

> **El estado compartido entre réplicas está entregado** — Valkey opcional
> (`valkey.address`), contadores del rate limiter y caché de tokens de Keystone
> incluidos. Lo hecho está en [DONE.md](DONE.md). Aquí queda solo lo que no entró.

- [ ] Caché en cliente de `valkey-go`: hoy está desactivada a propósito
      (`DisableCache: true` en `pkg/valkey/valkey.go`) porque miniredis, usado en
      los tests, no implementa la invalidación asistida por servidor que necesita.
      Activarla evitaría un viaje de red por cada lectura de caché compartida, a
      costa de esa dependencia con los tests.
- [ ] El `attemptLimiter` del panel (`cmd/dex-dashboard/auth.go`) sigue siendo
      local a propósito: protege el propio arranque de login del panel, no el
      login de Dex, y con una sola réplica del panel por despliegue el límite
      efectivo por proceso ya es el límite real.
- [ ] `Client.Key` en `pkg/valkey/valkey.go` no tiene más uso que su propio
      test: cada componente que necesita una clave pasa por `HashKey`. Un
      ayudante de clave sin hashear y sin usuarios invita a que alguien meta un
      secreto en el nombre de una clave donde no toca. Retirarlo, o darle un
      uso real, antes de que alguien lo use mal.
- [ ] En `server/ratelimit`, un cliente que aborta su petición cancela el
      contexto, y eso hoy cuenta como fallo del backend igual que Valkey
      inalcanzable — cae al bucket local y suma en
      `dex_login_rate_limit_backend_errors_total`. No es un bypass del
      límite, pero la métrica que debería significar "Valkey inalcanzable"
      también cuenta desconexiones normales de cliente, lo que la hace menos
      fiable como alarma. Distinguir `context.Canceled` del resto de errores
      antes de contarlo.
- [ ] La afirmación de que dex se niega a arrancar con un `cacheTTL` inválido
      no es cierta con el feature flag `continue_on_connector_failure` activo
      (su valor por defecto): ahí el conector falla, dex lo registra y arranca
      igual sin él. La documentación del conector Keystone y el CHANGELOG
      deberían decirlo explícitamente en vez de dar a entender que el arranque
      siempre se detiene.

### 3. ☁️ Ecosistema Cloud Native e Integraciones

- [ ] Provider para HashiCorp Vault: Leer el `adminPassword` y los app-credentials nativamente de Vault sin exponerlos en el `config.yaml`.
- [ ] Helm Chart u Operator Kubernetes Mejorado: Adaptar configuraciones del Fork directamente en los values nativos del chart oficial de la comunidad.

### 4. 🔐 Autenticación Avanzada (Beyond TOTP)

> **Reescrita tras el re-port.** WebAuthn ya no hay que construirlo: upstream trae
> `server/mfa` con TOTP nativo (con protección contra reutilización del código) y
> WebAuthn completo, más sus RPC de gestión. Su convivencia con el segundo factor de
> Keystone está resuelta y en [DONE.md](DONE.md). Queda lo que sí es trabajo nuevo.

- [ ] **Passkeys sin contraseña para Keystone.** Esto sí es trabajo nuevo: WebAuthn de
      upstream es un segundo factor, no un primer factor, así que el login sin contraseña
      contra Keystone sigue sin base.
- [ ] Políticas Condicionales: Permitir bloquear el login basado en roles o dominios específicos de OpenStack directamente en el Connector antes de emitir claims JWT.
      Upstream trae ahora políticas CEL, que podrían servir de base en vez de escribirlo en el conector.

### 6. 🎛️ Dashboard de administración

> **Las cuatro fases están entregadas**, más un bloque de endurecimiento de seguridad
> (re-autenticación, caducidad por inactividad, `__Host-`, límite de login), esqueletos de
> conector, revocación masiva de sesiones, exportación de configuración y vista de discovery.
> Lo hecho, con sus decisiones de diseño, está en
> [DONE.md](DONE.md); cómo funciona, en
> [documentacion/dashboard-administracion.md](documentacion/dashboard-administracion.md).
> Aquí queda solo lo pendiente.

- [ ] **El listado de clientes no enseña los campos nuevos.** Para saber qué clientes
      tienen back-channel logout o SSO compartido hay que abrirlos uno a uno. Una columna
      o un distintivo en la tabla, cuando estorbe de verdad.
- [ ] **`ResetMFA` y `DeleteWebAuthnCredential` no se han expuesto.** El panel ya quita un
      segundo factor; el primero de estos es recorrer la lista de autenticadores, que en la
      práctica son uno o dos, y el segundo solo importa cuando alguien tiene varias llaves
      bajo el mismo autenticador y pierde una — hoy se van todas con él.
- [ ] **Enseñar al panel los tokens con nombre.** Dex ya los acepta (`grpc.tokens`, con
      `caller` en la auditoría), pero el panel manda un único token y no sabe de nombres.
      Lo entregado está en [DONE.md](DONE.md).
- [ ] **Visor de intentos de login fallidos.** No se puede hacer con la API actual: dex los
      escribe en su log y no hay forma de consultarlos. Necesita un recolector de logs, no una
      vista más. La de Status ya dice *cuántos*; falta el *quién* y el *cuándo*.
- [ ] **Paginación en los listados.** Ya hay filtro por texto en clientes, conectores y
      usuarios, que resuelve el caso de «encontrar uno». Con miles de filas haría falta además
      paginar, y eso sí necesita que la API de dex lo soporte: hoy `ListClients` devuelve todo.
- [ ] **htmx.** El panel no sirve JavaScript y la CSP está en `default-src 'none'`. Entra
      cuando alguna pantalla gane algo real con actualización parcial, no antes.
- [ ] **Revocar una sesión de administrador ya no es tan simple como reiniciar el
      panel.** Con las sesiones en Valkey, sobreviven un reinicio, así que quitar a
      alguien de `admin.writeGroups` le deja la sesión que ya tenía con permiso de
      escritura hasta que caduque — no hay forma de terminarla antes salvo borrar la
      clave a mano en Valkey. Falta documentarlo y, más adelante, un botón en el panel
      para cerrar una sesión concreta.
