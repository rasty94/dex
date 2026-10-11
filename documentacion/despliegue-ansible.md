# Despliegue con Ansible — dex, el panel y Valkey en alta disponibilidad

Colección en `ansible/` que instala y arranca dex, el panel de administración y Valkey
sobre máquinas Docker, en las tres topologías de Valkey. Cierra las entradas «Rol de
Ansible», «Un `docker compose` de producción» y «Valkey es hoy un punto único de fallo»
de [DONE.md](../DONE.md). Es la fase 2 de la
[hoja de ruta del despliegue](specs/2026-09-03-despliegue-hoja-de-ruta.md); su spec
completa está en
[2026-09-03-despliegue-ansible-valkey-ha.md](specs/2026-09-03-despliegue-ansible-valkey-ha.md).

## Requisitos

### En los nodos de destino

- **Un usuario con `sudo`.** Las tandas de `valkey`, `dex` y el panel llevan
  `become: true`: escriben en `/etc/valkey`, `/etc/dex` y `/etc/dex-dashboard`, con
  propietarios que son los usuarios de dentro de los contenedores (999, 1001), y hablan
  con el demonio de Docker. La tanda de la CA interna **no** escala, a propósito: solo
  escribe en el nodo de control, y la clave privada de la CA no tiene por qué acabar
  siendo de root allí. Un inventario que no necesite escalar —el converge local, que
  escribe dentro del propio árbol de trabajo— lo apaga con `ansible_become: false` en
  sus hosts, que es variable de conexión y gana a la directiva de la tanda.
- **Docker**, con el plugin `compose`. El rol usa
  `community.docker.docker_compose_v2` y `community.docker.docker_container_exec`; no
  instala Docker por sí mismo.
- **MariaDB o MySQL alcanzable desde los nodos del grupo `dex`.** Puede ser el par
  primario/réplica que despliega el propio rol `mariadb` (fase 4, entregada — ver
  «MariaDB en alta disponibilidad» más abajo) o una base ajena ya existente; el rol
  `dex` solo comprueba que responde antes de arrancar, no la administra por sí mismo.

### En el nodo de control

- **Ansible**, con las colecciones `community.docker` y `community.crypto` (esta
  última genera la CA interna con `openssl_privatekey`/`openssl_csr`/
  `x509_certificate`). Son `dependencies` de `ansible/galaxy.yml`: se instalan solas
  junto con la colección.
- **El paquete Python `requests`.** Lo necesita
  `community.docker.docker_container_exec`, que el rol `valkey` usa para preguntar por
  el master de sentinel y para formar y comprobar el cluster. Sin él el módulo falla
  con `Failed to import the required Python library (requests)`, y nada en este
  repositorio lo documentaba hasta que la verificación de la Task 11 lo encontró en
  vivo.

`ansible/` es la colección `rasty94.dex` (fase 5 del despliegue, entregada; spec en
[2026-09-07-coleccion-ansible.md](specs/2026-09-07-coleccion-ansible.md)). Hay dos
formas de tenerla, y las dos invocan el mismo playbook con el mismo nombre
(`rasty94.dex.site`) y los mismos roles por FQCN (`rasty94.dex.mariadb`,
`rasty94.dex.dex`…):

**Instalada, sin este repositorio.** Fijada por etiqueta, en un `requirements.yml`:

```yaml
collections:
  - name: https://github.com/rasty94/dex.git#ansible/
    type: git
    version: fork-v2.2.0
```

```bash
ansible-galaxy collection install -r requirements.yml
```

O desde el tarball adjunto a la release de GitHub:
`ansible-galaxy collection install rasty94-dex-2.2.0.tar.gz`.

**Desde el checkout**, que es como se prueba todo en esta rama y como converge el
entorno local. Esta rama **no instala Ansible en la máquina**: vive en un venv dentro
del propio árbol de trabajo (`.venv/`, en `.gitignore`), para no tocar el intérprete de
Python del sistema. Así se montó y así hay que reproducirlo:

```bash
python3 -m venv .venv
.venv/bin/pip install ansible-core==2.21.3 ansible-lint==26.8.0 requests
# Las dos colecciones estan declaradas en ansible/requirements.yml
.venv/bin/ansible-galaxy collection install -r ansible/requirements.yml
```

Sin `-p`: van a `~/.ansible/collections`, compartido con cualquier otro proyecto de
Ansible en la máquina, no al árbol de trabajo. Lo que sí es del árbol de trabajo es
`ansible/.collections/ansible_collections/rasty94/dex`, un enlace simbólico versionado
que apunta a `../../..` (el propio `ansible/`). Con ese enlace y con `collections_path`
en el `ansible.cfg` de la raíz, cualquier comando `ansible-playbook`/`ansible-lint`
lanzado **desde la raíz del repositorio** resuelve `rasty94.dex.*` contra el propio
checkout, sin instalar nada de la colección. `roles_path` no existe en `ansible.cfg`:
los roles se nombran por FQCN y solo por FQCN, en el checkout igual que instalados.

Ese `ansible/requirements.yml` es la lista de colecciones en forma de fichero en vez de
en prosa; el hook de `ansible-lint` de `pre-commit` **no** se apoya en él —lo explica su
propio comentario en `.pre-commit-config.yaml`— sino en el paquete `ansible` como
`additional_dependencies`, porque `ansible-lint` busca el `requirements.yml` en el
directorio de trabajo y no en el proyecto que detecta.

Todos los comandos de este documento asumen `.venv/bin/` por delante y que se lanzan
**desde la raíz del repositorio**: es de ahí de donde `ansible.cfg` se recoge solo, y
también de donde hay que correr `ansible-lint` — desde dentro de `ansible/` no ve
`ansible.cfg`, no resuelve el FQCN y cambia los hallazgos.

Si además hay una copia de `rasty94.dex` instalada en `~/.ansible/collections`,
`ansible-lint` avisa "only the first one will be used" y resuelve contra la instalada,
no contra el checkout — `ansible-playbook` sí toma el checkout, porque `ansible.cfg`
antepone `collections_path` a las rutas por defecto y ahí solo está el enlace
`.collections`. Para lintear el checkout con esa copia instalada presente:
`ANSIBLE_COLLECTIONS_PATH=ansible/.collections ansible-lint ansible/`.

## El inventario y sus cuatro grupos

Ejemplo completo en `ansible/inventories/ejemplo/hosts.yml` (con direcciones del rango
de documentación RFC 5737, para que nadie las confunda con una máquina real) y sus
secretos en `ansible/inventories/ejemplo/group_vars/all.yml`.

**Con la colección instalada, copia ese inventario fuera de ella antes de usarlo.**
Vive dentro de `~/.ansible/collections/ansible_collections/rasty94/dex/inventories/ejemplo/`,
y apuntar `-i` ahí escribiría `internal_ca_dir` —que se ancla junto al inventario— dentro
de esa misma ruta: la próxima actualización de la colección se la lleva por delante,
justo lo que sacar la CA del repositorio quería evitar.

