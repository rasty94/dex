# TODO — Dex Fork (rasty94/dex)

> Solo trabajo pendiente. Lo ya entregado vive en [DONE.md](DONE.md), incluida la
> tabla de estado y la sincronización con upstream, cerrada por completo: el re-port
> sobre el layout nuevo es ya la línea principal del fork, publicada como `fork-v2.0.0`.
>
> Lo que el re-port trajo apagado tras feature flags —sesiones de navegador, API de
> sesiones e identidades, MFA nativo— está decidido y encendido; también en
> [DONE.md](DONE.md).
>
> Última actualización: 2026-09-06
> Imagen Docker: `ghcr.io/rasty94/dex:latest`
> Repositorio: <https://github.com/rasty94/dex>

---

## 📦 Pendiente de publicar

- [ ] **Empujar la etiqueta `fork-v2.1.0`.** Está creada en local sobre el merge del
      TLS mutuo de Valkey —se movió ahí a propósito, para que la primera imagen
      publicada no lleve el bus del cluster sin autenticar—, y
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
- [ ] **Un primario de MariaDB que reinicia vuelve en solo lectura, y nada lo
      repone solo.** `read_only=ON` en el arranque es de fichero, no un estado que
      orchestrator recuerde: tras un `docker start` o un reinicio del primario,
      ningún nodo queda escribible. Orchestrator no tiene ninguna política para un
      master read-only —`set-writeable` es una orden manual de su CLI/API,
      comprobado contra el binario, `orchestrator -c help` y 30 s de sondeo en
      vivo—, y el rol tampoco lo repone porque no promueve sobre datos existentes,
      por diseño. No se arregla en esta fase porque automatizarlo mal —reponer
      `read_only=OFF` sin saber si hubo bifurcación— es justo el riesgo que la
      spec de la fase 4 (§4.3) decide no correr: seguridad antes que
      disponibilidad. Paso del operador, con la orden exacta, en
      [despliegue-ansible.md](documentacion/despliegue-ansible.md#lo-que-el-operador-tiene-que-hacer-a-mano).
- [ ] **`RecoveryPeriodBlockSeconds: 3600` de orchestrator bloquea una segunda
      promoción de MariaDB dentro de la hora** si la anterior no está confirmada,
      aunque el fallo nuevo sea sobre el nodo contrario y legítimo. Medido: **2 min
      34 s** bloqueado hasta un `ack-all-recoveries` manual; confirmada de
      antemano, la promoción siguiente tardó los mismos **~3 s** de siempre. No se
      baja el valor a ciegas: encadenar promociones sin ninguna ventana de por
      medio es el riesgo contrario. Candidato: acuse automático, o una alarma
      sobre `api/audit-recovery` cuando aparezca una recuperación con
      `Acknowledged: false`.
- [ ] **El primario viejo de MariaDB que vuelve tras un failover queda
      huérfano.** Sin `read_only=OFF` (punto de arriba) y sin replicar:
      orchestrator no lo reengancha, y el rol tampoco porque no clona sobre datos
      existentes. Observado cinco veces durante las pruebas de la fase 4. Paso del
      operador: `gtid_slave_pos` + `CHANGE MASTER` si no divergió del nuevo
      primario; `mariadb-dump`/`mariabackup` si sí.

### 3. ☁️ Ecosistema Cloud Native e Integraciones

- [ ] Provider para HashiCorp Vault: Leer el `adminPassword` y los app-credentials nativamente de Vault sin exponerlos en el `config.yaml`.
- [ ] Helm Chart u Operator Kubernetes Mejorado: Adaptar configuraciones del Fork directamente en los values nativos del chart oficial de la comunidad.
- [ ] **El panel no sabe servirse bajo un prefijo.** Registra todas sus rutas en la
      raíz ([main.go:74](cmd/dex-dashboard/main.go#L74) y siguientes) y `baseURL` solo
      construye el `redirect_uri` de OIDC
      ([auth.go:190](cmd/dex-dashboard/auth.go#L190)). Publicado bajo
      `https://sso.interno/dex-dashboard` recibe `/dex-dashboard/callback`, cae en su
      catch-all `"/"`, arranca un login, dex devuelve al callback y vuelta: **bucle
      infinito de redirecciones**, del que solo lo saca el limitador de intentos —11
      por login, medido—. La fase 3 lo esquivó dándole un nombre propio y repartiendo
      por `Host`, que funciona y cuesta un nombre DNS más y un certificado que cubra
      los dos. Cerrarlo de verdad es registrar las rutas bajo la ruta del `baseURL` y
      que cada redirección y cada enlace de plantilla la lleven. Lo que destapó
      probarlo: quitar el prefijo en el balanceador arregla la **entrada** y no la
      salida — el callback completa y el panel redirige a `/`, sin prefijo.
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
- [ ] **Fase 5 del despliegue en alta disponibilidad: la colección de Ansible
      publicable**, con sus decisiones ya tomadas pero sin spec propia todavía —las
      fases 1 a 4 están en [DONE.md](DONE.md)—, ver la
      [hoja de ruta](documentacion/specs/2026-09-03-despliegue-hoja-de-ruta.md). Al
      final porque no se puede empaquetar lo que aún no existe.
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
