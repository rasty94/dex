# TODO — Dex Fork (rasty94/dex)

> Solo trabajo pendiente. Lo ya entregado vive en [DONE.md](DONE.md), incluida la
> tabla de estado y la sincronización con upstream, cerrada por completo: el re-port
> sobre el layout nuevo es ya la línea principal del fork, publicada como `fork-v2.0.0`.
>
> Lo que el re-port trajo apagado tras feature flags —sesiones de navegador, API de
> sesiones e identidades, MFA nativo— está decidido y encendido; también en
> [DONE.md](DONE.md).
>
> Última actualización: 2026-10-11
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
      **Superado por upstream (revisado el 2026-10-11):** `901d6d29` (2026-09-24) cambia el
      mismo `alg` a `"RS256"` al subir go-oidc a 3.21, sin pasar por nuestro PR. Queda
      cerrarlo con una nota que enlace su commit, y para eso hace falta permiso, porque es
      escribir en el repo público. Nuestro fixture sigue con el fallo: ver la sincronización
      de abajo.

---

## 🔄 Sincronización con upstream — pendiente

> Revisado el 2026-10-11: **53 commits** de `upstream/master` (hasta `ef96b8ad`) que no
> están en `master` desde la base `ab64ed77` (2026-08-06), sin ninguno aplicado. Aquí
> va solo lo que upstream tiene **mejor** que nosotros; lo que no nos toca (la
> refactorización de `signer/rotation.go`, los ajustes de `conformance`) no se apunta.
> Un `git merge-tree` en seco da conflicto en solo cuatro ficheros: `api/v2/api.pb.go`
> (se regenera), `go.mod`, `go.sum` y `cmd/dex/serve_test.go`, donde los dos lados
> añadieron tests. La primera opción es un merge, como el bloque F, no ir eligiendo
> commits sueltos.

- [ ] **gRPC con dos vulnerabilidades que nos alcanzan.** `govulncheck ./...` las marca
      como alcanzables desde nuestro código: GO-2026-6443 (panic por una petición sin
      `:authority`/`Host`, arreglada en 1.82.2) y GO-2026-6348 (OOM por fragmentar
      frames DATA de HTTP/2, arreglada en 1.83.1). Estamos en `grpc v1.82.1`, y en
      `v1.82.0` en `api/v2/go.mod`; upstream ya está en `v1.84.0` en los dos. Por ahí entra
      la API de gestión (`5557`), así que esto va primero aunque el resto espere.
- [ ] **Vulnerabilidades que upstream tampoco ha cerrado.** El mismo `govulncheck`
      encuentra doce más en la stdlib (`net/http`, `crypto/tls`, `net/textproto` y
      `html/template`, este último usado por el login y por el panel) y en
      `golang.org/x/net v0.57.0`. Se arreglan en Go 1.27.2 y x/net 0.60.0, y upstream va
      por 1.27.1 y 0.59.0. Ojo: el análisis corrió con el Go local (1.27.1), pero la
      imagen se compila con `golang:1.26.4`, así que la rama 1.26 hay que mirarla aparte.
      Este punto **no** se cubre con el merge.
- [ ] **El toolchain está desalineado consigo mismo.** `go.mod` dice `go 1.25.8`, CI
      instala 1.25 y el `Dockerfile` compila con 1.26.4: tres versiones distintas, y la
      imagen publicada no la ha compilado nunca CI. Upstream lo tiene todo en 1.27
      (`go 1.27.0` en `go.mod`, CI y `golang:1.27.1-alpine3.24`).
- [ ] **CI lintea con golangci-lint 2.4.0.** El bloque C de [DONE.md](DONE.md) dejó los
      tres pins en 2.13.1, pero el re-port lo perdió en el `Makefile`, que es el que usa CI:
      `pre-commit` va en 2.13.1, `Makefile` en 2.4.0 y el binario de `bin/` en 2.10.1.
      Upstream pone 2.13.1 en el `Makefile` y cambia el instalador a
      `golangci-lint.run/install.sh`.
- [ ] **Refresh tokens rechazados devuelven `invalid_request`.** `refreshLookupError`
      ([server/grants/refresh.go](server/grants/refresh.go)) contesta `invalid_request`
      cuando el token no existe o está caducado. RFC 6749 §5.2 pide `invalid_grant`, y los
      clientes miran ese código para lanzar un login nuevo, así que con `invalid_request`
      se quedan atascados. Upstream `6a242200`; además, el «not found» se registra a nivel
      info.
- [ ] **El conector OIDC ignora `rootCAs` si hay `providerDiscoveryOverrides`.**
      `getProvider` ([connector/oidc/oidc.go](connector/oidc/oidc.go)) crea el proveedor
      con `context.Background()`, que pierde el cliente HTTP con la CA propia, y el JWKS
      se valida contra las raíces del sistema. Rompe un IdP con CA privada que además
      sobrescriba endpoints. Upstream `ee3dd331`.