- **`valkey`** — las máquinas que corren un `valkey-server` propio. Llevan
  `valkey_topology` (`standalone`, `sentinel` o `cluster`) y, en sentinel,
  `valkey_master_set`.
- **`valkey_sentinel`** — los votantes del quorum de sentinel. **Deliberadamente
  separado de `valkey`**: un sentinel es un proceso ligero que no necesita su propio
  Valkey de datos, así que el tercer voto —el que hace falta para tener mayoría con
  solo dos máquinas de datos— puede vivir en un nodo del grupo `dex` en vez de exigir
  una tercera máquina de Valkey. Esa separación es la razón por la que el diseño entero
  funciona con dos máquinas de datos: sin ella, sentinel necesitaría tres máquinas de
  Valkey completas solo para poder tener quorum.
- **`dex`** — con `dex_issuer` (la URL del balanceador, nunca la del nodo) y
  `dex_storage` (tipo, host, puerto, base, usuario y `ssl_mode`).
- **`dex_dashboard`** — con `dex_dashboard_base_url` y las listas de administradores
  (`dex_dashboard_admin_emails`, `dex_dashboard_admin_groups`, …).

`ansible_host` va en todos los hosts: el rol `valkey` lo lee en `hostvars[h]` para
anunciar direcciones reales en sentinel y para el `--cluster create` del cluster, y la
plantilla de dex lo usa para construir `valkey.addresses`.

## Las tres topologías

Se eligen con `valkey_topology` en el grupo `valkey` del inventario:

- **`standalone`** — una sola máquina en el grupo `valkey`, sin `valkey_sentinel`. Para
  un despliegue de una réplica de dex, o para probar el rol.
- **`sentinel`** — un master y sus réplicas en el grupo `valkey`, más los votantes en
  `valkey_sentinel` (al menos tres, número impar; el rol falla al arrancar si no lo es,
  en vez de montar un failover que no vota el día que hace falta). Es la topología para
  dos máquinas de datos con un tercer voto en un nodo de dex, como trae el inventario
  de ejemplo.
- **`cluster`** — tres máquinas en el grupo `valkey`, cada una con dos procesos
  (`valkey_port` y `valkey_replica_port`, 6379 y 6380 por defecto): un master y la
  réplica de otro master, para que ninguna réplica caiga en la máquina de su propio
  master. Para volumen de datos que no cabe en una sola máquina; para solo tolerar la
  caída de un nodo, sentinel es más simple.

Detalle importante de por qué el rol es idempotente de verdad y no solo «vuelve a dejar
el estado inicial»: `managed.conf` lo reescribe Ansible en cada pasada (puertos, TLS,
credenciales), pero `valkey.conf` y `sentinel.conf` se crean **una sola vez**
(`force: false`) e incluyen a `managed.conf` en su primera línea — porque Valkey
reescribe su propia configuración (`CONFIG REWRITE` en cada failover, `nodes.conf` del
cluster) y un rol que sobrescriba esos ficheros le devuelve al nodo un `replicaof`
caducado, deshaciendo la promoción que acababa de pasar.

### Aplicar las SAN del balanceador reinicia todos los dex

`internal_ca_extra_sans` cambia el certificado de cada nodo de dex, y
`community.crypto` lo reemite: cambia la huella y el handler reinicia el contenedor. Es
lo que se quiere —sin ese certificado el panel no conecta por el balanceador— pero
conviene saberlo antes: **la primera pasada con esa variable reinicia dex en todos los
nodos**, de uno en uno por el `serial: 1`. Comprobado que con la misma lista otra vez
sale `changed=0`, así que no reinicia en cada despliegue.

### Lo que un nodo ya desplegado no recibe

La otra cara de esa regla, y conviene saberla **antes** de que muerda: como `valkey.conf`
y `sentinel.conf` se crean con `force: false`, un nodo que ya está desplegado **no recibe
por sí solo los cambios que se hagan después en esas plantillas**. Volver a lanzar el
playbook no basta, y no avisa de nada: la tarea sale `ok` porque el fichero ya existe.

Cómo queda cada fichero:

| Fichero | ¿Llegan los cambios de la plantilla? |
| --- | --- |
| `managed.conf` | Sí, se reescribe en cada pasada y el handler reinicia el contenedor |
| `valkey.conf` | Solo por el `include` de la primera línea, y **lo que el propio Valkey haya escrito después gana**: un `CONFIG REWRITE` deja sus directivas por debajo del `include`, y las posteriores mandan |
| `sentinel.conf` | **Nunca.** No incluye nada; es entero de sentinel desde que se crea |

Esto importa hoy, no en abstracto: el `tls-port`, el `tls-replication yes`, el
`tls-auth-clients` y el `requirepass` del sentinel son cambios recientes de la
plantilla. Un sentinel desplegado antes de ellos se queda **con su puerto de control en
claro y sin autenticar** hasta que alguien lo migre a mano.

El caso vivo ahora mismo es `tls-auth-clients`, que pasó de `no` a `yes`. Los nodos de
datos lo reciben solos —`managed.conf` se reescribe en cada pasada—, pero **un sentinel
ya desplegado no**: su fichero no se toca nunca, así que sigue aceptando clientes sin
certificado hasta que se migre. Se comprueba con
`docker exec <sentinel> grep tls-auth-clients /etc/valkey/sentinel.conf` y se arregla
editando esa línea y reiniciando el contenedor.

Y un aviso de ventana, para el mismo cambio: la tanda de Valkey va **antes** que la de
dex, así que los nodos de datos empiezan a exigir certificado mientras dex todavía corre
con la configuración vieja, que no lo presenta. El proceso que ya está arriba aguanta:
pierde el almacén compartido y cae a sus contadores locales hasta que le llega su turno.
El que **no** aguanta es el que reinicie dentro de esa ventana, porque `pkg/valkey.New`
hace `PING` al arrancar y sin él dex se niega a servir. Si la ventana no es aceptable,
desplegar primero solo dex y el panel (`--limit dex:dex_dashboard`) y en una segunda
pasada Valkey: el par de cliente no molesta a un servidor que todavía no lo pide.

Qué hacer, según qué se cambie:

- **Parámetros que sentinel sabe cambiar en caliente** —`down-after-milliseconds`,
  `failover-timeout`, `auth-pass`, el quorum— van por `SENTINEL SET <conjunto> <opción>
  <valor>` contra cada sentinel. Eso es lo que sentinel escribe él mismo en su fichero, y
  es el camino correcto: editar `sentinel.conf` a mano y reiniciar es lo que deshace
  failovers.
