# TODO — Dex Fork (rasty94/dex)

> Solo trabajo pendiente. Lo ya entregado vive en [DONE.md](DONE.md), incluida la
> tabla de estado y la sincronización con upstream, cerrada por completo: el re-port
> sobre el layout nuevo es ya la línea principal del fork, publicada como `fork-v2.0.0`.
>
> Lo que el re-port trajo apagado tras feature flags —sesiones de navegador, API de
> sesiones e identidades, MFA nativo— está decidido y encendido; también en
> [DONE.md](DONE.md).
>
> Última actualización: 2026-09-04
> Imagen Docker: `ghcr.io/rasty94/dex:latest`
> Repositorio: <https://github.com/rasty94/dex>

---

## 📦 Pendiente de publicar

- [ ] **Empujar la etiqueta `fork-v2.1.0`.** Está creada en local sobre `de3da40c`, y
      hasta que suba, [ansible/inventories/ejemplo/group_vars/all.yml](ansible/inventories/ejemplo/group_vars/all.yml)
      fija `ghcr.io/rasty94/dex:fork-v2.1.0`, una imagen que todavía no existe. El push
      del tag dispara `release.yaml`, que la construye y la publica en GHCR: por eso se
      hace aparte y con permiso, no de corrido con el `master`.
      Lo que destapó la revisión: el inventario llevaba pinchada `fork-v2.0.0`, 75
      commits por detrás, así que el ejemplo documentado desplegaba un binario anterior
      a todo Valkey. El rol le escribía `addresses`, `mode` y `masterSet` y el propio
      campo lápida de `pkg/valkey` los rechazaba. Un pin correcto en la forma —etiqueta
      inmutable, nunca `latest`— apuntando al sitio equivocado no lo delata nadie:
      solo aparece comparando el tag con `git describe`.

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
- [ ] **La conexión con Valkey no presenta certificado de cliente.** El bloque
      `tls:` de `pkg/valkey` solo tiene `caCert` e `insecureSkipVerify`: cifra el
      viaje y verifica al servidor, pero quien autentica al cliente es la
      contraseña, no un certificado. Esta nota venía dentro de la entrada «Valkey
      es hoy un punto único de fallo», que se cerró al entregar sentinel y
      cluster; la mitad del certificado de cliente sigue abierta y por eso vuelve
      aquí sola. **La otra mitad, que hay que cambiar a la vez**: el rol de
      Ansible pone `tls-auth-clients no` en `managed.conf.j2` y en
      `sentinel.conf.j2` justamente porque el cliente no sabe presentar
      certificado —con el valor de fábrica (`yes`) el servidor lo exige y dex no
      llega a conectar—. Quien añada certificados de cliente a `pkg/valkey`
      tiene que quitar esas dos líneas y hacer que el rol copie también un
      certificado a los nodos de dex y del panel; con una sola de las dos
      mitades, el despliegue deja de arrancar.
- [ ] **En topología cluster, el bus queda sin autenticar.** Es la consecuencia
      con filo del `tls-auth-clients no` de la entrada de arriba, y merece
      seguirse aparte porque no se arregla igual: los nodos se autentican entre
      sí con `requirepass`, pero el **bus del cluster** —el puerto del nodo más
      10000, por donde viaja el gossip y la migración de slots— no tiene
      equivalente de `requirepass`. Su única autenticación posible es el
      certificado de cliente que `tls-cluster yes` exigiría, y que acabamos de
      desactivar para que dex pudiera conectar. Cifrado sigue estando; autenticado
      no. Sale a la luz solo en `valkey_topology: cluster`: standalone y sentinel
      no tienen bus. La rebaja no empeora nada de lo que ya funcionaba —ese camino
      no arrancaba antes— pero antes de poner un cluster en una red donde no confíes
      del todo, esto es lo que hay que cerrar, y se cierra con la mitad de arriba.