- [ ] **TLS del servidor web sin cifrados ni curvas configurables.** Nosotros fijamos la
      lista `allowedTLSCiphers` en `serve.go`; upstream añade `web.tlsCiphers` y
      `web.tlsCurvePreferences` (con las curvas híbridas post-cuánticas ML-KEM), los valida
      en `Validate` y rechaza los cifrados de TLS 1.3 porque no son configurables (`9955b44d`,
      `4ccb2bd2`, `c8d4482f`). Al traerlo, documentarlo en `documentacion/` y en el rol de
      Ansible si este expone `web:`.
- [ ] **Contraseñas sin grupos en la API.** Upstream añade `Password.groups = 5` y
      `UpdatePasswordReq.new_groups = 4`, normalizados (ordenados, sin duplicados, sin
      vacíos) (`cf3fd9af`, `48690eeb`, `a1c8a418`). Tiene la misma trampa que
      `UpdateClient`: los grupos se reemplazan pero no se vacían. Los números de campo
      quedan cogidos para cualquier cosa nuestra en esos mensajes. Cuando entre, el panel
      podrá enseñar y editar grupos de los usuarios locales.
- [ ] **El GC de Kubernetes falla si otra réplica ya ha borrado el objeto.** Con varias
      réplicas sobre almacenamiento `kubernetes`, un `ErrNotFound` cuenta como error del GC
      (`785a6068`). Afecta poco a nuestro despliegue (Valkey y SQL), pero cuesta poco traerlo.
- [ ] **El log de «login successful» no dice el cliente.** Upstream añade `client_id`
      (`d249ff9b`). Viene bien para el visor de intentos de login de la sección 6.
- [ ] **El `Dockerfile` descarga gomplate con `wget` desde GitHub.** Upstream lo copia de
      `ghcr.io/hairyhenderson/gomplate:v5.2.0@sha256:…` (`fbcc48cb`), así que queda fijado
      por digest y Dependabot lo vigila; también borra `scripts/update-gomplate`. Las bases
      `alpine:3.24.2` y `distroless` también tienen digest nuevo.
- [ ] **Dependencias por detrás.** Además de gRPC: x/crypto 0.54→0.57, go-jose
      4.1.4→4.1.5, goxmldsig 1.6.0→1.6.1 (firma SAML), etree 1.7→1.8.1, go-oidc
      3.19→3.21, webauthn 0.17.4→0.18.2, etcd 3.6→3.7, go-sqlite3, mysql, openbao y
      protobuf. **go-oidc 3.21 y webauthn 0.18 rompen tests nuestros**: el fixture de
      `connector/oidc/oidc_test.go` sigue publicando `"alg": jwk.Algorithm` (lo de
      #4986), y los tests MFA usan `127.0.0.1` como RP ID (cinco usos en
      `server/mfa/*_test.go`), que webauthn 0.18 rechaza. Upstream lo arregla en `901d6d29`.