- **Puertos, TLS y `requirepass`** no se cambian en caliente. Hay que **recrear el nodo de
  forma controlada, de uno en uno**: parar el contenedor, borrar el fichero
  (`sentinel.conf`, o `valkey.conf` si un `CONFIG REWRITE` está tapando lo que se quiere
  cambiar), volver a lanzar el playbook para ese host, y **esperar a que el nodo vuelva a
  estar sano antes de tocar el siguiente** —en sentinel, que el resto del quorum lo vea; en
  cluster o en réplicas, que la replicación se haya puesto al día—. Nunca los tres a la
  vez: sin quorum no hay promoción, y ese es justo el día en que hace falta.
- **En una topología con master**, empezar por las réplicas y dejar el master para el
  final, provocando su relevo a propósito (`SENTINEL FAILOVER <conjunto>`) en vez de
  descubriéndolo al pararlo.

Un despliegue nuevo no tiene nada de esto: el fichero se crea ya con la plantilla de hoy.

## Secretos

Las contraseñas y claves van en `group_vars/all.yml`, cifrado con:

```bash
ansible-vault encrypt ansible/inventories/ejemplo/group_vars/all.yml
```

En cada nodo, Ansible renderiza el fichero de configuración de dex con modo `0600` y
propietario el usuario de dex — **el fichero de configuración es el secreto**, porque
dex no sabe leer de fichero aparte ni la contraseña de la base, ni la de Valkey, ni
`cookieEncryptionKey`; ponerlas por variable de entorno es peor, porque `docker
inspect` las enseña. El panel sí tiene `dex.tokenFile` para su token gRPC, así que ese
va en su propio fichero, también `0600`.

**La trampa del `$`.** El feature flag `expand_env` de dex viene **encendido por
defecto** y aplica `os.ExpandEnv` a todas las cadenas de la configuración, contraseñas
incluidas. Una contraseña que contenga `$` se expande en silencio y se convierte en
otra cosa —o en nada, si lo que sigue no es una variable de entorno que exista—, y dex
arranca con una contraseña que no es la que se escribió. El flag no se toca, porque es
de upstream y hay quien depende de él: la única defensa es generar contraseñas sin `$`
para todo lo que pase por el fichero de configuración de dex (`dex_storage_password`,
`dex_valkey_password`, `dex_grpc_token`, `dex_session_cookie_key`), y así lo advierte el
propio `group_vars/all.yml` de ejemplo. `dex_dashboard_oidc_client_secret` viaja igual
en el `config.yaml` del panel y tiene el mismo problema.

**La CA interna vive junto al inventario, no junto al código.** `internal_ca_dir` vale
`{{ inventory_dir }}/.internal-ca` — antes se anclaba en el propio repositorio
(`playbook_dir`), y con la colección instalada eso la hubiera mandado a
`~/.ansible/collections`, mezclada con las colecciones y borrada con ellas. Cada
inventario tiene su propia CA, que es lo correcto: dos despliegues con dos inventarios
no comparten certificados. **Migración:** quien tuviera un despliegue hecho desde el
checkout con `.internal-ca/` en la raíz del repositorio tiene que moverlo junto a su
inventario antes de volver a desplegar, o el rol emite una CA nueva y reemite todos los
certificados. El directorio está en `.gitignore` (a cualquier profundidad, no solo en la
raíz) y fuera del tarball de la colección (`build_ignore`), así que un `git add` o un
`ansible-galaxy collection build` no lo arrastran nunca.

`internal_ca_dir` exige un inventario de verdad: con `-i host1,host2,` (sin fichero, el
plugin `host_list`) `inventory_dir` vale la cadena `"None"`, y el rol se niega en su
primera tarea con un `assert` explícito en vez de dejar la CA caer en `./None/.internal-ca`
en silencio. El mensaje de fallo dice cómo seguir: fijar `internal_ca_dir` a mano, por
inventario o con `-e`.

Con una lista de hosts sueltos (`-i host1,host2,`) contra `rasty94.dex.site` el
resultado es "no hosts matched" en todas las tandas: los hosts de `host_list` no caen
en ningún grupo con nombre (`dex`, `mariadb`…), solo en `all`/`ungrouped`, y cada
`hosts:` del playbook nombra un grupo. El `assert` no protege entonces a `site.yml`
—ese caso ni siquiera llega a ejecutar el rol—, sino al rol `internal_ca` invocado
directamente o desde un playbook propio, donde `-i host1,host2,` sí es un uso real.

## Actualizar

El playbook (`ansible/playbooks/site.yml`, invocado como `rasty94.dex.site`) ordena las
tandas por dependencia — `CA interna` → `MariaDB` → `Orchestrator` → `Valkey` → `Dex` →
`Panel` → `Balanceador` — y cada una de las de aplicación lleva `serial: 1`: los hosts
de cada grupo se actualizan de uno en uno, para que una actualización no deje el
servicio entero abajo a la vez.

Cada rol se invoca por FQCN (`rasty94.dex.mariadb`, `rasty94.dex.dex`…), en el checkout
igual que instalado: no hay ninguna forma corta documentada. Es una convención de este
repositorio, no una regla que imponga ninguna herramienta estándar —ni `--syntax-check`
ni `ansible-lint` en perfil `production` rechazan `roles: [mariadb]` a secas, porque
`ansible/roles/mariadb` resuelve igual por directorio—; lo único que la hace exigible es
el hook local de `pre-commit` `fqcn-en-playbooks`, y solo entiende `roles: [a, b]` en una
sola línea: la forma en bloque (`roles:` con cada rol debajo, en su propia `- rol`) no la
lee y está prohibida a propósito, para que no pase en silencio.

La imagen se fija por variable (`dex_image`) a una etiqueta `fork-vX.Y.Z`, **nunca
`latest`**: con `serial: 1` sobre varios hosts, una etiqueta móvil puede cambiar de
versión a mitad de la tanda entre el primer host actualizado y el último.

## El balanceador

Ya no es algo pendiente: lo despliegan los roles `haproxy` y `keepalived` (fase 3), en
la última tanda del playbook. `dex_issuer` y `dex_dashboard_base_url` apuntan a él, nunca
a la de un nodo.

**El panel va en un nombre propio, no en una ruta bajo el del issuer.** Esto se corrigió
al probar el login de verdad: el panel registra todas sus rutas en la raíz y su `baseURL`
solo construye el `redirect_uri` de OIDC, así que servido bajo `/dex-dashboard` recibe
`/dex-dashboard/callback`, cae en su catch-all, arranca un login, dex devuelve al
callback y vuelta — bucle infinito del que solo lo saca el limitador de intentos. El
reparto es por cabecera `Host`: `panel.interno` al panel, todo lo demás a dex, que sí
sirve bajo la ruta de su issuer.

Consecuencia práctica: **hacen falta dos nombres DNS y un certificado que cubra los dos**
(dos SAN o un comodín). El rol no lo comprueba —no abre el certificado—, así que si no
los cubre el fallo se ve como un aviso de certificado en el navegador.

