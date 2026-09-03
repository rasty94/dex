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
      prueba.** Estuvo nueve días parada por culpa nuestra: al commit le faltaba el
      `Signed-off-by` y el DCO se quedó en *action required*, que es un estado donde nadie
      mira nada. Desbloqueada el 2026-09-03 con `git commit --amend -s` y un force-push;
      DCO ✅, Snyk ✅, sin conflictos. Esperando revisión. La lección no es el DCO: un PR
      abierto hay que volver a mirarlo, no solo abrirlo.

---

## 🚀 Futuras Mejoras (Propuestas)

### 1. 📊 Telemetría

- [ ] Trazabilidad distribuida (OpenTelemetry) para peticiones hacia OpenStack.
- [ ] **Ni el MFA nativo ni las sesiones de navegador exportan una sola
      métrica.** Las dos cosas se encendieron en este fork y las dos son
      material de alarma: un segundo factor fallido no lo cuenta nadie, y no
      hay forma de saber cuántas sesiones se abren, cuántas caducan por
      inactividad y cuántas las cierra alguien a mano. Lo que hay es
      `dex_login_rate_limited_total`, `dex_login_rate_limit_backend_errors_total`
      y las cinco de Keystone; el patrón está escrito dos veces
      ([server/server.go:242](server/server.go#L242) y
      [connector/keystone/metrics.go](connector/keystone/metrics.go)), así que
      esto es seguir el que ya existe, no inventar nada.

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
- [ ] **Un Valkey caído no se distingue de una caché fría.** En
      `connector/keystone/cache_valkey.go` cualquier error de `GET` se devuelve
      como fallo de caché —correcto, el login no debe romperse porque la
      optimización no esté— pero no se registra ni se cuenta: el `set` que
      falla se descarta igual de callado. Con Valkey inalcanzable,
      `keystone_token_cache_lookups_total{result="miss"}` marca el 100 % y
      parece tráfico nuevo, mientras cada login paga un viaje entero a
      Keystone. Es el mismo agujero que ya está anotado arriba para el
      limitador: falta la etiqueta o el contador que diga *por qué* no hubo
      acierto.
- [ ] **Valkey es hoy un punto único de fallo, y `New` solo admite una
      dirección.** `pkg/valkey/valkey.go` pasa un `InitAddress` con un
      elemento: sin sentinel, sin cluster y sin certificado de cliente (el
      bloque TLS solo tiene `caCert` e `insecureSkipVerify`, así que la
      autenticación del cliente es la contraseña). Mientras Valkey esté
      apagado la configuración sigue siendo opcional y esto no importa; en
      cuanto se encienda en producción, dex deja de arrancar si el servidor no
      responde. `valkey-go` soporta las tres cosas.
- [ ] **Nadie ha dicho con qué política de memoria debe correr ese Valkey.**
      Si el operador lo levanta con `maxmemory` y `allkeys-lru`, los
      contadores del limitador de login se pueden desalojar antes de que cierre
      su ventana: bajo presión de memoria el presupuesto de intentos se
      reinicia solo, que es justo lo que el límite existe para impedir. Y sin
      persistencia —el ejemplo usa `--save ""`, que ahí está bien— un reinicio
      se lleva por delante todas las sesiones del panel y todos los contadores.
      Hace falta escribir qué exige dex del servidor (`noeviction` o
      `volatile-*`, y qué se pierde en un reinicio), hoy repartido entre
      [keystone_connector.md](documentacion/keystone_connector.md), la del panel
      y la spec, sin una página propia.
- [ ] La afirmación de que dex se niega a arrancar con un `cacheTTL` inválido
      no es cierta con el feature flag `continue_on_connector_failure` activo
      (su valor por defecto): ahí el conector falla, dex lo registra y arranca
      igual sin él. La documentación del conector Keystone y el CHANGELOG
      deberían decirlo explícitamente en vez de dar a entender que el arranque
      siempre se detiene.

### 3. ☁️ Ecosistema Cloud Native e Integraciones

- [ ] Provider para HashiCorp Vault: Leer el `adminPassword` y los app-credentials nativamente de Vault sin exponerlos en el `config.yaml`.
- [ ] Helm Chart u Operator Kubernetes Mejorado: Adaptar configuraciones del Fork directamente en los values nativos del chart oficial de la comunidad.
- [ ] **Rol de Ansible para desplegar el fork.** Lo que hay hoy es un ejemplo para
      probar en local y una guía de TLS escrita a mano
      ([despliegue-docker-tls.md](documentacion/despliegue-docker-tls.md)): no hay nada
      que ponga esto en una máquina de verdad sin repetir los pasos a mano cada vez.
      El rol tendría que cubrir la imagen y su versión, el fichero de configuración con
      sus secretos fuera del repositorio, los certificados, el arranque como servicio, y
      el panel como su propio proceso. Con Valkey recién integrado hay además una pieza
      más que instalar y a la que hay que apuntar a los dos binarios.
- [ ] **Un `docker compose` de producción, separado de los ejemplos.** Los de
      `Ejemplos/` dicen en su primera línea que no se usen tal cual, y con razón:
      publican en `127.0.0.1`, llevan los secretos escritos en el fichero, comparten
      espacio de red entre servicios para no tocar `/etc/hosts`, y no tienen TLS. Hace
      falta uno pensado para un despliegue: secretos por fuera, TLS, red separada por
      servicio, y límites y reinicio declarados. La diferencia entre los dos ficheros es
      justo lo que hay que documentar.
- [ ] **Sondas que digan la verdad.** El orden de arranque está resuelto por dentro
      —`newAuthenticatorWithRetry` en [main.go:62](cmd/dex-dashboard/main.go#L62) reintenta
      el discovery, así que el panel puede levantarse antes que dex— pero las sondas no
      sirven para lo que un orquestador necesita: el `/healthz` del panel
      ([main.go:75](cmd/dex-dashboard/main.go#L75)) devuelve 200 siempre, sin mirar si la
      conexión gRPC con dex o el Valkey de las sesiones responden. Como sonda de vida está
      bien; como sonda de disponibilidad miente, y el orquestador manda tráfico a un panel
      donde todas las páginas van a fallar. Falta además decir qué hacer cuando Valkey no
      está: dex se niega a arrancar, que es lo correcto, pero hay que reintentar en vez de
      darlo por muerto.

### 4. 🔐 Autenticación Avanzada (Beyond TOTP)

> **Reescrita tras el re-port.** WebAuthn ya no hay que construirlo: upstream trae
> `server/mfa` con TOTP nativo (con protección contra reutilización del código) y
> WebAuthn completo, más sus RPC de gestión. Su convivencia con el segundo factor de
> Keystone está resuelta y en [DONE.md](DONE.md). Queda lo que sí es trabajo nuevo.

- [ ] **Passkeys sin contraseña para Keystone.** Esto sí es trabajo nuevo: WebAuthn de
      upstream es un segundo factor, no un primer factor, así que el login sin contraseña
      contra Keystone sigue sin base.
- [ ] **Políticas Condicionales: bloquear el login por rol o dominio de OpenStack antes
      de emitir los claims.** Lo que trae upstream es menos de lo que parece y también más:
      `pkg/cel` compila y evalúa expresiones, con `IdentityFromConnector`,
      `RequestFromContext` y `EvalBool` ya escritos —justo las piezas de esto— pero **no lo
      importa nadie**: `go mod why cel-go` responde que el módulo principal no lo necesita,
      y en el árbol de upstream tampoco hay un solo consumidor. Publicaron el evaluador
      antes que su punto de enganche. Así que el trabajo no es escribir políticas: es el
      campo de configuración y la llamada después de que el conector devuelva la identidad.
      El evaluador sale gratis; conviene mirar antes si upstream va a poner ahí su propio
      enganche, para no divergir en el mismo sitio.

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