- [ ] **OpenShift: `clientSecretFile`.** Lee el secreto de un token de service account
      montado como volumen (`1db0699c` y siguientes). No lo usamos, pero viene con el merge
      sin coste.

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
- [ ] **`ignore_unreachable` de la detección de estado (`replicacion.yml`) no
      tiene una prueba real con un nodo UNREACHABLE de verdad.** Lo que se probó
      en la tanda de arreglos es un contenedor parado en la misma máquina
      (`docker stop`), que el módulo `docker_container_exec` trata como un fallo
      sin `rc` — mismo resultado práctico para `mariadb_escribibles`, pero no es
      lo mismo que una máquina inalcanzable por red (SSH/conexión caída de
      verdad), que es el caso que `ignore_unreachable: true` existe para cubrir.
      Ligado: desplegar un cambio de plantilla con la réplica caída o muda deja
      el primario en solo lectura sin que orchestrator ni una segunda pasada del
      playbook lo reparen solos — paso A de
      [despliegue-ansible.md](documentacion/despliegue-ansible.md#lo-que-el-operador-tiene-que-hacer-a-mano).
- [ ] **HAProxy con `timeout server 1h` en `be_mysql`, sin `option clitcpka`/
      `srvtcpka` de por medio, ni un `connMaxLifetime` explícito en el lado de
      dex.** Una conexión del pool de `database/sql` puede quedar viva más de una
      hora sin que nadie la cierre por ninguno de los dos lados si el tráfico es
      escaso; una keepalive TCP en HAProxy o un `connMaxLifetime` menor que 1h en
      dex acotarían esa ventana en vez de depender solo del timeout de HAProxy.
- [ ] **El assert de `mariadb_bind_address` (rol `mariadb`) no tolera espacios
      tras la coma ni direcciones IPv6 (`::`, `::1`).** `'127.0.0.1' in (valor |
      split(','))` es una comparación de cadena exacta: `"127.0.0.1, 10.0.0.1"`
      (con espacio) o un bind por `::` no se reconocen como "incluye el
      loopback" y el assert falla aunque el valor sea funcionalmente correcto.
      Falla en el lado seguro (no arranca en vez de arrancar mal), así que no es
      urgente, pero merece un `trim` por elemento y una nota sobre IPv6 si algún
      despliegue lo necesita.

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
- [ ] **El camino `ent` con MariaDB queda fuera y no soportado**: el feature flag
      `DEX_ENT_ENABLED` (apagado por defecto) usa `atlas`, que hace sondeo de
      versión, y MariaDB devuelve una cadena compuesta
      (`5.5.5-10.11.2-MariaDB`) que atlas no interpreta como MySQL.
- [ ] **`meta/argument_specs.yml` por rol de la colección `rasty94.dex`.** Es la
      documentación formal de cada variable y da validación en tiempo de
      ejecución; hoy esa documentación vive en los comentarios de
      `defaults/main.yml` de cada rol, que es donde quien edita la variable la
      lee. Quedó fuera de la fase 5 a propósito: son ocho roles y decenas de
      variables, y ninguna de ellas cambia por empaquetar.
- [ ] **CI no renderiza el inventario de ejemplo de la colección, solo hace
      `--syntax-check`.** `--syntax-check` comprueba estructura, YAML y que
      cada rol y el playbook resuelvan por FQCN en la colección instalada; no
      renderiza ninguna plantilla, así que un `bind_address` o un `server`
      mal escritos no los cazaría. Se quedó fuera de CI (tarea 4, paso 6) por
      lo que la propia tarea demostró al intentarlo: `--check --diff` con
      `community.docker.docker_compose_v2` no es un modo de comprobación de
      verdad para ese módulo — el intento real levantó dos contenedores
      `mariadb`/`mariadb-probe` de verdad, con los nombres por defecto del
      rol, en vez de simularlos. Hace falta un entorno donde
      `docker_compose_v2` no arranque nada bajo `--check` antes de meter este
      render en un runner de GitHub Actions.
- [ ] **El hook `fqcn-en-playbooks` de `pre-commit` no cubre `import_role` ni
      `include_role` dentro de `tasks:`.** Solo entiende `roles: [a, b]` en
      una línea; un `- role: x` bajo un `roles:` en bloque ya falla (la
      forma en bloque entera está prohibida, no se llega a mirar la lista),
      así que el hueco real son esos dos módulos. Otro hueco real: `roles: [`
      con el `]` en la línea siguiente pasa en silencio (el awk solo mira el
      contenido entre corchetes de la misma línea). Hoy no hace falta más:
      `ansible/playbooks/site.yml` es el único playbook de la colección y no
      usa ninguno de los dos.

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

### 7. 🧭 Revisión de cómo funciona la API gRPC

> La API de gestión ha ido creciendo a base de añadidos: lo de upstream, lo del re-port y
> lo que fue pidiendo el panel, y cada pieza sigue su propia convención. Antes de
> añadirle más campos (los grupos de contraseñas de upstream, la paginación de la
> sección 6) toca revisar el conjunto y fijar unas reglas. La salida es un documento en
> `documentacion/` con las convenciones y una lista de cambios, no un refactor directo.
> Restricción: `api/v2/api.proto` es compartido con upstream, así que todo lo que se
> cambie tiene que seguir siendo compatible en el cable y no puede ocupar números de
> campo que upstream vaya a usar, o cada sincronización acabará en conflicto.

- [ ] **Errores sin código gRPC.** `server/apiserver` devuelve 96 errores con
      `errors.New`/`fmt.Errorf` y ni un `status.Error`, así que todos llegan como
      `codes.Unknown`. Un argumento inválido, un fallo de almacenamiento y un permiso
      denegado no se distinguen salvo leyendo el texto. Lo de «no existe» y «ya existe»
      va por otro lado, en booleanos `not_found`/`already_exists` dentro de la respuesta
      (17 en el proto), y de esos depende el panel
      ([cmd/dex-dashboard/dex.go](cmd/dex-dashboard/dex.go)). Decidir si se pasa a
      `codes.InvalidArgument`/`NotFound`/`Internal` sin romper los booleanos que upstream
      sigue usando.
- [ ] **Tres formas de vaciar un campo.** Las listas repetidas no se pueden vaciar,
      porque una lista vacía no viaja por protobuf: pasa en `UpdateClient` y, cuando
      entre, en `UpdatePasswordReq.new_groups`. Otros campos son `optional`
      (`backchannel_logout_uri`, `refresh_token_lifetime`), y `new_grant_types` va
      envuelto en un mensaje. Elegir una forma (wrapper, `optional` o `FieldMask`) para
      todo lo nuevo, y decidir si las listas que ya existen necesitan poder vaciarse.
- [ ] **Validación y normalización desiguales.** Upstream normaliza los grupos de
      contraseña (ordena, quita duplicados y rechaza vacíos); falta mirar si `redirect_uris`,
      `trusted_peers`, scopes de conector y demás listas se tratan igual, y si la
      validación vive en la API o se repite en el panel.
- [ ] **Listados sin paginar.** `List*` devuelve todo de una vez; es el bloqueo de la
      paginación del panel (sección 6). Ver si cabe `page_size`/`page_token` sin chocar
      con upstream.
- [ ] **Autorización y auditoría por método.** Comprobar que cada RPC, también las del
      re-port (sesiones, identidades, MFA y purga RGPD), pasa por el mismo control de
      token con nombre y deja `caller` en la auditoría, y que no hay ninguna que se lo salte.