Lo que hace:

- **Repartir tráfico** entre los nodos del grupo `dex` y, por separado, entre los del
  grupo `dex_dashboard`, con `balance roundrobin`.
- **Pasar el gRPC en `mode tcp`, sin terminarlo.** El 5557 de dex ya sirve TLS con la CA
  interna y el panel lo verifica; terminarlo en el balanceador cambiaría una credencial
  que hoy no sale de la máquina —el token gRPC— por una que viaja sin cifrar. Eso obliga
  a que el certificado de cada nodo de dex lleve el nombre del balanceador entre sus
  SAN (`internal_ca_extra_sans`): el cliente marca ese nombre y la sesión TLS se negocia
  contra el nodo que toque.
- **Comprobar `/healthz`** de cada servicio antes de mandarle tráfico (ver la
  advertencia de la siguiente sección: la del panel no dice todo lo que parece decir).
- **Poner `X-Forwarded-For`** en cada petición hacia el panel, y ser el único camino
  hasta él. Ese es el trato del ajuste `dex_dashboard_trust_forwarded_for`
  (`trustForwardedFor` en el `config.yaml` del panel), que el inventario de ejemplo
  enciende **porque describe un despliegue con balanceador**: con él, el limitador de
  intentos de login cuenta por dirección real del administrador; sin él, todos comparten
  el cubo de la dirección del balanceador. Encenderlo sin balanceador delante es el
  error contrario y peor: la cabecera la elige quien llama, así que cada petición
  anónima estrena clave en el Valkey compartido, que corre con `noeviction` — llenarlo
  deja sin poder crear sesiones a los administradores. Por eso viene apagado de fábrica
  y lo declara el inventario, no el rol.
- **Ser el único camino hasta los nodos.** Es un requisito, no un consejo: HAProxy
  termina el TLS y habla en claro con `:5556` y `:5560`, así que quien alcance esos
  puertos directamente elige su propia `X-Forwarded-For`. Para eso están `dex_http_bind`,
  `dex_grpc_bind` y `dex_dashboard_bind`, que atan los puertos a la interfaz privada; el
  inventario de ejemplo los pone. Un cortafuegos delante sirve igual. Y la regla en
  HAProxy es `set-header`, no `add-header`: con `add` queda delante la cabecera que
  trajera el cliente y el panel lee esa.
- **Que la red entre el balanceador y los nodos sea de confianza.** Por dentro va en
  claro: cookies de sesión incluidas. Vale en una VLAN privada o entre máquinas del mismo
  hipervisor; no vale si esa red no lo es.
- **No hacer afinidad de sesión.** Es exactamente lo que el estado compartido en Valkey
  existe para hacer innecesario: las sesiones del panel y los contadores del limitador
  de login viven en Valkey, no en el proceso que atendió la petición anterior, así que
  cualquier nodo puede atender cualquier petición. Las dos plantillas —la de `dex` y la
  de `dex_dashboard`— renderizan el mismo bloque `valkey:` a partir de los grupos
  `valkey`/`valkey_sentinel` del inventario, con `keyPrefix` distinto para que no se
  mezclen las claves de los dos procesos.

## MariaDB en alta disponibilidad

Rol `mariadb` — primario y réplica en Docker, con TLS y replicación semisíncrona —,
el agente de sonda que dice cuál de los dos es el primario, un frontend y un backend
nuevos en el rol `haproxy` para el 3306, y el rol `orchestrator` en modo raft a tres
para detectar la caída y promover. Es la fase 4 de la
[hoja de ruta](specs/2026-09-03-despliegue-hoja-de-ruta.md); spec completa en
[2026-09-06-mariadb-alta-disponibilidad.md](specs/2026-09-06-mariadb-alta-disponibilidad.md).

### Una réplica no es una copia de seguridad

Con todas las letras, porque es la confusión más cara de este terreno: **una réplica
no es una copia de seguridad**. Replica al instante el `DROP TABLE` y el `UPDATE` sin
`WHERE`. Esta fase protege de que se muera una máquina, **no** de que alguien se
equivoque. Las copias de seguridad son trabajo propio, con su retención y sus
restauraciones probadas, y quedan fuera de este despliegue.

### Cómo saber si la semisíncrona está activa de verdad

```sql
SHOW STATUS WHERE Variable_name IN (
    'Rpl_semi_sync_master_status',    -- ON no basta
    'Rpl_semi_sync_master_clients',   -- tiene que ser >= 1
    'Rpl_semi_sync_master_no_tx');    -- transacciones SIN la garantia
```

`status: ON` con `clients: 0` dice «habilitada», no «funcionando»: la siguiente
escritura bloqueará hasta agotar `rpl_semi_sync_master_timeout` (10 s de fábrica) y
caerá a asíncrono. Medido contra un `mariadb:11.4` de verdad:

| Momento | `..._status` | `..._clients` | Qué pasa de verdad |
| --- | --- | --- | --- |
| Recién arrancado, sin réplica | `ON` | `0` | La siguiente escritura bloquea 10 s y cae a asíncrono |
| Tras una escritura sin réplica | `OFF` | `0` | Ya cayó: `Rpl_semi_sync_master_no_tx` = 1 |
| Con la réplica enganchada | `ON` | `1` | Activa: `..._yes_tx` sube, `..._no_tx` se queda en 0 |

La comprobación correcta es la pareja `status` + `clients`, nunca `status` sola. El
número que vale para una alarma es `Rpl_semi_sync_master_no_tx`: cuenta las
transacciones que se confirmaron sin la protección que se creía tener.

### La garantía se apaga sola, y en silencio

Al agotarse `rpl_semi_sync_master_timeout` el primario **cae a asíncrono y sigue**. Es
lo correcto —una réplica muerta no puede bloquear los logins— pero significa que la
garantía de no perder escrituras recientes se apaga sola sin avisar en ningún sitio
visible. Vigilar `Rpl_semi_sync_master_no_tx` (arriba) es la única forma de enterarse.

### La réplica figura `DOWN` en HAProxy, y es lo que tiene que decir

El agente de sonda de cada nodo responde 200 solo si es primario escribible, 503 en
cualquier otro caso —incluida una consulta que no contesta: MariaDB caída, demasiadas
conexiones—. La réplica sale `DOWN` en el backend `be_mysql` todo el rato y a
propósito: no es una avería, es la sonda diciendo la verdad.

Lo que la sonda **no** cubre: un nodo que responde pero **no puede escribir**. Con el
disco lleno, `SELECT @@read_only + @@innodb_read_only` sigue devolviendo `0` y la sonda
sigue diciendo 200 mientras cada `INSERT` falla. Detectar eso exige una escritura de
prueba periódica, con su propio coste y su propia basura que limpiar, y no está aquí.
La sonda dice «MariaDB responde y este nodo es el escribible», no «este nodo puede
escribir».

### La replicación va cifrada, pero no verifica al servidor

