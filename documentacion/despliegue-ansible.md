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

- **Docker**, con el plugin `compose`. El rol usa
  `community.docker.docker_compose_v2` y `community.docker.docker_container_exec`; no
  instala Docker por sí mismo.
- **Una base MariaDB o MySQL que ya exista**, alcanzable desde los nodos del grupo
  `dex`. El rol la consume y comprueba que responde antes de arrancar dex; no la
  instala ni la administra — eso es la fase 4 de la hoja de ruta, todavía sin spec.

### En el nodo de control

- **Ansible**, con las colecciones `community.docker` y `community.crypto` (esta
  última genera la CA interna con `openssl_privatekey`/`openssl_csr`/
  `x509_certificate`).
- **El paquete Python `requests`.** Lo necesita
  `community.docker.docker_container_exec`, que el rol `valkey` usa para preguntar por
  el master de sentinel y para formar y comprobar el cluster. Sin él el módulo falla
  con `Failed to import the required Python library (requests)`, y nada en este
  repositorio lo documentaba hasta que la verificación de la Task 11 lo encontró en
  vivo.

Esta rama **no instala Ansible en la máquina**: vive en un venv dentro del propio
árbol de trabajo (`.venv/`, en `.gitignore`), para no tocar el intérprete de Python del
sistema. Así se montó y así hay que reproducirlo:

```bash
python3 -m venv .venv
.venv/bin/pip install ansible-core==2.21.3 ansible-lint==26.8.0 requests
.venv/bin/ansible-galaxy collection install \
    community.crypto:3.3.0 -p .venv/ansible_collections
# community.docker puede ya estar en ~/.ansible/collections; si no:
.venv/bin/ansible-galaxy collection install community.docker:5.2.1
```

Todos los comandos de este documento asumen `.venv/bin/` por delante y esta variable
de entorno, que junta las colecciones del venv con las que ya hubiera en el perfil del
usuario:

```bash
export ANSIBLE_COLLECTIONS_PATH=.venv/ansible_collections:~/.ansible/collections
```

## El inventario y sus cuatro grupos

Ejemplo completo en `ansible/inventories/ejemplo/hosts.yml` (con direcciones del rango
de documentación RFC 5737, para que nadie las confunda con una máquina real) y sus
secretos en `ansible/inventories/ejemplo/group_vars/all.yml`.

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

## Actualizar

El playbook (`ansible/playbooks/dex.yml`) ordena las tandas por dependencia — `CA
interna` → `Valkey` → `Dex` → `Panel` — y cada una de las tres últimas lleva
`serial: 1`: los hosts de cada grupo se actualizan de uno en uno, para que una
actualización no deje el servicio entero abajo a la vez.

La imagen se fija por variable (`dex_image`) a una etiqueta `fork-vX.Y.Z`, **nunca
`latest`**: con `serial: 1` sobre varios hosts, una etiqueta móvil puede cambiar de
versión a mitad de la tanda entre el primer host actualizado y el último.

## Qué tiene que hacer el balanceador

El rol no lo instala ni lo configura — es la fase 3 de la hoja de ruta, con su propia
spec pendiente (HAProxy + keepalived) — pero el despliegue ya cuenta con que exista
desde el primer día: `dex_issuer` y `dex_dashboard_base_url` apuntan a su URL, nunca a
la de un nodo, para que enchufarlo no obligue a reconfigurar nada.

Lo que tiene que hacer:

- **Repartir tráfico** entre los nodos del grupo `dex` y, por separado, entre los del
  grupo `dex_dashboard`.
- **Comprobar `/healthz`** de cada servicio antes de mandarle tráfico (ver la
  advertencia de la siguiente sección: la del panel no dice todo lo que parece decir).
- **No hacer afinidad de sesión.** Es exactamente lo que el estado compartido en Valkey
  existe para hacer innecesario: las sesiones del panel y los contadores del limitador
  de login viven en Valkey, no en el proceso que atendió la petición anterior, así que
  cualquier nodo puede atender cualquier petición. Las dos plantillas —la de `dex` y la
  de `dex_dashboard`— renderizan el mismo bloque `valkey:` a partir de los grupos
  `valkey`/`valkey_sentinel` del inventario, con `keyPrefix` distinto para que no se
  mezclen las claves de los dos procesos.

## Qué NO cubre este despliegue

- **La sonda de disponibilidad del panel sigue sin decir la verdad.** El `/healthz` del
  panel ([cmd/dex-dashboard/main.go:75](../cmd/dex-dashboard/main.go#L75)) devuelve 200
  siempre, sin mirar si su conexión gRPC con dex responde ni si Valkey responde. Sirve
  como sonda de vida — el proceso está arriba — pero no de disponibilidad: un
  balanceador que solo mire esta ruta puede seguir mandando tráfico a un panel cuyas
  páginas van a fallar todas. Sigue siendo una entrada abierta del TODO; este rol no la
  cierra, solo la hereda.
- **`ent` con MariaDB no está soportado.** El feature flag `DEX_ENT_ENABLED` (apagado
  por defecto) usa `atlas`, que hace sondeo de versión del servidor; MariaDB devuelve
  una cadena compuesta del tipo `5.5.5-10.11.2-MariaDB` que atlas no interpreta como
  MySQL. El camino soportado con MariaDB es `storage/sql` (el de por defecto), verificado
  contra 10.11 y 11.4.
- **Las fases 3, 4 y 5** — el balanceador, MariaDB en alta disponibilidad y la colección
  de Ansible publicable — con sus decisiones ya tomadas pero sin spec propia, en la
  [hoja de ruta](specs/2026-09-03-despliegue-hoja-de-ruta.md).

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
.venv/bin/ansible-playbook -i <inv>/hosts.yml ansible/playbooks/dex.yml

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
.venv/bin/ansible-playbook -i <inv>/hosts.yml ansible/playbooks/dex.yml
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
.venv/bin/ansible-playbook -i <inv>/hosts.yml ansible/playbooks/dex.yml

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
  certificado de cliente. `pkg/valkey` solo sabe presentar la CA (su bloque `tls:` tiene
  `caCert` e `insecureSkipVerify` y nada más), así que con el valor de fábrica un
  cliente que solo trae la CA recibe `Server closed the connection`. Con
  `tls-auth-clients no`, `PONG`. Lo mismo vale para los nodos de datos: por eso está en
  `managed.conf.j2`. La autenticación la pone `requirepass`, no el certificado.
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