- [ ] `Client.Key` en `pkg/valkey/valkey.go` no tiene más uso que su propio
      test: cada componente que necesita una clave pasa por `HashKey`. Un
      ayudante de clave sin hashear y sin usuarios invita a que alguien meta un
      secreto en el nombre de una clave donde no toca. Retirarlo, o darle un
      uso real, antes de que alguien lo use mal.
- [ ] La afirmación de que dex se niega a arrancar con un `cacheTTL` inválido
      no es cierta con el feature flag `continue_on_connector_failure` activo
      (su valor por defecto): ahí el conector falla, dex lo registra y arranca
      igual sin él. La documentación del conector Keystone y el CHANGELOG
      deberían decirlo explícitamente en vez de dar a entender que el arranque
      siempre se detiene.

### 3. ☁️ Ecosistema Cloud Native e Integraciones

- [ ] Provider para HashiCorp Vault: Leer el `adminPassword` y los app-credentials nativamente de Vault sin exponerlos en el `config.yaml`.
- [ ] Helm Chart u Operator Kubernetes Mejorado: Adaptar configuraciones del Fork directamente en los values nativos del chart oficial de la comunidad.
- [ ] **Sondas que digan la verdad.** El orden de arranque está resuelto por dentro
      —`newAuthenticatorWithRetry` en [main.go:62](cmd/dex-dashboard/main.go#L62) reintenta
      el discovery, así que el panel puede levantarse antes que dex— pero las sondas no
      sirven para lo que un orquestador necesita: el `/healthz` del panel
      ([main.go:75](cmd/dex-dashboard/main.go#L75)) devuelve 200 siempre, sin mirar si la
      conexión gRPC con dex o el Valkey de las sesiones responden. Como sonda de vida está
      bien; como sonda de disponibilidad miente, y el orquestador manda tráfico a un panel
      donde todas las páginas van a fallar. Falta además decir qué hacer cuando Valkey no
      está: dex se niega a arrancar, que es lo correcto, pero hay que reintentar en vez de
      darlo por muerto. Sigue abierta con el rol de Ansible desplegado: ver
      [despliegue-ansible.md](documentacion/despliegue-ansible.md#qué-no-cubre-este-despliegue).
- [ ] **Fases 3, 4 y 5 del despliegue en alta disponibilidad**, con sus decisiones ya
      tomadas pero sin spec propia todavía —el rol de Ansible de la fase 2 ya está en
      [DONE.md](DONE.md)—, ver la
      [hoja de ruta](documentacion/specs/2026-09-03-despliegue-hoja-de-ruta.md):
      - **Fase 3 — el balanceador**: HAProxy más keepalived (VRRP entre dos nodos, no
        un balanceador de balanceadores).
      - **Fase 4 — MariaDB en alta disponibilidad**: primario y réplica con
        replicación semisíncrona, ProxySQL como punto de entrada, orchestrator para
        detectar la caída y promover. **Galera descartado, y no por gusto**: dex fija
        `transaction_isolation = SERIALIZABLE` en cada conexión
        ([storage/sql/config.go:236](storage/sql/config.go#L236) y
        [storage/ent/mysql.go:53](storage/ent/mysql.go#L53)) y Galera solo garantiza
        REPEATABLE READ.
      - **Fase 5 — la colección de Ansible publicable**, al final porque no se puede
        empaquetar lo que aún no existe.
      - **El camino `ent` con MariaDB queda fuera y no soportado**: el feature flag
        `DEX_ENT_ENABLED` (apagado por defecto) usa `atlas`, que hace sondeo de
        versión, y MariaDB devuelve una cadena compuesta
        (`5.5.5-10.11.2-MariaDB`) que atlas no interpreta como MySQL.

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
- [ ] **Cerrar una sesión de administrador concreta desde el panel.** Quitarle los
      permisos a alguien ya funciona sin esperar a que caduque —el permiso se recalcula
      en cada petición, ver [DONE.md](DONE.md)—, pero eso resuelve la baja, no la cookie
      robada: para terminar *una* sesión sin tocar la configuración sigue habiendo que
      borrar la clave a mano en Valkey. Falta la lista de sesiones abiertas del propio
      panel y un botón por fila.