`CHANGE MASTER` se hace con `master_ssl = 1` y `master_ssl_ca` apuntando a la CA
interna, así que el binlog viaja cifrado entre los dos nodos. No se pone
`master_ssl_verify_server_cert`: la réplica se conecta al primario **por IP**
(`ansible_host`), y con eso activo MariaDB compara esa IP contra el *common name* del
certificado, no contra sus SAN —que es donde el rol `internal_ca` sí pone la IP—. El
hueco que queda es un ataque activo en la red entre las dos máquinas (alguien que se
haga pasar por el primario); el pasivo —leer la replicación— está cerrado. Cerrarlo del
todo exige que la réplica marque un *nombre* al conectarse, y eso es un cambio de cómo
se identifica cada nodo, no un parámetro más.

Orchestrator está en el mismo sitio, y no por elección: su conexión a los dos MariaDB
va cifrada —`MySQLTopologyUseMutualTLS` a `true` lo deja explícito en vez de depender
del `MySQLTopologyUseMixedTLS` que la herramienta trae de fábrica sin escribirlo en
ninguna parte— pero **tampoco verifica al servidor**, y aquí ni siquiera es una
decisión: la última versión de orchestrator (2022) carga el fichero de CA en
`ClientCAs`, que es la mitad de *servidor* de la configuración TLS, y deja `RootCAs`
vacío; su cliente de MySQL acaba usando el almacén de certificados del sistema y
rechaza cualquier CA propia con `x509: certificate signed by unknown authority`. Por
eso `MySQLTopologySSLSkipVerify` está en `true` y no en `false`: el `false` anterior
prometía una verificación que nunca ocurrió. Por el mismo camino tampoco presenta
certificado de cliente (con el usuario en `REQUIRE X509` recibe `Access denied`), así
que el rol no le copia cert ni clave: serían ficheros sin uso.

Importa porque la cuenta de orchestrator lleva `SUPER`, `PROCESS` y `READ_ONLY ADMIN`.
Lo que sigue sosteniendo esa credencial es que la red entre las máquinas sea privada,
igual que el bus del clúster de Valkey.

### El primer arranque tarda ~10 s de más

Con `rpl_semi_sync_master_enabled=ON` y sin réplica todavía, el primario espera el ack
de una réplica que aún no existe hasta agotar `rpl_semi_sync_master_timeout`, y deja
esto en el log:

```
Timeout waiting for reply of binlog
```

Esperado, no roto: es el mismo mecanismo del apartado anterior, visto en el arranque.

### El digest de orchestrator, y por qué no hay una etiqueta de versión

`openarkcode/orchestrator` no publica etiquetas de versión en Docker Hub, solo
`latest`. La regla del repositorio de no usar nunca `latest` se cumple fijando la
imagen **por digest** (`orchestrator_image`, en
`ansible/roles/orchestrator/defaults/main.yml`). Para obtener uno nuevo:

```bash
docker manifest inspect openarkcode/orchestrator:latest
```

y leer el digest del manifiesto de la plataforma que toque. Cambiarlo es una decisión
consciente, no una actualización rutinaria: **la última versión de orchestrator es de
2022**, así que un digest nuevo no trae correcciones, trae otra construcción de lo
mismo.

### El rol no clona la réplica si el primario ya tiene datos

Con datos ya escritos en el primario, el rol se niega a enganchar una réplica nueva —
lo dice el `fail_msg` de la propia tarea—: clonar bases de datos en producción desde
un playbook, sin ventana ni comprobación de tamaño, es un mecanismo que no se puede
probar aquí y que borra datos si se equivoca. Los dos caminos, a mano:

```bash
# En el primario
mariadb-dump --defaults-file=... --all-databases --master-data=2 \
    --single-transaction --gtid > volcado.sql
# (o mariabackup, que no bloquea, si la base es grande)

# En la replica: cargar el volcado y fijar gtid_slave_pos con el valor que el
# propio volcado trae en su cabecera. Despues, volver a lanzar el playbook:
# detectara la replicacion existente y no tocara nada.
```

### Lo que el operador tiene que hacer a mano

Cuatro huecos que la fase deja abiertos a propósito: automatizarlos sin una comprobación
que hoy nadie puede hacer con seguridad es peor que dejarlos manuales.

**A. Un primario que reinicia vuelve en solo lectura.** `read_only=ON` en el arranque
es de fichero (`50-dex.cnf`), no un estado que orchestrator recuerde. Tras un
`docker start` o un reinicio del primario, ningún nodo queda escribible: orchestrator
no tiene ninguna política para un master que vuelve en solo lectura —`set-writeable`
es una orden manual de su CLI/API, no algo que corra por su cuenta (comprobado contra
el binario, `orchestrator -c help` y 30 s de sondeo en vivo)—, y el rol tampoco lo
repone, porque no promueve sobre datos existentes, por diseño. Paso del operador, en
el nodo correcto:

```bash
docker exec <contenedor-mariadb> mariadb --defaults-file=/run/secrets/root.cnf \
    -e "SET GLOBAL read_only = OFF"
```

Es el coste que la spec elige a propósito (§4.3): seguridad frente a bifurcación del
historial, antes que disponibilidad automática.

**B. Una segunda promoción dentro de la hora se queda bloqueada.**
`RecoveryPeriodBlockSeconds: 3600` en la configuración de orchestrator impide lanzar
una recuperación nueva sobre el mismo clúster lógico mientras la anterior no se haya
confirmado, aunque el fallo nuevo sea sobre el nodo contrario y legítimo. Medido el
2026-09-06: con la recuperación anterior sin confirmar, el bloqueo duró **2 min 34 s**
hasta un acuse manual; confirmada de antemano, la promoción siguiente tardó los
mismos **~3 s** de siempre. El acuse:

```bash
curl -s "http://<host-orchestrator>:<orchestrator_http_port>/api/ack-all-recoveries?comment=<motivo>"
```

Contra `<host-orchestrator>`, **no** contra `localhost`: `ListenAddress` ata la API a
`ansible_host`, la interfaz privada, y no a todas. Y el aviso que va con eso: **esta API
no lleva autenticación**. Orchestrator la soporta, pero configurarla es otra fase; hasta
entonces, cualquiera que alcance ese puerto puede promover, desenganchar o poner en solo
lectura los dos nodos de datos. El 3000 no puede ser alcanzable fuera de la red privada
—misma condición que el bus del clúster de Valkey—, y `ListenAddress` es lo único que
hoy lo sostiene.

No se baja el valor a ciegas: encadenar promociones sin ninguna ventana de por medio
es el riesgo contrario. Candidato de TODO: acuse automático, o una alarma sobre
`api/audit-recovery` cuando aparezca una recuperación con `Acknowledged: false`.

**C. El primario viejo que vuelve tras un failover queda huérfano.** Sin el
`read_only=OFF` del punto A y sin replicar: orchestrator no lo reengancha, y el rol
tampoco porque no clona sobre datos existentes. Observado cinco veces durante las
pruebas de esta fase. Paso del operador, según haya divergido o no del nuevo
primario:

```bash
# Si NO divergio: reenganchar por GTID al primario nuevo
docker exec <contenedor-viejo> mariadb --defaults-file=/run/secrets/root.cnf \
    -e "SET GLOBAL gtid_slave_pos = '<gtid_binlog_pos del primario nuevo>'"
docker exec -i <contenedor-viejo> mariadb --defaults-file=/run/secrets/root.cnf <<'SQL'
CHANGE MASTER TO
    master_host = '<ip-del-primario-nuevo>',
    master_port = 3306,
    master_user = '<mariadb_repl_user>',
    master_password = '<...>',
    master_ssl = 1,
    master_ssl_ca = '/etc/mysql/tls/ca.crt',
    master_use_gtid = slave_pos;
START REPLICA;
SQL

# Si SI divergio: mariadb-dump o mariabackup, igual que en "El rol no clona
# la replica si el primario ya tiene datos", de arriba.
```

**D. Desplegar un cambio de plantilla REINICIA el primario, y eso puede disparar un
failover.** Cualquier cambio en `50-dex.cnf.j2`, `probe.sh.j2`, `probe.cnf.j2`, el
`docker-compose.yml.j2` del rol o el material TLS dispara el handler «Reiniciar
mariadb», que recrea el contenedor. Durante esa ventana el primario no responde, y si
dura más que la detección de orchestrator (`InstancePollSeconds`, 5 s de fábrica),
orchestrator lo da por muerto y promueve a la réplica: el despliegue se encuentra con
un failover que nadie pidió y con el nodo viejo huérfano (punto C). Antes de un
despliegue **con cambios** en el rol `mariadb`, declarar la ventana:

```bash
# Antes: silenciar la deteccion sobre el primario
curl -s "http://<host-orchestrator>:<orchestrator_http_port>/api/begin-downtime/<host-primario>/<mariadb_port>/<quien>/<motivo>/30m"

# Desplegar

# Despues: devolverlo a vigilancia
curl -s "http://<host-orchestrator>:<orchestrator_http_port>/api/end-downtime/<host-primario>/<mariadb_port>"
```

(o `orchestrator-client -c begin-downtime -i <host>:<puerto> -d 30m`, que llama a esa
misma API). Un despliegue **sin cambios** no reinicia nada y no necesita la ventana: el
rol es idempotente y el handler no salta.

Con la ventana declarada, el rol repone el `read_only=OFF` del primario él solo al
terminar —es la rama 2 de la elección del primario en `replicacion.yml`: si este nodo
era escribible al empezar la ejecución y al terminar no lo es nadie, lo que hay que
reponer es lo que el propio rol tiró—. Sin la ventana, si orchestrator llegó a promover
a la réplica durante el reinicio, esa réplica sale escribible, gana ella, y el nodo
viejo queda huérfano: punto C.

Y un tercer caso, además del failover de orchestrator: si la réplica está caída o muda
en ese mismo instante, la rama 2 tampoco repone —exige oír a los dos nodos antes de
devolver el `read_only=OFF`, precisamente para no arriesgarse a un segundo primario— y
el rol deja el primario en solo lectura sin que nadie lo note hasta la siguiente
petición de escritura; una segunda ejecución del playbook no lo arregla sola, porque ya
no «era escribible» al empezar esa segunda pasada. La regla: no desplegar cambios de
plantilla en el rol `mariadb` con un nodo del par caído; si no hay más remedio, reponer
con el paso A (`SET GLOBAL read_only = OFF` en el nodo correcto) y volver a desplegar.

## Qué NO cubre este despliegue

- **Que el panel reintente cuando Valkey no está al arrancar.** HAProxy sondea el
  `/readyz` del panel, que da 503 si no llega a dex o a Valkey, así que no le manda
  tráfico a un panel ciego. Pero si Valkey no responde al **arrancar**, el panel sale
  con error en vez de esperar; lo levanta el `restart` del contenedor, no él mismo.
- **`ent` con MariaDB no está soportado.** El feature flag `DEX_ENT_ENABLED` (apagado
  por defecto) usa `atlas`, que hace sondeo de versión del servidor; MariaDB devuelve
  una cadena compuesta del tipo `5.5.5-10.11.2-MariaDB` que atlas no interpreta como
  MySQL. El camino soportado con MariaDB es `storage/sql` (el de por defecto), verificado
  contra 10.11 y 11.4.

## Verificación manual entre máquinas

Cluster y sentinel se anuncian por IP real (gossip), así que probarlos de verdad exige
máquinas de verdad: en un solo host, Docker Desktop en macOS no da red de host y
`community.docker.docker_container_exec` se salta bajo `--check`, lo que deja sin probar
justo la lógica que importa —que el rol no deshaga un failover—. Lo siguiente se hizo a
propósito como verificación manual, no automatizada; hace falta tres máquinas de usar y
tirar (o tres VMs), Docker en las tres, y un inventario apuntando a sus IPs reales.

Sustituye `<inv>` por la ruta al inventario real y `<host-N>` por el nombre de cada
máquina tal como aparece en él.

### 0. Consultar Valkey a mano sin dejar la contraseña por ahí

Todos los `valkey-cli` de esta lista necesitan la contraseña, porque los nodos y el
sentinel llevan `requirepass`. **No la escribas en la orden**, ni con `-a`, ni con un
`-e REDISCLI_AUTH=…` con valor: `-a` la deja en el argv del propio `valkey-cli` (legible
con `docker top` mientras dura), y un `-e VAR=valor` la deja en el argv del cliente
`docker` del host —cualquier usuario local la ve con `ps`— y además en el historial del
intérprete de órdenes. Es la misma puerta que se cerró sacando los secretos del compose
y poniendo `no_log` en el rol; escribirla aquí volvía a abrirla.

La forma correcta, una vez por sesión y **en la máquina donde corre Docker** (`ssh` no
lleva la variable consigo):

```bash
ssh <host-N>
read -rs REDISCLI_AUTH   # se teclea, no se hace eco, y no queda en el historial
export REDISCLI_AUTH

# A partir de aquí, en esa misma sesión: -e REDISCLI_AUTH SIN valor, que es
# como se le dice a docker que la tome del entorno del cliente
docker exec -e REDISCLI_AUTH valkey valkey-cli --tls \
    --cacert /etc/valkey/tls/ca.crt -p 6379 ping

unset REDISCLI_AUTH      # al terminar
```

Los bloques siguientes dan por hecho ese `read`/`export` en la máquina donde se lanza
cada orden, y por eso escriben `ssh <host-N>` en su propia línea en vez de delante del
`docker exec`.

### 1. Sentinel: failover, y que el rol no lo deshaga

```bash
# Desplegar sentinel (grupos valkey y valkey_sentinel del inventario)
.venv/bin/ansible-playbook rasty94.dex.site -i <inv>/hosts.yml

# Quién es el master ahora mismo, preguntando a cualquier sentinel. El puerto
# de control es TLS-only y lleva requirepass, así que hace falta --tls y la
# contraseña, que va por entorno (`-e REDISCLI_AUTH` sin valor: docker la toma
# del entorno del cliente) y nunca escrita en la orden.
ssh <host-sentinel-1>
docker exec -e REDISCLI_AUTH valkey-sentinel \
    valkey-cli --tls --cacert /etc/valkey/tls/ca.crt \
    -p 26379 sentinel get-master-addr-by-name dex

# Pararlo
ssh <host-del-master> docker stop valkey

# Esperar mas que down-after-milliseconds + un margen (valores de fabrica:
# 5000 ms de deteccion, hasta 60000 ms de plazo de failover) y volver a
# preguntar: tiene que devolver una IP distinta a la de antes
ssh <host-sentinel-1>
docker exec -e REDISCLI_AUTH valkey-sentinel \
    valkey-cli --tls --cacert /etc/valkey/tls/ca.crt \
    -p 26379 sentinel get-master-addr-by-name dex

# El punto que importa: repetir el playbook y confirmar que el master sigue
# siendo el promocionado, no el primer host del grupo valkey del inventario
.venv/bin/ansible-playbook rasty94.dex.site -i <inv>/hosts.yml
ssh <host-sentinel-1>
docker exec -e REDISCLI_AUTH valkey-sentinel \
    valkey-cli --tls --cacert /etc/valkey/tls/ca.crt \
    -p 26379 sentinel get-master-addr-by-name dex
# -> tiene que coincidir con la IP promocionada, no con la original
```

Si el rol reasignara el master en cada pasada (en vez de solo en el arranque inicial,
como dice `ansible/roles/valkey/tasks/sentinel.yml`), esta última comprobación es la
que lo delata: el failover se deshace en el siguiente despliegue.

### 2. Cluster: formación y supervivencia

```bash
# Con valkey_topology: cluster en el grupo valkey del inventario
.venv/bin/ansible-playbook rasty94.dex.site -i <inv>/hosts.yml

# Estado del cluster desde cualquier nodo (TLS + contraseña por REDISCLI_AUTH,
# con el read/export del paso 0)
ssh <host-1>
docker exec -e REDISCLI_AUTH valkey \
    valkey-cli --tls --cacert /etc/valkey/tls/ca.crt \
    --cert /etc/valkey/tls/node.crt --key /etc/valkey/tls/node.key \
    -p 6379 cluster info
# -> cluster_state:ok

# Parar una maquina entera (sus dos procesos: master y replica)
ssh <host-1> docker stop valkey valkey-replica

# El cluster tiene que seguir sirviendo desde otro nodo
ssh <host-2>
docker exec -e REDISCLI_AUTH valkey \
    valkey-cli --tls --cacert /etc/valkey/tls/ca.crt \
    --cert /etc/valkey/tls/node.crt --key /etc/valkey/tls/node.key \
    -p 6379 cluster info
# -> cluster_state:ok, con el slot que llevaba el master caido promovido en
#    su replica
```

### 3. Colocación de réplicas: ninguna en la máquina de su master

Dejado deliberadamente manual en vez de con un analizador de topología propio: no hay
forma de probar ese analizador contra un cluster real en una sola máquina, y un
analizador sin probar guardando una propiedad de seguridad es peor que revisarlo a
mano.

```bash
ssh <host-1>
docker exec -e REDISCLI_AUTH valkey \
    valkey-cli --tls --cacert /etc/valkey/tls/ca.crt \
    --cert /etc/valkey/tls/node.crt --key /etc/valkey/tls/node.key \
    --cluster check 127.0.0.1:6379
```

La salida lista cada master con sus réplicas y la IP de cada una: confirmar a mano que
ninguna IP de réplica coincide con la IP de la máquina de su propio master.

### 4. El sentinel también va en TLS, y esto es lo que lo demuestra

**Respondida la pregunta que este documento dejaba abierta: sí, el sentinel necesita
su propio `tls-port`, y además `tls-replication yes`.** Darle solo su certificado no
basta.

`ansible/roles/valkey/templates/sentinel.conf.j2` tiene hoy la misma forma que los
nodos de datos —`port 0` más `tls-port`, `tls-auth-clients no` y `requirepass`— y eso
no es simetría por gusto: sin ello el sentinel no tenía configuración válida en
**ninguna** dirección. `pkg/valkey` copia la configuración TLS del cliente al
`SentinelOption` en cuanto hay TLS (un solo mando, a propósito), así que un cliente sin
`tls:` no alcanzaba al master TLS-only y uno con `tls:` hablaba TLS contra un 26379 en
texto plano. Las dos fallaban.

Comprobado contra contenedores de Valkey 8 de verdad, no razonado:

| Configuración del sentinel | Lo que ve `sentinel master dex` |
| --- | --- |
| Solo los `tls-*-file`, sin `tls-replication` | `flags: s_down,o_down,master,disconnected`, `runid` vacío |
| Con `tls-replication yes` | `flags: master`, `runid` aprendido, enlace correcto |

Es decir: lo que cifra la conexión **saliente** del sentinel hacia el master es
`tls-replication yes`, no el `tls-port`. El `tls-port` es para el otro lado, el de quien
le pregunta —dex, el panel y el operador—, y es lo que permite que el puerto de control
deje de estar en claro.

Otras dos cosas que la misma prueba dejó claras:

- **`tls-auth-clients` viene en `yes` de fábrica**, o sea que el servidor exige
  certificado de cliente. Durante un tiempo las dos plantillas lo pusieron en `no`,
  porque `pkg/valkey` solo sabía presentar la CA y un cliente que solo la traía recibía
  `Server closed the connection`. **Ya no**: `pkg/valkey` acepta `tls.cert` y `tls.key`,
  el rol copia el par a los nodos de dex y del panel, y las dos plantillas han vuelto al
  `yes` de fábrica. Importa más de lo que parecía: la directiva es global, no solo para
  los clientes, así que mientras estuvo en `no` el bus del cluster iba cifrado y **sin
  autenticar** —el bus no tiene `requirepass`—, y bastaba tener la CA para unirse.
  Comprobado con un nodo de usar y tirar: con el par, `PONG`; sin él,
  `remote error: tls: certificate required`.
- **Con `requirepass`, un cliente sin credenciales recibe `NOAUTH Authentication
  required`** en el puerto de control. Esto importa porque el sentinel va en red de
  host: sin `requirepass`, cualquiera que alcance el 26379 podía repuntar el master set
  a una dirección suya, y entonces dex le pregunta al sentinel quién es el master y le
  manda `AUTH <contraseña>` a la máquina del atacante.

Lo que sigue sin probarse contra máquinas de verdad —y por eso queda en esta lista— es
el failover completo del paso 1 con esta configuración: aquí solo se ha verificado el
enlace sentinel → master y el acceso autenticado al puerto de control.

```bash
# Tras desplegar sentinel de verdad (paso 1), en cualquier host de sentinel:
ssh <host-sentinel-1> docker logs valkey-sentinel | grep -i tls

# Y el estado que ve sentinel del master, que dice si la conexión funciona
ssh <host-sentinel-1>
docker exec -e REDISCLI_AUTH valkey-sentinel \
    valkey-cli --tls --cacert /etc/valkey/tls/ca.crt \
    -p 26379 sentinel master dex
# -> flags: master, sin s_down ni o_down, y num-slaves con las réplicas reales
```

### 5. VRRP: la IP flotante se mueve, y una sesión abierta sobrevive

Lo único de la fase 3 que no se puede probar en una sola máquina. Dos contenedores con
`NET_ADMIN` en el mismo host comparten pila de red y el resultado no dice nada del caso
real, así que aquí no hay un test verde que no prueba nada: hay estos cuatro pasos.

Todo lo demás del balanceador **sí** está probado contra contenedores de verdad: el
reparto, las sondas, el `X-Forwarded-For`, el passthrough del gRPC con sus SAN, y una
sesión de administrador atendida alternativamente por los dos paneles.

```bash
# 1. La VIP está en el MASTER y responde.
ssh <lb-1>
ip addr show <interfaz> | grep <vip>        # -> la VIP aparece aquí y no en lb-2
curl -sk https://<vip>/dex/healthz          # -> 200

# 2. Parar HAProxy en el MASTER mueve la VIP.
docker stop haproxy
# esperar mas de (keepalived_health_interval x keepalived_health_fall) segundos
ssh <lb-2> "ip addr show <interfaz> | grep <vip>"   # -> ahora esta aqui
curl -sk https://<vip>/dex/healthz                  # -> sigue 200
```

3. **Una sesión abierta en el panel sobrevive al salto.** Con sesión iniciada en el
   panel *antes* del paso 2, recargar cualquier página después: tiene que seguir dentro,
   sin volver a pedir credenciales. Es lo que demuestra que el balanceador y el estado
   compartido se entienden; si pide login otra vez, el problema está en Valkey, no en
   keepalived.

4. **Levantar el primero.** `docker start haproxy` en `lb-1`. Con la configuración de
   este rol la VIP vuelve, porque la prioridad se recupera al recuperarse la sonda.
   Anotar lo que salga tal cual: si se ha añadido `nopreempt`, no vuelve, y eso también
   es correcto.

Y una comprobación que **no** es de VRRP pero pertenece a esta lista, porque tampoco se
puede hacer en una máquina: que el certificado del sitio cubra **los dos nombres**, el
del issuer y el del panel.

```bash
openssl s_client -connect <vip>:443 -servername <nombre-del-panel> </dev/null 2>/dev/null \
    | openssl x509 -noout -ext subjectAltName
# -> tienen que aparecer los dos nombres, o un comodin que los cubra
```

### 6. MariaDB: promoción automática entre máquinas

Tres instancias de raft en un portátil comparten demasiado —misma red, mismo reloj,
mismo disco— para que el resultado del quorum signifique algo, igual que con el
failover de sentinel y los pasos de VRRP: hace falta máquinas de verdad.

```bash
# Quien es el primario ahora mismo, por la sonda de cada nodo
ssh <host-db-1>
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:<mariadb_probe_port>/

# Matar el primario de verdad
ssh <host-primario> docker stop mariadb

# Esperar al failover y comprobar que el otro nodo quedo escribible:
# ApplyMySQLPromotionAfterMasterFailover tiene que haber quitado el solo lectura
ssh <host-replica>
docker exec mariadb mariadb --defaults-file=/run/secrets/root.cnf -N -B \
    -e "SELECT @@read_only"
# -> 0

# Que HAProxy cambio de backend por la sonda, no porque orchestrator se lo dijera
ssh <host-balanceador> docker logs haproxy | tail -20
# -> el backend viejo a DOWN y el nuevo a UP, ambos por el check de la sonda

# Una escritura por el balanceador tiene que llegar al nodo nuevo
mariadb --host=<vip-o-nombre-balanceador> --port=<haproxy_mysql_port> \
    --ssl-ca=<ca.crt> -u<usuario> -p -e "SELECT @@hostname"
```

Medido en un portátil con los tres orchestrator en la misma máquina —**no un
despliegue real, cifras de referencia únicamente**—, el 2026-09-06: failover
automático (`docker stop` del primario → sonda 200 del nodo promovido) **4,64 s**;
`docker stop` → primera petición de dex con éxito (`/dex/auth`, que escribe en
storage) **6,87 s**; peticiones de dex fallidas en medio, **3** (`healthz` no falló
ninguna, porque no toca storage).

### 7. La pérdida de escrituras con `AFTER_COMMIT` frente a `AFTER_SYNC`

Demostrarla de verdad exige matar el primario en una ventana de milisegundos entre
máquinas distintas —justo el hueco en el que `AFTER_COMMIT` confirma localmente antes
de esperar el ack de la réplica—, y eso no se puede montar como prueba de laboratorio
sin que el resultado sea casualidad. Lo que se documenta es el razonamiento y el valor
fijado (§3.2 de la [spec de la fase 4](specs/2026-09-06-mariadb-alta-disponibilidad.md)):
`AFTER_SYNC`, que espera el ack **antes** de confirmar, es el que evita perder un
refresh token recién emitido si el primario muere justo después del commit. Lo único
verificable en una sola máquina:

```sql
SELECT @@rpl_semi_sync_master_wait_point;
-- -> AFTER_SYNC, no el AFTER_COMMIT de fabrica de MariaDB
```

### 8. Instalar la colección por `git+https://` público, de verdad

Pendiente hasta que se empuje la primera etiqueta `fork-vX.Y.Z` con la colección dentro
—`release.yaml` la construye y la comprueba en CI contra un clon local en cada
etiqueta, pero nadie ha instalado todavía desde el repositorio público de verdad—. La
primera vez que eso pase:

```bash
ansible-galaxy collection install \
    'git+https://github.com/rasty94/dex.git#ansible/,fork-vX.Y.Z'
ansible-galaxy collection list rasty94.dex
# -> rasty94.dex   X.Y.Z, instalada desde el subdirectorio ansible/ del repositorio público
```

Si falla, lo primero que hay que mirar es la etiqueta: `ansible-galaxy` necesita que
`fork-vX.Y.Z` exista de verdad en el remoto, y que `ansible/galaxy.yml` en ese commit
lleve la misma cifra de versión —lo que comprueba `release.yaml` antes de construir
nada—.
