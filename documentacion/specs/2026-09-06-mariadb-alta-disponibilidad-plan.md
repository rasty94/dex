# Fase 4 — MariaDB en alta disponibilidad: plan de implementación

> **Para quien lo ejecute:** SUB-SKILL OBLIGATORIA: usar
> superpowers:subagent-driven-development (recomendada) o
> superpowers:executing-plans, tarea a tarea. Los pasos llevan casilla (`- [ ]`).

**Objetivo:** quitar el último punto único de fallo del despliegue — la base de datos
donde viven las claves de firma, los refresh tokens y las sesiones — con un par
primario/réplica en replicación semisíncrona, entrada por HAProxy y promoción
automática por orchestrator.

**Arquitectura:** dos roles nuevos (`mariadb`, `orchestrator`) con la misma forma que los
cinco ya entregados: configuración renderizada en `/etc/<servicio>`, `docker-compose.yml`
al lado, `community.docker.docker_compose_v2` levantando, y un handler con
`recreate: always` porque compose no considera cambio el contenido de un bind mount. El
enrutado no lo decide orchestrator sino un **agente de sonda** en cada nodo de datos, que
responde 200 solo si es primario escribible; HAProxy manda al único backend que pasa la
sonda. Más cambios acotados en `haproxy`, `dex`, el inventario y el playbook.

**Tecnologías:** Ansible, `community.docker`, `community.crypto`, MariaDB 11.4, `socat`,
HAProxy 3.0, orchestrator (openark), Docker Compose.

**Spec:**
[2026-09-06-mariadb-alta-disponibilidad.md](2026-09-06-mariadb-alta-disponibilidad.md)

## Restricciones globales

- **Documentación en español, código y comentarios en inglés.** Los comentarios de las
  plantillas Jinja y de los ficheros de Ansible van en **español y sin tildes**, como los
  de los cinco roles ya entregados; el inglés es para el código Go.
- **Commits en español, sin tildes** (la ñ sí), prefijo convencional, asunto en
  minúsculas y sin punto. **Sin atribución de ninguna herramienta.**
- **Nada de `git add -A`.** `references/` no se versiona y `go build ./...` deja un
  binario en `cmd/dex-dashboard`.
- **`pre-commit run --files <cambiados>`** antes de cada commit. El hook de
  `ansible-lint` funciona: si falla, es un fallo de verdad, no del hook.
- **Imágenes fijadas por etiqueta, nunca `latest`**, y siempre por variable. orchestrator
  es la excepción y se fija **por digest**, porque no publica etiquetas de versión (§3.6
  de la spec).
- **Ningún secreto en el entorno de un contenedor** ni en el `argv` de un proceso:
  `docker inspect` y `docker top` los leen. Fichero en disco modo `0600`, y
  `no_log: true` en la tarea que lo escribe.
- **Los defaults de los roles ya entregados no cambian de comportamiento.** Un inventario
  de la fase 3, sin grupo `mariadb`, tiene que seguir desplegando igual que hoy.
- **Nada fuera del worktree**: ni ficheros, ni contenedores, ni imágenes, ni volúmenes que
  no haya creado esta tarea. La máquina tiene `Ejemplos/dashboard` y `omniroute` corriendo
  (contenedores `dex`, `dex-replica`, `dex-dashboard`, `valkey`, `omniroute`,
  `omniroute-valkey`) y **no se tocan**. Cualquier contenedor propio lleva prefijo `t14-`,
  y se borra al terminar la tarea que lo creó.
- **Cada test nuevo se sabotea una vez**: revertir el arreglo y comprobar que falla por lo
  que importa, no por otra cosa.

---

## Lo que ya se verificó contra la imagen real, y por qué cambia el plan

Esto **no** está en la spec: son cuatro hallazgos de comprobar `mariadb:11.4` en vivo
antes de escribir el plan. Los cuatro cambian código que se va a escribir, así que van
arriba y no enterrados en su tarea.

### 1. Un par recién creado NO puede empezar a replicar desde cero

Es el hallazgo que más caro habría salido. La imagen de MariaDB, en su primer arranque,
ejecuta su propio bootstrap: crea la base `dex` y el usuario `dex`, y esas sentencias
**entran en el binlog del primario**. La réplica hace su propio bootstrap idéntico por su
cuenta.

Si se arranca la replicación con `gtid_slave_pos` vacío, la réplica se pone a reproducir
el binlog del primario **desde el principio**, incluido ese bootstrap que ella ya hizo. Se
para en seco:

```
Slave_IO_Running: Yes
Slave_SQL_Running: No
Last_SQL_Error: Error 'Operation CREATE USER failed for 'dex'@'%'' on query.
                Default database: 'mysql'. Query: 'CREATE USER 'dex'@'%' IDENTIFIED BY 'y''
```

Reproducido en vivo con dos contenedores. **El arreglo es fijar el punto de partida antes
de enganchar**, saltándose la historia que la réplica ya tiene:

```sql
-- en el primario
SELECT @@gtid_binlog_pos;            -- p.ej. "0-1-5"
-- en la replica
SET GLOBAL gtid_slave_pos = '0-1-5';
CHANGE MASTER TO master_host='...', master_user='...', master_password='...',
                 master_use_gtid = slave_pos;
START REPLICA;
```

Con eso: `Slave_IO_Running: Yes`, `Slave_SQL_Running: Yes`, `Seconds_Behind_Master: 0`,
`Using_Gtid: Slave_Pos`, y una escritura en el primario aparece en la réplica. Comprobado.

**Y de aquí sale la única razón de que el rol se niegue a clonar** (§4.4 de la spec): este
atajo solo es correcto cuando la réplica no tiene nada que perder. Con datos ya en el
primario, saltarse la historia deja una réplica silenciosamente incompleta — mucho peor
que una que se para con un error.

### 2. `Rpl_semi_sync_master_status` por sí solo miente

La spec dice que la documentación explique mirar `Rpl_semi_sync_master_status` para saber
si la semisíncrona está activa de verdad. **No basta**, y esto se midió:

| Momento | `..._status` | `..._clients` | Qué pasa de verdad |
| --- | --- | --- | --- |
| Recién arrancado, sin réplica | `ON` | `0` | La siguiente escritura bloquea 10 s y cae a asíncrono |
| Tras una escritura sin réplica | `OFF` | `0` | Ya cayó: `Rpl_semi_sync_master_no_tx` = 1 |
| Con la réplica enganchada | `ON` | `1` | Activa: `..._yes_tx` sube, `..._no_tx` se queda en 0 |

`status: ON` con `clients: 0` es una falsa tranquilidad: dice «habilitada», no
«funcionando». La comprobación correcta es **la pareja**, y el contador que vale para una
alarma es `Rpl_semi_sync_master_no_tx`: cuenta las transacciones que se confirmaron sin la
protección que se creía tener. La tarea 8 lo escribe así.

### 3. El primer arranque tarda ~10 s de más, y no es un fallo

Con `rpl_semi_sync_master_enabled = ON` y sin réplica todavía, el bootstrap de la propia
imagen bloquea el tiempo entero de `rpl_semi_sync_master_timeout` (10 s de fábrica) y deja
esto en el log antes de seguir:

```
[Warning] Timeout waiting for reply of binlog (file: binlog.000001, pos: 468),
          semi-sync up to file , position 0.
```

Es esperado y es correcto (§3.2: caer a asíncrono es lo que evita que una réplica muerta
bloquee los logins), pero un `wait_for` con timeout corto lo leería como un despliegue
roto. Las esperas de este plan dan 90 s.

### 4. Lo que ya se puede dar por bueno

- **`read_only = ON` en el fichero de configuración no rompe el bootstrap de la imagen**:
  la base `dex` y el usuario `dex` se crean igual, porque `root` tiene `SUPER`. Comprobado.
- **Nombres exactos de las variables en 11.4.13**: `rpl_semi_sync_master_enabled`,
  `rpl_semi_sync_master_wait_point` (de fábrica `AFTER_COMMIT`, confirmado),
  `rpl_semi_sync_master_timeout` (10000), `rpl_semi_sync_master_wait_no_slave` (`ON`),
  `rpl_semi_sync_slave_enabled`. La sección del fichero es `[mariadbd]`.
- **`SHOW REPLICA STATUS` existe** y devuelve vacío sin error en un nodo sin replicación:
  sirve como primitiva de detección.
- **`@@transaction_isolation` de fábrica es `REPEATABLE-READ`**, que es exactamente la
  pérdida silenciosa de corrección de §3.1.
- **La imagen trae todo lo que hace falta**: `/usr/bin/socat`, `/usr/bin/mariadb`,
  `/usr/bin/mariadb-dump`, `/usr/bin/mariabackup`, `/usr/bin/mariadb-admin`. Sin imagen
  propia.
- **El usuario `mysql` de la imagen es `999:999`** (`docker run --rm --entrypoint sh
  mariadb:11.4 -c 'id mysql'`).
- **Los sufijos `_FILE` funcionan**: `MARIADB_ROOT_PASSWORD_FILE` y
  `MARIADB_PASSWORD_FILE` leen la contraseña de un fichero y `docker inspect` solo enseña
  la ruta. Es lo que hace posible cumplir la restricción de secretos sin inventar nada.
- **El agente de sonda de §5 funciona tal cual está escrito en la spec**: 200 con
  `read_only=OFF`, 503 con `read_only=ON`, 503 con las credenciales rotas (el caso «la
  base no responde»). Y el usuario de la sonda con **solo `USAGE`** puede ejecutar
  `SELECT @@read_only + @@innodb_read_only`.

---

## Estructura de ficheros

**Se crean:**

| Fichero | Responsabilidad |
| --- | --- |
| `ansible/roles/mariadb/defaults/main.yml` | Variables, imagen, puertos, usuarios |
| `ansible/roles/mariadb/tasks/main.yml` | Asserts, TLS, configuración, secretos, levantar |
| `ansible/roles/mariadb/tasks/tls.yml` | Material TLS del nodo desde la CA interna |
| `ansible/roles/mariadb/tasks/replicacion.yml` | Detección, bootstrap y negativa a clonar |
| `ansible/roles/mariadb/handlers/main.yml` | `Reiniciar mariadb` |
| `ansible/roles/mariadb/templates/50-dex.cnf.j2` | Lo único que Ansible posee de la config |
| `ansible/roles/mariadb/templates/probe.sh.j2` | El agente de sonda |
| `ansible/roles/mariadb/templates/probe.cnf.j2` | Credenciales de la sonda, fuera del `argv` |
| `ansible/roles/mariadb/templates/docker-compose.yml.j2` | Servidor y sonda |
| `ansible/roles/orchestrator/defaults/main.yml` | Digest, pares del raft, usuario |
| `ansible/roles/orchestrator/tasks/main.yml` | Asserts, configuración, levantar |
| `ansible/roles/orchestrator/handlers/main.yml` | `Reiniciar orchestrator` |
| `ansible/roles/orchestrator/templates/orchestrator.conf.json.j2` | Raft y política de failover |
| `ansible/roles/orchestrator/templates/docker-compose.yml.j2` | El servicio |

**Se modifican:**

| Fichero | Cambio |
| --- | --- |
| `ansible/roles/haproxy/defaults/main.yml` | `haproxy_mysql_port` |
| `ansible/roles/haproxy/templates/haproxy.cfg.j2` | `fe_mysql` y `be_mysql`, condicionados al grupo |
| `ansible/roles/dex/defaults/main.yml` | Nada de comportamiento: solo el comentario de `dex_storage` |
| `ansible/inventories/ejemplo/hosts.yml` | Grupos `mariadb` y `orchestrator`; `dex_storage.host` al balanceador |
| `ansible/inventories/ejemplo/group_vars/all.yml` | Cuatro contraseñas nuevas |
| `ansible/playbooks/dex.yml` | Tandas de MariaDB y orchestrator, en su sitio |
| `documentacion/despliegue-ansible.md` | Sección de MariaDB, comprobaciones y lista manual |
| `documentacion/specs/2026-09-03-despliegue-hoja-de-ruta.md` | Fase 4 a «Entregada» |
| `CHANGELOG.md`, `TODO.md`, `DONE.md` | Al cerrar |

**Orden de las tandas al terminar** (§8 de la spec):

```
CA interna → MariaDB → orchestrator → Valkey → dex → panel → balanceador
```

---

## Tarea 1: El rol `mariadb` — un nodo con su TLS y su configuración

Deja **un** nodo de MariaDB corriendo, con TLS de la CA interna y la configuración de
§4.3 de la spec. Sin sonda (tarea 2) y sin replicación (tarea 3): el corte está ahí a
propósito, porque un nodo que arranca con `AFTER_SYNC` puesto ya es revisable por sí solo
y es la mitad del valor de la fase.

**Ficheros:**

- Crear: `ansible/roles/mariadb/defaults/main.yml`
- Crear: `ansible/roles/mariadb/tasks/main.yml`
- Crear: `ansible/roles/mariadb/tasks/tls.yml`
- Crear: `ansible/roles/mariadb/handlers/main.yml`
- Crear: `ansible/roles/mariadb/templates/50-dex.cnf.j2`
- Crear: `ansible/roles/mariadb/templates/docker-compose.yml.j2`

**Interfaces:**

- Consume: `internal_ca_cert`, `internal_ca_host_cert`, `internal_ca_host_key` — hechos
  que publica el rol `internal_ca` para el mismo host (ver
  `ansible/roles/internal_ca/tasks/main.yml`). El playbook tiene que meter al grupo
  `mariadb` en su tanda de CA interna, y eso lo hace la tarea 6.
- Produce: `mariadb_config_dir`, `mariadb_container_name`, `mariadb_port`,
  `mariadb_probe_port`, `mariadb_database`, `mariadb_user` — los lee el rol `haproxy`
  (tarea 4) y el rol `orchestrator` (tarea 5).

- [ ] **Paso 1: Los defaults**

Crear `ansible/roles/mariadb/defaults/main.yml`:

```yaml
# La regla que hace idempotente este rol es la misma que la del rol valkey:
# 50-dex.cnf es de Ansible y se reescribe en cada pasada; lo que MariaDB
# escriba por su cuenta --auto.cnf, los ficheros de estado de la replicacion--
# no se toca nunca.
mariadb_config_dir: /etc/mariadb

# Nombre de los contenedores y de la imagen. Parametrizados por el mismo motivo
# que valkey_container_name: poder correr un converge de prueba en una maquina
# que ya tenga otro "mariadb" suelto sin chocar con el.
mariadb_container_name: mariadb
mariadb_probe_container_name: mariadb-probe
mariadb_image: mariadb:11.4

mariadb_port: 3306

# El puerto del agente de sonda. Es de ESTE rol y el rol haproxy lo lee, igual
# que keepalived lee haproxy_https_port: los dos corren en la misma ejecucion.
# Deliberadamente sin default en el rol que no es su dueno, para que correr
# haproxy suelto falle con "undefined variable" en vez de sondear un puerto
# inventado, que dejaria todos los backends DOWN sin decir por que.
mariadb_probe_port: 3307

mariadb_database: dex
mariadb_user: dex

# UID/GID del usuario "mysql" dentro de la imagen oficial. No son
# parametrizables desde fuera: vienen en su /etc/passwd. Comprobado con
#   docker run --rm --entrypoint sh mariadb:11.4 -c 'id mysql'
#   -> uid=999(mysql) gid=999(mysql)
# El directorio de configuracion y el material TLS tienen que ser legibles por
# el, porque el entrypoint hace setpriv a ese usuario.
mariadb_config_owner: "999"
mariadb_config_group: "999"

# La interfaz por la que escucha. El contenedor va en red de host, asi que
# quien limita la exposicion es esta directiva y no un mapeo de puertos.
# Mismo criterio que dex_http_bind: con el balanceador delante, atarlo a la
# interfaz privada deja de ser un ajuste. El valor de fabrica no cambia nada de
# lo de hoy.
mariadb_bind_address: "0.0.0.0"

# Usuarios de servicio. Ninguno de los tres es el usuario de dex: cada uno tiene
# los privilegios justos de su oficio, y el de la sonda no tiene ninguno.
mariadb_repl_user: repl
mariadb_probe_user: probe
mariadb_orchestrator_user: orchestrator

# Este nodo es nuestro (lo instala y lo arranca este rol), asi que una
# contrasena vacia hace fallar el despliegue en vez de avisar: mismo criterio
# que valkey_allow_no_password. Solo un converge de prueba desechable tiene
# motivo para ponerlo a true.
mariadb_allow_no_password: false

# server_id NO tiene default a proposito. Es la identidad de replicacion del
# nodo y va explicita en el inventario: derivarla del indice del grupo hace que
# reordenar el inventario reasigne identidades, y dos nodos con el mismo
# server_id rompen la replicacion de una forma que no se ve hasta que falla.
```

- [ ] **Paso 2: El material TLS, copiado de la CA interna**

Crear `ansible/roles/mariadb/tasks/tls.yml`. Es el mismo patrón que
`ansible/roles/valkey/tasks/tls.yml`, con las rutas y el propietario de este rol:

```yaml
# El material TLS no lo genera este rol: lo hace internal_ca y lo publica como
# hechos para el mismo host, siempre que el playbook incluya a este grupo en su
# tanda "CA interna" (ver ansible/playbooks/dex.yml). Aqui solo se copia.
#
# El certificado del nodo necesita el nombre del balanceador entre sus SAN,
# porque dex se conecta marcando ese nombre y la sesion TLS se negocia contra el
# nodo que toque: es internal_ca_extra_sans, la misma variable que la fase 3
# anadio para el passthrough del gRPC.
- name: Directorio de TLS
  ansible.builtin.file:
    path: "{{ mariadb_config_dir }}/tls"
    state: directory
    owner: "{{ mariadb_config_owner }}"
    group: "{{ mariadb_config_group }}"
    mode: "0750"

- name: Certificado de la CA interna
  ansible.builtin.copy:
    content: "{{ internal_ca_cert }}"
    dest: "{{ mariadb_config_dir }}/tls/ca.crt"
    owner: "{{ mariadb_config_owner }}"
    group: "{{ mariadb_config_group }}"
    mode: "0644"
  notify: Reiniciar mariadb

- name: Certificado propio del nodo
  ansible.builtin.copy:
    content: "{{ internal_ca_host_cert }}"
    dest: "{{ mariadb_config_dir }}/tls/node.crt"
    owner: "{{ mariadb_config_owner }}"
    group: "{{ mariadb_config_group }}"
    mode: "0644"
  notify: Reiniciar mariadb

- name: Clave propia del nodo
  # no_log: es la clave privada del nodo; --diff la imprimiria entera.
  ansible.builtin.copy:
    content: "{{ internal_ca_host_key }}"
    dest: "{{ mariadb_config_dir }}/tls/node.key"
    owner: "{{ mariadb_config_owner }}"
    group: "{{ mariadb_config_group }}"
    mode: "0600"
  notify: Reiniciar mariadb
  no_log: true
```

- [ ] **Paso 3: La plantilla de configuración**

Crear `ansible/roles/mariadb/templates/50-dex.cnf.j2`:

```jinja
# Renderizado por Ansible. No editar aqui: los cambios se pierden en el
# proximo despliegue. La plantilla esta en ansible/roles/mariadb/templates/.
#
# Este fichero es lo UNICO que Ansible posee. Lo que MariaDB escriba por su
# cuenta --auto.cnf con el UUID del servidor, los ficheros de estado de la
# replicacion-- vive en el volumen de datos y no se toca nunca: sobrescribirlo
# le devolveria al nodo una topologia caducada y desharia la promocion que
# acabara de pasar. Es la misma regla que managed.conf en el rol valkey.
[mariadbd]
# La seccion se llama [mariadbd], no [mysqld]: comprobado contra 11.4.
bind_address = {{ mariadb_bind_address }}
port = {{ mariadb_port }}

# Identidad de replicacion. Explicita en el inventario, nunca derivada del
# indice del grupo: ver el comentario de defaults/main.yml.
server_id = {{ mariadb_server_id }}

# GTID y binlog por filas. GTID es lo que permite a orchestrator repuntar una
# replica sin coordenadas de fichero y posicion.
log_bin = /var/lib/mysql/binlog
binlog_format = ROW
gtid_strict_mode = ON

# Semisincrona, los DOS papeles en los DOS nodos: cualquiera puede acabar
# siendo cualquier cosa despues de un failover, y un nodo promovido sin
# rpl_semi_sync_master_enabled serviria escrituras sin la garantia.
rpl_semi_sync_master_enabled = ON
rpl_semi_sync_slave_enabled = ON

# AFTER_SYNC, y NO el valor de fabrica. MariaDB trae AFTER_COMMIT (comprobado
# contra 11.4.13; MySQL trae AFTER_SYNC): con el, el primario confirma y
# DESPUES espera el ack de la replica, asi que si muere en ese hueco la
# transaccion esta confirmada localmente y en ninguna replica. Un cliente pudo
# ver el commit y la promocion se lo lleva por delante -- que es justo el
# motivo por el que se eligio semisincrona sobre asincrona.
rpl_semi_sync_master_wait_point = AFTER_SYNC

# Los dos siguientes se dejan en su valor de fabrica A PROPOSITO, y se escriben
# aqui para que el numero sea una decision y no un descuido:
#
#   rpl_semi_sync_master_timeout = 10000
#     Al agotarse, el primario CAE A ASINCRONO Y SIGUE. Es lo correcto --una
#     replica muerta no puede bloquear los logins-- pero significa que la
#     garantia se apaga sola y en silencio. Como saber si sigue activa esta en
#     documentacion/despliegue-ansible.md: NO basta con
#     Rpl_semi_sync_master_status.
#
#   rpl_semi_sync_master_wait_no_slave = ON
#     Con una sola replica, es lo que evita que un reinicio de esa replica deje
#     al primario confirmando a ciegas durante la ventana.
rpl_semi_sync_master_timeout = 10000
rpl_semi_sync_master_wait_no_slave = ON

# Los DOS nodos arrancan en solo lectura. El primario lo quita el bootstrap del
# rol (tasks/replicacion.yml) y orchestrator lo quita al promover
# (ApplyMySQLPromotionAfterMasterFailover). Sin esto, un nodo que arranca
# despues de un failover aceptaria escrituras y bifurcaria el historial: dos
# primarios divergiendo es la averia que no tiene arreglo automatico.
#
# Una replica que reinicia vuelve sola a solo lectura, que es lo que se quiere.
read_only = ON

# TLS con el certificado de la CA interna. Se sirve, pero no se EXIGE
# (require_secure_transport se queda sin poner) por una razon concreta: quien
# tiene que negarse a hablar en claro es el cliente, y dex ya lo hace con
# dex_storage.ssl_mode "true". Exigirlo aqui obligaria tambien al agente de
# sonda, cuya conexion no sale del loopback de la propia maquina, a montar TLS
# para nada. Si algun dia hay clientes que no controlamos, esto se revisa.
ssl_ca = /etc/mysql/tls/ca.crt
ssl_cert = /etc/mysql/tls/node.crt
ssl_key = /etc/mysql/tls/node.key

# transaction_isolation NO se toca. Lo pone dex por conexion, en los parametros
# del DSN (storage/sql/config.go), y ahi es donde tiene que estar. El valor de
# fabrica de MariaDB es REPEATABLE-READ (comprobado), que es justo el
# aislamiento con el que dex NO debe correr.
```

- [ ] **Paso 4: La plantilla del compose**

Crear `ansible/roles/mariadb/templates/docker-compose.yml.j2`. El servicio de la sonda
llega en la tarea 2; aquí va solo el servidor:

```jinja
# Renderizado por Ansible. No editar aqui: los cambios se pierden en el
# proximo despliegue. La plantilla esta en ansible/roles/mariadb/templates/.
services:
    mariadb:
        image: {{ mariadb_image }}
        container_name: {{ mariadb_container_name }}
        restart: unless-stopped
        # Red de host: la replicacion es entre maquinas y orchestrator y HAProxy
        # llegan desde fuera. Quien limita la exposicion es bind_address en
        # 50-dex.cnf, no un mapeo de puertos.
        network_mode: host
        environment:
            # Las contrasenas van por FICHERO, no por valor: docker inspect
            # imprime el entorno del contenedor entero. Con el sufijo _FILE la
            # imagen lee el contenido y en el entorno solo queda la ruta
            # (comprobado con docker inspect sobre esta misma imagen).
            MARIADB_ROOT_PASSWORD_FILE: /run/secrets/root-password
            MARIADB_DATABASE: {{ mariadb_database }}
            MARIADB_USER: {{ mariadb_user }}
            MARIADB_PASSWORD_FILE: /run/secrets/dex-password
        volumes:
            - {{ mariadb_config_dir }}/50-dex.cnf:/etc/mysql/conf.d/50-dex.cnf:ro
            - {{ mariadb_config_dir }}/tls:/etc/mysql/tls:ro
            - {{ mariadb_config_dir }}/secrets:/run/secrets:ro
            # Volumen con nombre, NO un bind mount del arbol: un
            # "docker compose down -v" mal dado no puede llevarse los datos por
            # delante desde el rol.
            - mariadb-data:/var/lib/mysql
volumes:
    mariadb-data:
```

- [ ] **Paso 5: El handler**

Crear `ansible/roles/mariadb/handlers/main.yml`:

```yaml
# Igual que en los roles dex, valkey y haproxy: docker compose solo recrea un
# contenedor cuando cambia la definicion del propio servicio, no el contenido
# de un fichero montado por bind mount (aqui, 50-dex.cnf y el material TLS).
# recreate: always fuerza el reinicio cuando este handler salta.
- name: Reiniciar mariadb
  community.docker.docker_compose_v2:
    project_src: "{{ mariadb_config_dir }}"
    state: present
    recreate: always
```

- [ ] **Paso 6: Las tareas**

Crear `ansible/roles/mariadb/tasks/main.yml`:

```yaml
# Este nodo es nuestro: lo instala, escribe su configuracion y lo arranca este
# mismo rol, a diferencia de dex_storage --que avisa en vez de negarse porque
# la base de datos no era nuestra--. Desde esta fase si lo es.
- name: Las contrasenas de MariaDB no pueden quedar vacias sin querer
  ansible.builtin.assert:
    that:
      - (mariadb_root_password | default('') | length > 0) or (mariadb_allow_no_password | bool)
      - (dex_storage_password | default('') | length > 0) or (mariadb_allow_no_password | bool)
      - (mariadb_repl_password | default('') | length > 0) or (mariadb_allow_no_password | bool)
      - (mariadb_probe_password | default('') | length > 0) or (mariadb_allow_no_password | bool)
    fail_msg: >-
      Falta alguna de mariadb_root_password, dex_storage_password,
      mariadb_repl_password o mariadb_probe_password. Van cifradas con
      ansible-vault en group_vars. Si esto es un converge de prueba desechable,
      mariadb_allow_no_password: true.
    success_msg: Las cuatro contrasenas de MariaDB estan presentes.

# server_id no tiene default en el rol: es la identidad de replicacion y va
# explicita en el inventario. Un assert propio, porque el fallo por defecto
# ("'mariadb_server_id' is undefined" al renderizar la plantilla) no dice donde
# ponerlo ni por que no se deriva del grupo.
- name: Cada nodo declara su server_id, y el inventario es quien lo dice
  ansible.builtin.assert:
    that:
      - mariadb_server_id is defined
      - mariadb_server_id | int > 0
    fail_msg: >-
      {{ inventory_hostname }} no declara mariadb_server_id. Va en el inventario,
      distinto por nodo y mayor que cero. No se deriva del indice del grupo a
      proposito: reordenar el inventario reasignaria identidades de replicacion,
      y dos nodos con el mismo server_id rompen la replicacion sin decirlo.
    success_msg: mariadb_server_id declarado.

- name: Directorio de configuracion
  ansible.builtin.file:
    path: "{{ mariadb_config_dir }}"
    state: directory
    owner: "{{ mariadb_config_owner }}"
    group: "{{ mariadb_config_group }}"
    mode: "0750"

- name: Material TLS del nodo
  ansible.builtin.include_tasks: tls.yml

- name: Directorio de secretos
  ansible.builtin.file:
    path: "{{ mariadb_config_dir }}/secrets"
    state: directory
    owner: "{{ mariadb_config_owner }}"
    group: "{{ mariadb_config_group }}"
    mode: "0700"

- name: Las contrasenas que lee la imagen, en fichero y nunca en el entorno
  # docker inspect imprime el entorno del contenedor entero, asi que las
  # contrasenas van por MARIADB_*_FILE y en el entorno solo viaja la ruta.
  # no_log: sin esto, un -v o un --diff las imprimiria enteras.
  ansible.builtin.copy:
    content: "{{ item.valor }}"
    dest: "{{ mariadb_config_dir }}/secrets/{{ item.nombre }}"
    owner: "{{ mariadb_config_owner }}"
    group: "{{ mariadb_config_group }}"
    mode: "0600"
  loop:
    - {nombre: root-password, valor: "{{ mariadb_root_password | default('') }}"}
    - {nombre: dex-password, valor: "{{ dex_storage_password | default('') }}"}
  notify: Reiniciar mariadb
  no_log: true

- name: El fichero 50-dex.cnf, lo unico que Ansible posee
  ansible.builtin.template:
    src: 50-dex.cnf.j2
    dest: "{{ mariadb_config_dir }}/50-dex.cnf"
    owner: "{{ mariadb_config_owner }}"
    group: "{{ mariadb_config_group }}"
    mode: "0640"
  notify: Reiniciar mariadb

- name: Compose
  ansible.builtin.template:
    src: docker-compose.yml.j2
    dest: "{{ mariadb_config_dir }}/docker-compose.yml"
    mode: "0644"
  notify: Reiniciar mariadb

- name: Levantar mariadb
  community.docker.docker_compose_v2:
    project_src: "{{ mariadb_config_dir }}"
    state: present

# Los handlers corren al final de la tanda, y las tareas de replicacion
# (tarea 3) necesitan el nodo respondiendo YA. Este flush es lo que ordena las
# dos cosas.
- name: Aplicar los reinicios pendientes antes de tocar la replicacion
  ansible.builtin.meta: flush_handlers

- name: Esperar a que MariaDB acepte conexiones
  # 90 segundos y no 30: el PRIMER arranque de un primario con la semisincrona
  # encendida y sin replica todavia bloquea el timeout entero
  # (rpl_semi_sync_master_timeout, 10 s) durante el bootstrap de la propia
  # imagen, y deja un "Timeout waiting for reply of binlog" en el log. Es
  # esperado, no es un fallo, y una espera corta lo leeria como un despliegue
  # roto.
  ansible.builtin.wait_for:
    host: "{{ ansible_host | default('127.0.0.1') }}"
    port: "{{ mariadb_port }}"
    timeout: 90
```

- [ ] **Paso 7: Un converge de un solo nodo**

Antes de correrlo, añadir al inventario local (`ansible/inventories/local/hosts.yml`) un
grupo `mariadb` con este host, con las variables desechables — el mismo patrón que ya usa
`valkey-converge`:

```yaml
    mariadb:
      hosts:
        localhost: {}
      vars:
        mariadb_server_id: 1
        mariadb_container_name: t14-mariadb
        mariadb_config_dir: "{{ playbook_dir }}/../../.converge/mariadb"
        mariadb_port: 13306
        mariadb_probe_port: 13307
        mariadb_config_owner: "{{ ansible_facts.user_id }}"
        mariadb_config_group: "{{ ansible_facts.user_gid }}"
        mariadb_allow_no_password: true
```

Y en `ansible/inventories/local/group_vars/all.yml`, las contraseñas desechables
(`mariadb_root_password`, `mariadb_repl_password`, `mariadb_probe_password`,
`mariadb_orchestrator_password`) — este fichero no va cifrado porque es el del converge.

Añadir la tanda al playbook (la tarea 6 la deja en su sitio definitivo; aquí basta con
poder correrla):

```bash
cd /Users/antoniorodriguez/GITHUB-RASTY/dex
.venv/bin/ansible-playbook -i ansible/inventories/local/hosts.yml \
    ansible/playbooks/dex.yml --tags never --limit localhost \
    > /tmp/t14-converge.log 2>&1 < /dev/null; tail -30 /tmp/t14-converge.log
```

> `< /dev/null` no es decorativo: sin él, Ansible falla con «Ansible requires blocking
> IO» en esta máquina. Ya mordió en la fase 3.

Si es más cómodo, un playbook de un solo rol en el scratchpad sirve igual. Lo que importa
es que el rol corra contra este host.

- [ ] **Paso 8: Comprobar lo que la tarea promete**

```bash
docker exec t14-mariadb mariadb -uroot -p"$(cat .converge/mariadb/secrets/root-password)" \
    -N -B -e "SELECT @@rpl_semi_sync_master_wait_point, @@read_only, @@gtid_strict_mode, @@server_id;"
```

Esperado: `AFTER_SYNC	1	1	1`.

`AFTER_SYNC` es la línea que evita reintroducir el fallo de §3.2 en el siguiente
refactor, y `read_only` a 1 es la protección contra el historial bifurcado.

Y que el TLS está de verdad:

```bash
docker exec t14-mariadb mariadb -uroot -p"$(cat .converge/mariadb/secrets/root-password)" \
    -N -B -e "SHOW VARIABLES LIKE 'have_ssl';"
```

Esperado: `have_ssl	YES`.

- [ ] **Paso 9: Sabotear la comprobación**

Quitar la línea `rpl_semi_sync_master_wait_point = AFTER_SYNC` de la plantilla, volver a
converger y repetir el paso 8. Tiene que salir `AFTER_COMMIT` — el valor de fábrica de
MariaDB, que es exactamente el fallo que §3.2 describe. Restaurar la línea y converger
otra vez antes de seguir.

Si sale `AFTER_SYNC` con la línea quitada, algo más la está poniendo y la plantilla no es
la fuente de verdad: pararse ahí.

- [ ] **Paso 10: Lint y commit**

```bash
pre-commit run --files ansible/roles/mariadb/defaults/main.yml \
    ansible/roles/mariadb/tasks/main.yml ansible/roles/mariadb/tasks/tls.yml \
    ansible/roles/mariadb/handlers/main.yml \
    ansible/roles/mariadb/templates/50-dex.cnf.j2 \
    ansible/roles/mariadb/templates/docker-compose.yml.j2 \
    ansible/inventories/local/hosts.yml ansible/inventories/local/group_vars/all.yml
git add ansible/roles/mariadb ansible/inventories/local
git commit -m "feat(ansible): rol mariadb con tls y la semisincrona en after_sync"
```

El cuerpo del commit cuenta que `AFTER_COMMIT` es el valor de fábrica de MariaDB y qué
deshace, y que el primer arranque bloquea el timeout entero.

---

## Tarea 2: El agente de sonda

Deja el nodo respondiendo por HTTP si es primario escribible. Es lo que sustituye la única
cosa que ProxySQL aportaba (§3.5) y **es la fuente de verdad del enrutado**: si
orchestrator se equivoca, se queda sin quorum o se muere, HAProxy sigue mandando al nodo
que se declara escribible.

**Ficheros:**

- Crear: `ansible/roles/mariadb/templates/probe.sh.j2`
- Crear: `ansible/roles/mariadb/templates/probe.cnf.j2`
- Modificar: `ansible/roles/mariadb/templates/docker-compose.yml.j2` (servicio nuevo)
- Modificar: `ansible/roles/mariadb/tasks/main.yml` (renderizar los dos ficheros)

**Interfaces:**

- Consume: `mariadb_probe_port`, `mariadb_probe_user`, `mariadb_probe_password`,
  `mariadb_port` (tarea 1).
- Produce: un puerto HTTP en `mariadb_probe_port` que devuelve **200** con cuerpo
  `primario` o **503** con cuerpo `replica`. El rol `haproxy` (tarea 4) lo sondea con
  `check port`.

> El usuario `probe` lo crea la tarea 3, junto con los demás usuarios de servicio: es una
> escritura y necesita el primario ya escribible. Hasta entonces la sonda devuelve 503 en
> los dos nodos, que es la respuesta correcta cuando todavía no hay primario.

- [ ] **Paso 1: La plantilla del agente**

Crear `ansible/roles/mariadb/templates/probe.sh.j2`:

```jinja
#!/bin/sh
# Renderizado por Ansible. No editar aqui: los cambios se pierden en el
# proximo despliegue. La plantilla esta en ansible/roles/mariadb/templates/.
#
# Devuelve 200 solo si este nodo es primario escribible. La replica da 503 todo
# el rato y a proposito: no es una averia, es lo que la sonda tiene que decir, y
# HAProxy la muestra DOWN por eso.
#
# Un nodo que no responde a la consulta --MariaDB caida, disco lleno, demasiadas
# conexiones-- tambien da 503, que es lo correcto: no sirve aunque tecnicamente
# sea el primario.
#
# Las credenciales van en /probe.cnf (modo 0600), NO en el argv: docker top deja
# la linea de ordenes de cualquier proceso a la vista de cualquiera que este en
# la maquina. Es la misma leccion que ya costo una correccion con REDISCLI_AUTH
# en el rol valkey.
if mariadb --defaults-file=/probe.cnf -N -B -e \
     "SELECT @@read_only + @@innodb_read_only" 2>/dev/null | grep -qx 0
then
    printf 'HTTP/1.1 200 OK\r\nContent-Length: 8\r\nConnection: close\r\n\r\nprimario'
else
    printf 'HTTP/1.1 503 Service Unavailable\r\nContent-Length: 7\r\nConnection: close\r\n\r\nreplica'
fi
```

- [ ] **Paso 2: La plantilla de las credenciales**

Crear `ansible/roles/mariadb/templates/probe.cnf.j2`:

```jinja
# Renderizado por Ansible. No editar aqui.
#
# Este fichero existe para que la contrasena NO viaje en el argv del cliente:
# "mariadb -p<contrasena>" la deja legible con docker top mientras dura la
# consulta, y la sonda consulta cada pocos segundos.
[client]
user = {{ mariadb_probe_user }}
password = {{ mariadb_probe_password }}
host = 127.0.0.1
port = {{ mariadb_port }}
```

- [ ] **Paso 3: El servicio en el compose**

Añadir a `ansible/roles/mariadb/templates/docker-compose.yml.j2`, dentro de `services:`:

```jinja
    mariadb-probe:
        # La MISMA imagen que el servidor: ya trae socat y el cliente mariadb
        # (comprobado: /usr/bin/socat y /usr/bin/mariadb). No hace falta escribir
        # un binario ni construir una imagen propia para esto.
        image: {{ mariadb_image }}
        container_name: {{ mariadb_probe_container_name }}
        restart: unless-stopped
        network_mode: host
        # entrypoint sobrescrito: aqui no se arranca ningun servidor, solo socat.
        # Sin esto correria el entrypoint de MariaDB, que intentaria inicializar
        # un segundo directorio de datos.
        entrypoint: ["socat"]
        command:
            - "-T2"
            - "TCP-LISTEN:{{ mariadb_probe_port }},fork,reuseaddr"
            - "SYSTEM:/probe.sh"
        # Como el usuario mysql y no como root: lo unico que necesita leer es su
        # propio probe.cnf, que es suyo y esta en 0600.
        user: "{{ mariadb_config_owner }}:{{ mariadb_config_group }}"
        volumes:
            - {{ mariadb_config_dir }}/probe.sh:/probe.sh:ro
            - {{ mariadb_config_dir }}/probe.cnf:/probe.cnf:ro
        depends_on:
            - mariadb
```

- [ ] **Paso 4: Renderizarlos**

Añadir a `ansible/roles/mariadb/tasks/main.yml`, **antes** de la tarea «Compose»:

```yaml
- name: El agente de sonda
  ansible.builtin.template:
    src: probe.sh.j2
    dest: "{{ mariadb_config_dir }}/probe.sh"
    owner: "{{ mariadb_config_owner }}"
    group: "{{ mariadb_config_group }}"
    mode: "0755"
  notify: Reiniciar mariadb

- name: Credenciales de la sonda, fuera del argv
  # no_log: lleva mariadb_probe_password en claro.
  ansible.builtin.template:
    src: probe.cnf.j2
    dest: "{{ mariadb_config_dir }}/probe.cnf"
    owner: "{{ mariadb_config_owner }}"
    group: "{{ mariadb_config_group }}"
    mode: "0600"
  notify: Reiniciar mariadb
  no_log: true
```

- [ ] **Paso 5: Converger y probar los tres casos**

El usuario `probe` todavía no existe (lo crea la tarea 3), así que para probar el agente
ahora hay que crearlo a mano en el nodo del converge:

```bash
R=$(cat .converge/mariadb/secrets/root-password)
docker exec t14-mariadb mariadb -uroot -p"$R" -e \
  "SET GLOBAL read_only=OFF;
   CREATE USER IF NOT EXISTS 'probe'@'%' IDENTIFIED BY 'sonda';
   GRANT USAGE ON *.* TO 'probe'@'%';"
```

Los tres casos, y el tercero es el que suele faltar:

```bash
# 1. Primario escribible -> 200
printf 'GET / HTTP/1.0\r\n\r\n' | nc 127.0.0.1 13307 | head -1

# 2. En solo lectura -> 503
docker exec t14-mariadb mariadb -uroot -p"$R" -e "SET GLOBAL read_only=ON"
printf 'GET / HTTP/1.0\r\n\r\n' | nc 127.0.0.1 13307 | head -1

# 3. MariaDB parada -> 503, no un cuelgue ni una conexion rechazada
docker stop t14-mariadb
printf 'GET / HTTP/1.0\r\n\r\n' | nc 127.0.0.1 13307 | head -1
docker start t14-mariadb
```

Esperado: `HTTP/1.1 200 OK`, `HTTP/1.1 503 Service Unavailable`,
`HTTP/1.1 503 Service Unavailable`.

- [ ] **Paso 6: Comprobar que la contraseña no está en el `argv`**

```bash
docker top t14-mariadb-probe
```

No puede aparecer la contraseña en ninguna línea. **Sabotaje:** cambiar en la plantilla
`--defaults-file=/probe.cnf` por `-p{{ mariadb_probe_password }}`, converger, hacer una
petición a la sonda y correr `docker top` **mientras** — la contraseña tiene que aparecer.
Restaurar la plantilla y converger.

> Con `-T2` y `fork`, el proceso hijo del cliente vive poco: para verlo hay que lanzar la
> petición en bucle mientras se mira. `while true; do printf 'GET / HTTP/1.0\r\n\r\n' | nc
> 127.0.0.1 13307 >/dev/null; done &` sirve, y se mata al terminar.

- [ ] **Paso 7: Comprobar el usuario de la sonda**

Que un usuario con **solo `USAGE`** basta para la consulta, y que no puede nada más:

```bash
docker exec t14-mariadb mariadb -uprobe -psonda -N -B -e \
    "SELECT @@read_only + @@innodb_read_only"          # 0 o 1, sin error
docker exec t14-mariadb mariadb -uprobe -psonda -e \
    "SELECT * FROM mysql.user" 2>&1 | head -2           # tiene que ser un denegado
```

Un agente alcanzable desde la red no debe poder hacer nada más que leer esas dos
variables.

- [ ] **Paso 8: Lint y commit**

```bash
pre-commit run --files ansible/roles/mariadb/templates/probe.sh.j2 \
    ansible/roles/mariadb/templates/probe.cnf.j2 \
    ansible/roles/mariadb/templates/docker-compose.yml.j2 \
    ansible/roles/mariadb/tasks/main.yml
git add ansible/roles/mariadb
git commit -m "feat(ansible): agente de sonda que dice cual es el primario escribible"
```

El cuerpo cuenta por qué la sonda manda y orchestrator no, y que la contraseña va por
fichero porque `docker top` lee el `argv`.

---

## Tarea 3: El bootstrap de la replicación

La tarea con más superficie de daño de toda la fase: establecer replicación sobre un par
vivo lo rompe, igual que `--cluster create` sobre un cluster ya formado en el rol
`valkey`. Se detecta antes de tocar nada y hay **cuatro** salidas, no dos.

**Ficheros:**

- Crear: `ansible/roles/mariadb/tasks/replicacion.yml`
- Modificar: `ansible/roles/mariadb/tasks/main.yml` (incluirlo al final)

**Interfaces:**

- Consume: todo lo de la tarea 1, más `mariadb_repl_password`, `mariadb_probe_password`,
  `mariadb_orchestrator_password`.
- Produce: el par replicando; los usuarios `repl`, `probe` y `orchestrator` creados. El
  rol `orchestrator` (tarea 5) usa el tercero.

- [ ] **Paso 1: Escribir las tareas de detección**

Crear `ansible/roles/mariadb/tasks/replicacion.yml`. Primero, mirar sin tocar:

```yaml
# Establecer la replicacion es la unica operacion realmente destructiva de este
# rol: rehacerla sobre un par vivo lo rompe, igual que --cluster create sobre un
# cluster ya formado en el rol valkey. Por eso se detecta antes de tocar nada, y
# hay CUATRO salidas y no dos:
#
#   a) ya hay replicacion  -> no se hace nada
#   b) nadie replica y nadie escribe -> arranque inicial, se establece una vez
#   c) mas de un nodo escribible -> historial bifurcado: se para y se avisa
#   d) el primario ya tiene datos y la replica no replica -> se niega a clonar
#
# El papel de primario lo decide el rol SOLO en el arranque inicial. A partir de
# ahi manda orchestrator y el rol LEE quien es, no lo asigna: un rol que
# reasigna el papel en cada pasada deshace el failover que acababa de pasar, que
# es exactamente el fallo que el rol valkey evita no tocando valkey.conf.
- name: Estado de replicacion de cada nodo
  community.docker.docker_container_exec:
    container: "{{ mariadb_container_name }}"
    command: >-
      mariadb --defaults-file=/run/secrets/root.cnf -N -B
      -e "SELECT @@read_only, @@gtid_binlog_pos,
                 (SELECT COUNT(*) FROM information_schema.tables
                   WHERE table_schema = '{{ mariadb_database }}'),
                 (SELECT COUNT(*) FROM information_schema.processlist
                   WHERE command = 'Slave_IO')"
  register: mariadb_estado
  changed_when: false
  failed_when: false
  no_log: true

- name: El nodo respondio, esto no es un contenedor que todavia arranca
  # Un rc distinto de 0 no es una topologia rota: es un nodo que no ha
  # contestado todavia. Separarlo del mensaje de "historial bifurcado" evita que
  # un despliegue que solo llego tarde se lea como una averia de datos.
  ansible.builtin.assert:
    that: mariadb_estado.rc == 0
    fail_msg: >-
      {{ inventory_hostname }} no respondio a la consulta de estado
      (rc={{ mariadb_estado.rc }}). Puede que el contenedor siga arrancando: el
      primer arranque de un primario con la semisincrona encendida bloquea 10
      segundos de mas a proposito. Repite el despliegue cuando el nodo este
      arriba.

- name: Descomponer el estado de este nodo
  ansible.builtin.set_fact:
    mariadb_es_escribible: "{{ (mariadb_estado.stdout.split() | first) == '0' }}"
    mariadb_gtid_pos: "{{ mariadb_estado.stdout.split()[1] }}"
    mariadb_tiene_datos: "{{ (mariadb_estado.stdout.split()[2] | int) > 0 }}"
    mariadb_ya_replica: "{{ (mariadb_estado.stdout.split()[3] | int) > 0 }}"
```

> `information_schema.processlist` con `command = 'Slave_IO'` es la forma de preguntar «¿este
> nodo replica?» que devuelve una fila contable. `SHOW REPLICA STATUS` devuelve **vacío sin
> error** cuando no hay replicación (comprobado en 11.4), lo que lo hace incómodo de
> registrar: sirve para mirarlo a mano, no para decidir en un `when`.

El fichero `/run/secrets/root.cnf` no existe todavía: se añade en el paso 2.

- [ ] **Paso 2: El fichero de credenciales de root, para no repetir la contraseña**

En `ansible/roles/mariadb/tasks/main.yml`, dentro del bucle que escribe los secretos,
añadir una tercera entrada — un fichero de opciones completo, para que ninguna tarea de
este rol tenga que pasar `-p` por el `argv`:

```yaml
    - nombre: root.cnf
      valor: |
        [client]
        user = root
        password = {{ mariadb_root_password | default('') }}
        host = 127.0.0.1
        port = {{ mariadb_port }}
```

- [ ] **Paso 3: Las tres negativas**

Seguir en `replicacion.yml`:

```yaml
- name: Dos nodos escribibles a la vez no se reparan solos
  # Historial bifurcado: los dos han podido aceptar escrituras distintas. Un rol
  # que "arregle" esto eligiendo uno tira las escrituras del otro sin decirlo.
  ansible.builtin.assert:
    that: >-
      (groups['mariadb'] | map('extract', hostvars)
       | selectattr('mariadb_es_escribible', 'defined')
       | selectattr('mariadb_es_escribible') | list | length) <= 1
    fail_msg: >-
      Mas de un nodo de MariaDB se declara escribible. Eso es un historial
      bifurcado y no se toca desde aqui: cada uno ha podido aceptar escrituras
      que el otro no tiene. Revisalo a mano --GTID de cada nodo, binlogs-- y
      decide cual sobrevive antes de volver a desplegar.
  run_once: true

- name: Elegir el primario, leyendolo y solo asignandolo en el arranque inicial
  # Si alguien ya es escribible, ESE es el primario: puede haberlo promovido
  # orchestrator. Solo cuando nadie lo es --arranque inicial-- se elige el
  # primer host del grupo.
  ansible.builtin.set_fact:
    mariadb_primario: >-
      {{ (groups['mariadb'] | select('extract', hostvars, 'mariadb_es_escribible') | list | first)
         | default(groups['mariadb'] | first) }}
  run_once: true

- name: Con datos en el primario, la replica no se clona desde aqui
  # Clonar bases de datos en produccion desde un playbook, sin ventana ni
  # comprobacion de tamano, es un mecanismo que no se puede probar aqui y que
  # borra datos si se equivoca.
  #
  # Y hay una razon tecnica ademas de la prudente: el enganche de este rol salta
  # la historia del primario fijando gtid_slave_pos (ver el paso siguiente). Eso
  # SOLO es correcto cuando la replica no tiene nada que perder. Con datos ya
  # escritos, saltarse la historia deja una replica silenciosamente incompleta,
  # que es mucho peor que una que se para con un error.
  ansible.builtin.assert:
    that: not (hostvars[mariadb_primario].mariadb_tiene_datos | bool)
    fail_msg: >-
      El primario ({{ mariadb_primario }}) ya tiene tablas en la base
      "{{ mariadb_database }}" y {{ inventory_hostname }} todavia no replica. El
      rol NO clona: hazlo a mano y vuelve a desplegar.
        1) En el primario:  mariadb-dump --defaults-file=... --all-databases
                              --master-data=2 --single-transaction --gtid > volcado.sql
           (o mariabackup, que no bloquea, si la base es grande)
        2) En la replica:   cargar el volcado y fijar gtid_slave_pos con el valor
                            que el propio volcado trae en su cabecera
        3) Volver a lanzar el playbook: detectara la replicacion y no tocara nada
  when:
    - inventory_hostname != mariadb_primario
    - not (mariadb_ya_replica | bool)
```

- [ ] **Paso 4: Los usuarios de servicio y el arranque de la replicación**

```yaml
- name: Quitar el solo lectura del primario
  # Los dos nodos arrancan en read_only (ver 50-dex.cnf.j2). Este es el unico
  # sitio donde el rol lo quita, y solo al primario.
  community.docker.docker_container_exec:
    container: "{{ mariadb_container_name }}"
    command: >-
      mariadb --defaults-file=/run/secrets/root.cnf
      -e "SET GLOBAL read_only = OFF"
  when: inventory_hostname == mariadb_primario
  changed_when: true
  no_log: true

- name: Los tres usuarios de servicio, en el primario
  # Se crean solo en el primario a proposito: viajan a la replica por el binlog.
  # Cada uno con los privilegios justos de su oficio, y el de la sonda con
  # ninguno: USAGE basta para leer @@read_only y @@innodb_read_only (comprobado),
  # y un agente alcanzable desde la red no debe poder hacer nada mas.
  #
  # CREATE USER IF NOT EXISTS mas GRANT es idempotente; ALTER USER refresca la
  # contrasena si cambio en el vault sin fallar si no cambio.
  community.docker.docker_container_exec:
    container: "{{ mariadb_container_name }}"
    command: >-
      mariadb --defaults-file=/run/secrets/root.cnf -e "{{ item }}"
  loop:
    - >-
      CREATE USER IF NOT EXISTS '{{ mariadb_repl_user }}'@'%'
      IDENTIFIED BY '{{ mariadb_repl_password }}';
      ALTER USER '{{ mariadb_repl_user }}'@'%'
      IDENTIFIED BY '{{ mariadb_repl_password }}';
      GRANT REPLICATION SLAVE ON *.* TO '{{ mariadb_repl_user }}'@'%';
    - >-
      CREATE USER IF NOT EXISTS '{{ mariadb_probe_user }}'@'%'
      IDENTIFIED BY '{{ mariadb_probe_password }}';
      ALTER USER '{{ mariadb_probe_user }}'@'%'
      IDENTIFIED BY '{{ mariadb_probe_password }}';
      GRANT USAGE ON *.* TO '{{ mariadb_probe_user }}'@'%';
    - >-
      CREATE USER IF NOT EXISTS '{{ mariadb_orchestrator_user }}'@'%'
      IDENTIFIED BY '{{ mariadb_orchestrator_password }}';
      ALTER USER '{{ mariadb_orchestrator_user }}'@'%'
      IDENTIFIED BY '{{ mariadb_orchestrator_password }}';
      GRANT SUPER, PROCESS, REPLICATION SLAVE, REPLICATION CLIENT, RELOAD
        ON *.* TO '{{ mariadb_orchestrator_user }}'@'%';
      GRANT SELECT ON mysql.slave_master_info
        TO '{{ mariadb_orchestrator_user }}'@'%';
  when: inventory_hostname == mariadb_primario
  changed_when: true
  no_log: true

- name: Punto de partida de la replica, saltando el bootstrap que ya hizo
  # ESTE es el paso que la spec no tenia y sin el la replicacion no arranca.
  #
  # La imagen de MariaDB, en su primer arranque, crea la base y el usuario de dex
  # -- y esas sentencias entran en el binlog del primario. La replica hizo su
  # propio bootstrap identico por su cuenta. Con gtid_slave_pos vacio, la replica
  # reproduce el binlog del primario DESDE EL PRINCIPIO, incluido ese bootstrap,
  # y se para en seco:
  #
  #   Last_SQL_Error: Error 'Operation CREATE USER failed for 'dex'@'%'' on query
  #
  # Reproducido en vivo. Fijar gtid_slave_pos al gtid_binlog_pos actual del
  # primario salta esa historia, y es correcto SOLO porque la tarea anterior ya
  # se ha negado a seguir si el primario tenia datos.
  community.docker.docker_container_exec:
    container: "{{ mariadb_container_name }}"
    command: >-
      mariadb --defaults-file=/run/secrets/root.cnf
      -e "SET GLOBAL gtid_slave_pos = '{{ hostvars[mariadb_primario].mariadb_gtid_pos }}'"
  when:
    - inventory_hostname != mariadb_primario
    - not (mariadb_ya_replica | bool)
  changed_when: true
  no_log: true

- name: Enganchar la replica al primario
  # master_use_gtid = slave_pos, no coordenadas de fichero y posicion: es lo que
  # permite a orchestrator repuntar la topologia sin tener que calcularlas.
  community.docker.docker_container_exec:
    container: "{{ mariadb_container_name }}"
    command: >-
      mariadb --defaults-file=/run/secrets/root.cnf
      -e "CHANGE MASTER TO
            master_host = '{{ hostvars[mariadb_primario].ansible_host }}',
            master_port = {{ mariadb_port }},
            master_user = '{{ mariadb_repl_user }}',
            master_password = '{{ mariadb_repl_password }}',
            master_ssl = 1,
            master_ssl_ca = '/etc/mysql/tls/ca.crt',
            master_use_gtid = slave_pos;
          START REPLICA"
  when:
    - inventory_hostname != mariadb_primario
    - not (mariadb_ya_replica | bool)
  changed_when: true
  no_log: true
```

- [ ] **Paso 5: Comprobar que quedó enganchada de verdad**

Una tarea que se ejecuta siempre, no solo cuando se acaba de enganchar: es la que delata
una replicación que se paró entre despliegues.

```yaml
- name: La replica esta al dia
  community.docker.docker_container_exec:
    container: "{{ mariadb_container_name }}"
    command: >-
      mariadb --defaults-file=/run/secrets/root.cnf -N -B
      -e "SELECT COUNT(*) FROM information_schema.processlist
           WHERE command IN ('Slave_IO', 'Slave_SQL')"
  register: mariadb_hilos_replica
  when: inventory_hostname != mariadb_primario
  changed_when: false
  failed_when: false
  no_log: true

- name: Los dos hilos de replicacion tienen que estar corriendo
  # Dos: el de E/S y el de SQL. Con uno solo la replica esta rota de una forma
  # que no se nota hasta el failover, que es cuando ya no sirve enterarse.
  ansible.builtin.assert:
    that: mariadb_hilos_replica.stdout | trim | int == 2
    fail_msg: >-
      {{ inventory_hostname }} no tiene los dos hilos de replicacion corriendo
      (encontrados: {{ mariadb_hilos_replica.stdout | trim }}). Miralo con
      "SHOW REPLICA STATUS\G" dentro del contenedor: Last_SQL_Error suele decir
      exactamente que sentencia la paro.
  when: inventory_hostname != mariadb_primario
```

Y al final de `ansible/roles/mariadb/tasks/main.yml`:

```yaml
- name: Replicacion
  ansible.builtin.include_tasks: replicacion.yml
```

- [ ] **Paso 6: Probar el par entero**

El converge local es de un solo nodo. Para esta tarea hacen falta dos, así que se levantan
dos contenedores con el rol usando dos nombres de inventario y dos `mariadb_config_dir`,
igual que la fase 3 probó dos HAProxy. Añadir al inventario local un segundo host
(`mariadb-2`, `ansible_connection: local`, `mariadb_server_id: 2`, puerto `13316`, sonda
`13317`, su propio directorio).

Correr el playbook y comprobar:

```bash
R=$(cat .converge/mariadb/secrets/root-password)
# El primario: escribible, con la replica conectada
docker exec t14-mariadb mariadb -uroot -p"$R" -N -B -e \
  "SHOW STATUS WHERE Variable_name IN
     ('Rpl_semi_sync_master_status','Rpl_semi_sync_master_clients')"
# La replica: los dos hilos y sin retraso
docker exec t14-mariadb-2 mariadb -uroot -p"$R" -e "SHOW REPLICA STATUS\G" \
  | grep -E "Slave_IO_Running:|Slave_SQL_Running:|Seconds_Behind_Master|Using_Gtid"
```

Esperado: `Rpl_semi_sync_master_clients 1`, `Rpl_semi_sync_master_status ON`,
`Slave_IO_Running: Yes`, `Slave_SQL_Running: Yes`, `Seconds_Behind_Master: 0`,
`Using_Gtid: Slave_Pos`.

Y que una escritura viaja:

```bash
docker exec t14-mariadb mariadb -uroot -p"$R" -e \
  "CREATE TABLE dex.viaja(i INT); INSERT INTO dex.viaja VALUES (42);"
sleep 2
docker exec t14-mariadb-2 mariadb -uroot -p"$R" -N -B -e "SELECT i FROM dex.viaja"
```

Esperado: `42`.

- [ ] **Paso 7: Sabotear el paso que la spec no tenía**

Es el sabotaje que más importa de la fase. Quitar la tarea «Punto de partida de la
réplica», borrar los dos contenedores y sus volúmenes, y converger de cero:

```bash
docker compose -f .converge/mariadb/docker-compose.yml down -v
docker compose -f .converge/mariadb-2/docker-compose.yml down -v
# ... converger ...
docker exec t14-mariadb-2 mariadb -uroot -p"$R" -e "SHOW REPLICA STATUS\G" \
  | grep -E "Slave_SQL_Running:|Last_SQL_Error"
```

Esperado con el sabotaje: `Slave_SQL_Running: No` y
`Last_SQL_Error: Error 'Operation CREATE USER failed for 'dex'@'%'' on query`.

Si sale `Yes`, el `SET GLOBAL gtid_slave_pos` no era lo que hacía falta y hay que entender
por qué antes de seguir. Restaurar la tarea y converger de cero otra vez.

- [ ] **Paso 8: Sabotear la negativa a clonar**

Con el par ya replicando y con datos (`dex.viaja` existe), parar la réplica, hacerle
`RESET REPLICA ALL`, y volver a converger. El playbook tiene que **fallar** con el mensaje
de «El primario ya tiene tablas», no ponerse a enganchar.

- [ ] **Paso 9: Idempotencia**

Converger dos veces seguidas sobre el par ya montado. La segunda pasada no puede tocar la
replicación: `SHOW REPLICA STATUS` tiene que seguir dando lo mismo y `Seconds_Behind_Master`
seguir en 0. Es la comprobación de que el rol **lee** el papel y no lo reasigna.

- [ ] **Paso 10: Limpiar, lint y commit**

```bash
docker compose -f .converge/mariadb/docker-compose.yml down -v
docker compose -f .converge/mariadb-2/docker-compose.yml down -v
pre-commit run --files ansible/roles/mariadb/tasks/replicacion.yml \
    ansible/roles/mariadb/tasks/main.yml ansible/inventories/local/hosts.yml
git add ansible/roles/mariadb ansible/inventories/local
git commit -m "feat(ansible): bootstrap de la replicacion semisincrona, una sola vez"
```

El cuerpo cuenta el hallazgo del `gtid_slave_pos` con el error exacto que produce sin él,
y por qué el rol lee el papel de primario en vez de asignarlo.

---

## Tarea 4: Los cambios en el rol `haproxy`

**Ficheros:**

- Modificar: `ansible/roles/haproxy/defaults/main.yml`
- Modificar: `ansible/roles/haproxy/templates/haproxy.cfg.j2`

**Interfaces:**

- Consume: `mariadb_probe_port` y `mariadb_port` del rol `mariadb` (tarea 1), leídos
  porque los dos roles corren en la misma ejecución — el mismo acoplamiento que ya existe
  entre `keepalived` y `haproxy`.
- Produce: `haproxy_mysql_port`, el puerto por el que dex habla con la base de datos.

- [ ] **Paso 1: El default nuevo**

Añadir a `ansible/roles/haproxy/defaults/main.yml`:

```yaml
# El puerto por el que se publica MariaDB. Es el mismo 3306 de dentro: cambiarlo
# no aporta nada y obligaria a tocar dex_storage.port en el inventario.
#
# mariadb_probe_port NO se declara aqui, y no es un olvido: es del rol mariadb.
# Sin default en el rol que no es su dueno, correr haproxy suelto falla con
# "undefined variable" en vez de sondear un puerto inventado, que dejaria todos
# los backends DOWN sin decir por que. Mismo criterio que keepalived con
# haproxy_https_port.
haproxy_mysql_port: 3306
```

- [ ] **Paso 2: El frontend y el backend**

Añadir al final de `ansible/roles/haproxy/templates/haproxy.cfg.j2`:

```jinja
{% if groups['mariadb'] is defined and groups['mariadb'] | length > 0 %}

frontend fe_mysql
    # mode tcp, y aqui NO es solo por no terminar el TLS. Un proxy que
    # MULTIPLEXA conexiones de cliente sobre conexiones de backend tendria que
    # rastrear el transaction_isolation que dex mete en cada conexion
    # (SERIALIZABLE, en los parametros del DSN), o dex acabaria corriendo en el
    # REPEATABLE-READ de fabrica de MariaDB sin decir una palabra. Es la misma
    # perdida silenciosa de correccion por la que se descarto Galera, entrando
    # por otra puerta. En mode tcp esto es un tubo de bytes y la sesion es de
    # punta a punta: no hay nada que rastrear.
    bind :{{ haproxy_mysql_port }}
    mode tcp
    option tcplog
    default_backend be_mysql

backend be_mysql
    mode tcp
    # Un solo nodo activo a la vez: el que la sonda declara primario. No es
    # balanceo, es enrutado, y "balance first" lo deja explicito. La replica
    # figura DOWN todo el rato y a proposito.
    #
    # NI una lectura va a la replica: con replicacion por debajo, una lectura
    # enviada ahi puede no ver un codigo de autorizacion recien escrito, y eso
    # son logins que fallan sin motivo aparente y sin dejar rastro util. Dex
    # ademas usa el mismo pool para todo, asi que no hay forma de marcar "esta
    # consulta tolera retraso". No hay backend de lecturas: es una decision, no
    # una optimizacion pendiente.
    balance first
    # check port es la pieza: la salud se comprueba en el puerto del agente y el
    # trafico va al 3306. Sondeando el propio 3306 no habria forma de distinguir
    # un primario de una replica -- los dos aceptan la conexion.
    option httpchk
    http-check send meth GET uri /
    http-check expect status 200
{% for h in groups['mariadb'] %}
    server {{ h }} {{ hostvars[h].ansible_host }}:{{ mariadb_port }} check port {{ mariadb_probe_port }}
{% endfor %}
{% endif %}
```

> El `{% if %}` no es cosmético: un inventario de la fase 3, sin grupo `mariadb`, tiene
> que seguir desplegando igual que hoy. Es la restricción global de no cambiar el
> comportamiento de los roles ya entregados.

- [ ] **Paso 3: Comprobar que la configuración es válida en los dos casos**

Con grupo `mariadb` y sin él. Renderizar y validar con el propio HAProxy:

```bash
docker run --rm -v "$PWD/.converge/haproxy/haproxy.cfg:/tmp/haproxy.cfg:ro" \
    haproxy:3.0-alpine haproxy -c -f /tmp/haproxy.cfg
```

Esperado: `Configuration file is valid`, **y sin ningún `[WARNING]`**. Un aviso constante
es donde se esconde el aviso que sí importa — la lección de `option httplog` en la fase 3.

- [ ] **Paso 4: Sabotear el `check port`**

Quitar `check port {{ mariadb_probe_port }}` de la línea `server`, converger, y mirar el
estado de los backends. Sin él, HAProxy sondea el 3306 con `option httpchk`: MariaDB
acepta la conexión pero no habla HTTP, así que los **dos** nodos salen `DOWN` — y si en
vez de `httpchk` quedara un chequeo TCP, saldrían los **dos UP**, que es peor: HAProxy
mandaría escrituras a la réplica. Restaurar la línea.

- [ ] **Paso 5: Lint y commit**

```bash
pre-commit run --files ansible/roles/haproxy/defaults/main.yml \
    ansible/roles/haproxy/templates/haproxy.cfg.j2
git add ansible/roles/haproxy
git commit -m "feat(ansible): frontend de mysql en el balanceador, enrutado por la sonda"
```

---

## Tarea 5: El rol `orchestrator`

Detecta la caída del primario y promueve. **No decide a dónde va el tráfico**: eso lo hace
la sonda (§3.5). Esa separación es lo que hace que un orchestrator equivocado, sin quorum
o muerto no rompa el despliegue.

**Ficheros:**

- Crear: `ansible/roles/orchestrator/defaults/main.yml`
- Crear: `ansible/roles/orchestrator/tasks/main.yml`
- Crear: `ansible/roles/orchestrator/handlers/main.yml`
- Crear: `ansible/roles/orchestrator/templates/orchestrator.conf.json.j2`
- Crear: `ansible/roles/orchestrator/templates/docker-compose.yml.j2`

**Interfaces:**

- Consume: `mariadb_orchestrator_user` y `mariadb_orchestrator_password` (tarea 3),
  `groups['orchestrator']` (tarea 6).
- Produce: nada que otro rol lea. Es deliberado: **no hay `PostFailoverProcesses` que
  toquen HAProxy**, porque eso sería devolverle a orchestrator el poder que §3.5 le quita.

- [ ] **Paso 1: Sacar el fichero de ejemplo de la imagen y confirmar los nombres**

orchestrator lleva sin publicar versión desde 2022 y su configuración es un JSON con
decenas de claves. **Antes de escribir la plantilla**, extraer el ejemplo que trae la
propia imagen y confirmar contra él cada clave que se vaya a usar:

```bash
docker run --rm --entrypoint sh \
  openarkcode/orchestrator@sha256:789b9e7c093f3c3c2b2ff7fb5a01f4c3d85b42124213e93b765d23c10d58674a \
  -c 'ls /; cat /orchestrator.conf.json 2>/dev/null || find / -name "orchestrator*conf*json*" 2>/dev/null'
```

Confirmar que existen y con qué nombre exacto: `MySQLTopologyUser`,
`MySQLTopologyPassword`, `BackendDB`, `SQLite3DataFile`, `RaftEnabled`, `RaftDataDir`,
`RaftBind`, `RaftNodes`, `DefaultRaftPort`, `ApplyMySQLPromotionAfterMasterFailover`,
`FailureDetectionPeriodBlockMinutes`, `RecoveryPeriodBlockSeconds`,
`RecoverMasterClusterFilters`, `InstancePollSeconds`, y las de TLS hacia la topología.

**Si alguna no existe con ese nombre, se usa la del ejemplo y se anota en el commit.**
Escribir claves inventadas en este JSON no da error: orchestrator ignora lo que no
entiende, así que un nombre mal puesto se manifiesta como un failover que no ocurre el día
que hace falta.

- [ ] **Paso 2: Los defaults**

Crear `ansible/roles/orchestrator/defaults/main.yml`:

```yaml
orchestrator_config_dir: /etc/orchestrator
orchestrator_container_name: orchestrator

# Fijado por DIGEST y no por etiqueta, que es la unica excepcion a la regla del
# repositorio -- y lo es porque openarkcode/orchestrator NO publica etiquetas de
# version en Docker Hub: solo "latest". Lo que la regla protege es la
# inmutabilidad, y un digest la da mejor que una etiqueta.
#
# Para obtener uno nuevo:
#   docker manifest inspect openarkcode/orchestrator:latest
# y leer el digest del manifiesto de la plataforma que toque. Cambiarlo aqui es
# una decision consciente: la ultima version de orchestrator es de 2022, asi que
# un digest nuevo no trae correcciones, trae otra construccion de lo mismo.
orchestrator_image: >-
  openarkcode/orchestrator@sha256:789b9e7c093f3c3c2b2ff7fb5a01f4c3d85b42124213e93b765d23c10d58674a

orchestrator_http_port: 3000
orchestrator_raft_port: 10008

# Ventanas de deteccion y recuperacion, explicitas. Los valores de fabrica son
# razonables, pero un failover automatico con ventanas que nadie eligio es un
# failover que nadie puede explicar despues.
#
#   InstancePollSeconds: cada cuanto pregunta a cada nodo.
#   FailureDetectionPeriodBlockMinutes: cuanto tiempo NO vuelve a detectar el
#     mismo fallo, para no encadenar detecciones sobre una averia que sigue.
#   RecoveryPeriodBlockSeconds: cuanto espera antes de otra recuperacion sobre
#     el mismo cluster. Corto encadena promociones; largo deja el par degradado.
orchestrator_poll_seconds: 5
orchestrator_failure_detection_block_minutes: 60
orchestrator_recovery_block_seconds: 3600
```

- [ ] **Paso 3: La plantilla de configuración**

Crear `ansible/roles/orchestrator/templates/orchestrator.conf.json.j2` con las claves
confirmadas en el paso 1:

```jinja
{
    "_comentario": "Renderizado por Ansible. No editar aqui: los cambios se pierden en el proximo despliegue. La plantilla esta en ansible/roles/orchestrator/templates/.",
    "Debug": false,
    "ListenAddress": ":{{ orchestrator_http_port }}",

    "MySQLTopologyUser": "{{ mariadb_orchestrator_user }}",
    "MySQLTopologyPassword": "{{ mariadb_orchestrator_password }}",
    "MySQLTopologySSLCAFile": "/etc/orchestrator/tls/ca.crt",
    "MySQLTopologySSLSkipVerify": false,
    "MySQLTopologyUseMutualTLS": false,
    "InstancePollSeconds": {{ orchestrator_poll_seconds }},

    "BackendDB": "sqlite",
    "SQLite3DataFile": "/var/lib/orchestrator/orchestrator.db",

    "RaftEnabled": true,
    "RaftDataDir": "/var/lib/orchestrator",
    "RaftBind": "{{ ansible_host }}",
    "DefaultRaftPort": {{ orchestrator_raft_port }},
    "RaftNodes": [
{% for h in groups['orchestrator'] %}
        "{{ hostvars[h].ansible_host }}"{{ "," if not loop.last }}
{% endfor %}
    ],

    "FailureDetectionPeriodBlockMinutes": {{ orchestrator_failure_detection_block_minutes }},
    "RecoveryPeriodBlockSeconds": {{ orchestrator_recovery_block_seconds }},
    "RecoverMasterClusterFilters": ["*"],
    "ApplyMySQLPromotionAfterMasterFailover": true
}
```

> `RaftEnabled` con los tres nodos y no tres orchestrator sueltos: sin raft pueden promover
> tres cosas distintas. Con dos nodos de datos, la decisión no puede tomarse ahí — un nodo
> solo no distingue «el otro se ha muerto» de «me he quedado aislado» — así que el quorum
> vive en la capa de decisión, con el tercer voto en una máquina que no es de base de
> datos. Es el mismo patrón que el tercer sentinel de la fase 2.
>
> `ApplyMySQLPromotionAfterMasterFailover: true` es lo que quita el `read_only` del
> promovido. **Sin esto la promoción ocurre y la sonda sigue dando 503 en los dos**: el
> despliegue se queda sin escrituras con un failover «correcto».
>
> **No hay `PostFailoverProcesses`.** La sonda ya resuelve el enrutado, y acoplarlos sería
> devolverle a orchestrator el poder que se le quita a propósito.

- [ ] **Paso 4: El compose y el handler**

`ansible/roles/orchestrator/templates/docker-compose.yml.j2`:

```jinja
# Renderizado por Ansible. No editar aqui: los cambios se pierden en el
# proximo despliegue. La plantilla esta en ansible/roles/orchestrator/templates/.
services:
    orchestrator:
        image: {{ orchestrator_image }}
        container_name: {{ orchestrator_container_name }}
        restart: unless-stopped
        # Red de host: los pares del raft se hablan entre maquinas por su IP
        # real, igual que el bus de Valkey.
        network_mode: host
        volumes:
            - {{ orchestrator_config_dir }}/orchestrator.conf.json:/etc/orchestrator.conf.json:ro
            - {{ orchestrator_config_dir }}/tls:/etc/orchestrator/tls:ro
            - orchestrator-data:/var/lib/orchestrator
volumes:
    orchestrator-data:
```

`ansible/roles/orchestrator/handlers/main.yml`:

```yaml
# Mismo motivo que en los demas roles: compose no considera cambio el contenido
# de un fichero montado por bind mount.
- name: Reiniciar orchestrator
  community.docker.docker_compose_v2:
    project_src: "{{ orchestrator_config_dir }}"
    state: present
    recreate: always
```

- [ ] **Paso 5: Las tareas**

Crear `ansible/roles/orchestrator/tasks/main.yml`:

```yaml
- name: El raft necesita un numero impar de votantes, y tres es el numero
  # Con dos nodos de datos, la decision de promover no puede tomarse ahi: un
  # nodo solo no distingue "el otro se ha muerto" de "me he quedado aislado".
  # Por eso el quorum vive aqui, y por eso el tercer voto tiene que estar en una
  # maquina que NO sea de base de datos. Con dos votantes, perder uno deja al
  # otro sin quorum y no hay promocion: exactamente lo que se queria evitar.
  ansible.builtin.assert:
    that:
      - (groups['orchestrator'] | default([])) | length >= 3
      - ((groups['orchestrator'] | default([])) | length) % 2 == 1
    fail_msg: >-
      El grupo orchestrator tiene {{ (groups['orchestrator'] | default([])) | length }}
      hosts: hacen falta al menos 3 y en numero impar. El tercero va en una
      maquina que no sea de base de datos --un balanceador o un nodo de dex--,
      igual que el tercer sentinel de la fase 2.
    success_msg: El raft de orchestrator tiene un numero impar de votantes.
  run_once: true

- name: La contrasena de orchestrator no puede quedar vacia
  ansible.builtin.assert:
    that:
      - mariadb_orchestrator_password | default('') | length > 0
    fail_msg: >-
      mariadb_orchestrator_password esta vacia. Es el usuario con el que
      orchestrator habla a los dos MariaDB, y lleva SUPER: va cifrada con
      ansible-vault en group_vars.
    success_msg: mariadb_orchestrator_password esta presente.

- name: Directorio de configuracion
  ansible.builtin.file:
    path: "{{ orchestrator_config_dir }}"
    state: directory
    mode: "0750"

- name: Directorio de TLS
  ansible.builtin.file:
    path: "{{ orchestrator_config_dir }}/tls"
    state: directory
    mode: "0750"

- name: Certificado de la CA interna, para verificar a los MariaDB
  ansible.builtin.copy:
    content: "{{ internal_ca_cert }}"
    dest: "{{ orchestrator_config_dir }}/tls/ca.crt"
    mode: "0644"
  notify: Reiniciar orchestrator

- name: Configuracion
  # no_log: lleva MySQLTopologyPassword en claro. El fichero va en 0600 por lo
  # mismo.
  ansible.builtin.template:
    src: orchestrator.conf.json.j2
    dest: "{{ orchestrator_config_dir }}/orchestrator.conf.json"
    mode: "0600"
  notify: Reiniciar orchestrator
  no_log: true

- name: Compose
  ansible.builtin.template:
    src: docker-compose.yml.j2
    dest: "{{ orchestrator_config_dir }}/docker-compose.yml"
    mode: "0644"
  notify: Reiniciar orchestrator

- name: Levantar orchestrator
  community.docker.docker_compose_v2:
    project_src: "{{ orchestrator_config_dir }}"
    state: present
```

- [ ] **Paso 6: Comprobar que arranca y descubre la topología**

Con el par de la tarea 3 levantado y tres orchestrator locales (puertos distintos):

```bash
curl -s http://127.0.0.1:3000/api/health | head -c 400; echo
curl -s http://127.0.0.1:3000/api/clusters | head -c 400; echo
```

Tiene que responder, y `api/clusters` tiene que llegar a listar el par. Si `api/health`
responde pero `api/clusters` sale vacío, es que no ha descubierto nada: revisar los
privilegios del usuario `orchestrator` (`SUPER, PROCESS, REPLICATION SLAVE, REPLICATION
CLIENT, RELOAD`) antes de dar la tarea por hecha.

- [ ] **Paso 7: Sabotear el assert del quorum**

Dejar dos hosts en el grupo `orchestrator` y correr: tiene que fallar con el `fail_msg`
propio, **no** con un error interno de Jinja. Es exactamente el fallo que mordió en la
fase 3 con `map('extract', hostvars, 'clave')`, que **lanza** cuando la clave no está en
vez de devolver Undefined; la forma que funciona es `selectattr('clave', 'defined')`.

- [ ] **Paso 8: Lint y commit**

```bash
pre-commit run --files ansible/roles/orchestrator/defaults/main.yml \
    ansible/roles/orchestrator/tasks/main.yml \
    ansible/roles/orchestrator/handlers/main.yml \
    ansible/roles/orchestrator/templates/orchestrator.conf.json.j2 \
    ansible/roles/orchestrator/templates/docker-compose.yml.j2
git add ansible/roles/orchestrator
git commit -m "feat(ansible): rol orchestrator en raft a tres, sin tocar el balanceador"
```

El cuerpo dice que se fija por digest porque no publica etiquetas, que su última versión
es de 2022, y por qué no hay `PostFailoverProcesses`.

---

## Tarea 6: El inventario y el playbook

Es la tarea que hace que el despliegue de ejemplo cierre el círculo: hoy
`dex_storage.host` apunta a `mariadb.interno`, una máquina que **ningún grupo declara y
ningún rol crea**.

**Ficheros:**

- Modificar: `ansible/inventories/ejemplo/hosts.yml`
- Modificar: `ansible/inventories/ejemplo/group_vars/all.yml`
- Modificar: `ansible/playbooks/dex.yml`
- Modificar: `ansible/roles/dex/defaults/main.yml` (solo el comentario)

- [ ] **Paso 1: Los grupos nuevos**

Añadir a `ansible/inventories/ejemplo/hosts.yml`, dentro de `children:`:

```yaml
    mariadb:
      hosts:
        db-1:
          ansible_host: 192.0.2.41
          # Identidad de replicacion, explicita y estable. NO se deriva del
          # indice del grupo a proposito: reordenar el inventario reasignaria
          # identidades, y dos nodos con el mismo server_id rompen la
          # replicacion sin decirlo.
          mariadb_server_id: 1
        db-2:
          ansible_host: 192.0.2.42
          mariadb_server_id: 2
      vars:
        # Quien arranca de primario, SOLO la primera vez. A partir de ahi manda
        # orchestrator y el rol lee el papel en vez de asignarlo.
        #
        # El certificado de cada nodo necesita el nombre del balanceador entre
        # sus SAN: dex se conecta marcando ese nombre --db.interno resuelve a la
        # VIP-- y la sesion TLS se negocia contra el nodo que toque. Sin esto,
        # Go rechaza el certificado por nombre y dex no arranca.
        internal_ca_extra_sans:
          - "DNS:db.interno"
          - "IP:192.0.2.30"
        # Atar el puerto a la interfaz privada, igual que dex_http_bind: el
        # contenedor va en red de host, asi que quien limita la exposicion es
        # esta directiva.
        mariadb_bind_address: "{{ ansible_host }}"

    orchestrator:
      # Tres votantes, y el tercero NO es una maquina de base de datos: con dos
      # nodos de datos, un orchestrator en cada uno deja al superviviente sin
      # quorum justo el dia que hace falta promover. Es el mismo patron que el
      # tercer sentinel de la fase 2, que vive en un nodo de dex.
      hosts:
        db-1: {}
        db-2: {}
        lb-1: {}
```

Y cambiar `dex_storage` en las vars del grupo `dex`:

```yaml
        dex_storage:
          type: mysql
          # El BALANCEADOR, no una maquina. HAProxy manda al unico nodo que la
          # sonda declara primario escribible, asi que tras un failover dex
          # sigue apuntando al mismo sitio. Apuntarlo a db-1 seria dejar el
          # punto unico de fallo que esta fase existe para quitar.
          host: db.interno
          port: 3306
          database: dex
          user: dex
          # "true" y no "false": desde la fase 4 la base de datos es NUESTRA y
          # la sirve el rol mariadb con el certificado de la CA interna, la
          # misma que este nodo ya lleva. El aviso del rol dex se queda para
          # quien lo apunte a una base ajena.
          ssl_mode: "true"
          ssl_ca_file: /etc/dex/internal-ca.pem
```

> `db.interno` tiene que resolver a la VIP de keepalived (192.0.2.30), igual que
> `sso.interno` y `panel.interno`. Las tres son la misma dirección; el balanceador reparte
> por puerto en el caso de MySQL y por `Host` en el de HTTP.

- [ ] **Paso 2: Las contraseñas nuevas**

Añadir a `ansible/inventories/ejemplo/group_vars/all.yml` (el fichero que va cifrado
entero con `ansible-vault`):

```yaml
# MariaDB. dex_storage_password (arriba) sigue siendo la del usuario "dex": no
# se duplica. Estas cuatro son las de los usuarios de servicio.
#
# Ojo con el '$' igual que en las de arriba: el feature flag expand_env de dex
# aplica os.ExpandEnv a su configuracion.
mariadb_root_password: cambiame-sin-simbolo-dolar
mariadb_repl_password: cambiame-tambien
# La de la sonda es la unica que vive tambien en un fichero legible por el
# contenedor del agente. Su usuario no tiene ningun privilegio (solo USAGE), asi
# que filtrarla no da acceso a nada -- pero se cambia igual.
mariadb_probe_password: cambiame-igual
# Esta si es sensible: el usuario de orchestrator lleva SUPER.
mariadb_orchestrator_password: cambiame-y-en-serio
```

- [ ] **Paso 3: Las tandas del playbook**

En `ansible/playbooks/dex.yml`: añadir `mariadb` y `orchestrator` a la tanda de la CA
interna, e insertar las dos tandas nuevas **antes** de la de Valkey.

```yaml
- name: CA interna
  hosts: dex:dex_dashboard:valkey:loadbalancer:mariadb:orchestrator
  roles: [internal_ca]
```

```yaml
- name: MariaDB
  # Antes que dex: dex no arranca sin almacenamiento. Y antes que orchestrator,
  # que no tiene nada que descubrir hasta que el par existe.
  #
  # serial: 1 igual que las demas, y aqui importa mas que en ninguna: dos nodos
  # convergiendo a la vez pueden intentar establecer la replicacion cada uno por
  # su lado.
  hosts: mariadb
  serial: 1
  become: true
  roles: [mariadb]

- name: orchestrator
  # Despues de MariaDB. El grupo incluye un host que NO es de base de datos --el
  # tercer voto del raft-- y por eso es una tanda propia y no un rol mas de la
  # anterior.
  hosts: orchestrator
  become: true
  roles: [orchestrator]
```

Orden final, que es el que la spec fija:

```
CA interna → MariaDB → orchestrator → Valkey → dex → panel → balanceador
```

- [ ] **Paso 4: El comentario del rol `dex`**

En `ansible/roles/dex/defaults/main.yml`, junto a lo que documenta `dex_storage`: el aviso
de `ssl_mode` se queda (sigue siendo posible apuntar el rol a una base de datos ajena),
pero añadir que desde la fase 4 el inventario de ejemplo la despliega y usa `"true"`. **No
se cambia ningún default**: lo que cambia lo declara el inventario.

- [ ] **Paso 5: Comprobar los dos inventarios**

```bash
.venv/bin/ansible-inventory -i ansible/inventories/ejemplo/hosts.yml --graph \
    > /tmp/t14-inv.log 2>&1 < /dev/null; cat /tmp/t14-inv.log
.venv/bin/ansible-playbook -i ansible/inventories/ejemplo/hosts.yml \
    ansible/playbooks/dex.yml --syntax-check \
    > /tmp/t14-syn.log 2>&1 < /dev/null; cat /tmp/t14-syn.log
```

El grafo tiene que enseñar `mariadb` con dos hosts y `orchestrator` con tres, uno de ellos
`lb-1`.

- [ ] **Paso 6: Comprobar que un inventario sin `mariadb` sigue funcionando**

Es la restricción global de no cambiar el comportamiento de lo ya entregado. Correr el
`--syntax-check` y renderizar `haproxy.cfg` contra un inventario **sin** grupo `mariadb`
(el local, si no se le añadió): la configuración de HAProxy tiene que salir sin
`fe_mysql`, y válida.

- [ ] **Paso 7: Lint y commit**

```bash
pre-commit run --files ansible/inventories/ejemplo/hosts.yml \
    ansible/inventories/ejemplo/group_vars/all.yml ansible/playbooks/dex.yml \
    ansible/roles/dex/defaults/main.yml
git add ansible/inventories ansible/playbooks ansible/roles/dex
git commit -m "feat(ansible): grupos de mariadb y orchestrator, y dex apuntando al balanceador"
```

El cuerpo cuenta que hasta ahora el inventario de ejemplo apuntaba a una máquina que nadie
creaba, y por qué el tercer voto del raft vive en un balanceador.

---

## Tarea 7: Las pruebas en vivo

Los tests unitarios no bastan y el render tampoco: los tres fallos que más han costado en
estas fases solo aparecieron probando de verdad. Siete pruebas, que son las de §9.1 de la
spec.

**Ficheros:** ninguno de producción. Las transcripciones van a la documentación (tarea 8).

- [ ] **Paso 1: Levantar el par entero en local**

Con el inventario local extendido (par de MariaDB, tres orchestrator, un HAProxy y un dex
apuntando al balanceador). Todos los contenedores con prefijo `t14-`.

- [ ] **Paso 2: Prueba 1 — `AFTER_SYNC` está puesto de verdad**

```bash
docker exec t14-mariadb mariadb --defaults-file=/run/secrets/root.cnf -N -B \
    -e "SELECT @@rpl_semi_sync_master_wait_point"
```

Esperado: `AFTER_SYNC`.

- [ ] **Paso 3: Prueba 2 — la sonda dice lo que tiene que decir**

Los tres casos de la tarea 2, ahora sobre el par: 200 en el primario, 503 en la réplica,
503 con MariaDB parada.

- [ ] **Paso 4: Prueba 3 — la contraseña de la sonda no está en el `argv`**

`docker top t14-mariadb-probe` mientras la sonda consulta en bucle. Ya saboteada en la
tarea 2; aquí se repite sobre el despliegue completo.

- [ ] **Paso 5: Prueba 4 — HAProxy manda solo al primario**

```bash
# Escribir POR EL BALANCEADOR
mariadb -h 127.0.0.1 -P <puerto del balanceador> -u dex -p<...> \
    -e "CREATE TABLE dex.porelbalanceador(i INT); INSERT INTO dex.porelbalanceador VALUES (1);"
# Y el estado de los backends: uno UP, uno DOWN, y eso es lo correcto
echo "show stat" | docker exec -i t14-haproxy socat stdio /var/run/haproxy.sock \
    | grep be_mysql
```

Si el socket de estadísticas no está configurado, sirve el log de HAProxy: al arrancar
dice qué servidor pone `DOWN` y por qué.

- [ ] **Paso 6: Prueba 5 — un failover manual mueve el tráfico sin tocar HAProxy**

Prueba el mecanismo entero sin necesitar orchestrator, que es lo que la hace valiosa: si
esto funciona, el enrutado no depende de que orchestrator acierte.

```bash
# Degradar el primario y promover la replica a mano
docker exec t14-mariadb mariadb --defaults-file=/run/secrets/root.cnf \
    -e "SET GLOBAL read_only = ON"
docker exec t14-mariadb-2 mariadb --defaults-file=/run/secrets/root.cnf \
    -e "STOP REPLICA; SET GLOBAL read_only = OFF"
sleep 10   # el intervalo de sonda de HAProxy
# Y escribir otra vez por el balanceador: tiene que funcionar
mariadb -h 127.0.0.1 -P <puerto del balanceador> -u dex -p<...> \
    -e "INSERT INTO dex.porelbalanceador VALUES (2);"
```

- [ ] **Paso 7: Prueba 6 — dex sobrevive al cambio**

La que puede destapar trabajo nuevo. El pool de dex tiene conexiones abiertas contra el
primario viejo (`MaxOpenConns` 5 por réplica).

Sin reiniciar dex, tras el failover del paso anterior: hacer un login completo contra dex
por el balanceador y comprobar que sirve.

**Si no vuelve solo, eso es una entrada de TODO que sale de esta fase** —con lo que se
midió: cuántas peticiones fallan, cuánto tarda en recuperarse, si se recupera— **no un
detalle a esconder.**

- [ ] **Paso 8: Prueba 7 — la suite de `storage/sql` contra el par**

```bash
DEX_MYSQL_HOST=127.0.0.1 DEX_MYSQL_PORT=<puerto del balanceador> \
DEX_MYSQL_DATABASE=dex DEX_MYSQL_USER=dex DEX_MYSQL_PASSWORD=<...> \
    go test ./storage/sql/... -v 2>&1 | tail -40
```

Esperado: pasa. Es la comprobación de que el dialecto MySQL de dex funciona contra este
par y no solo contra un MariaDB suelto.

- [ ] **Paso 9: Limpiar**

```bash
docker ps -a --format '{{.Names}}' | grep '^t14-' | xargs -r docker rm -f
docker volume ls --format '{{.Name}}' | grep -i 't14\|converge' | xargs -r docker volume rm
docker ps --format '{{.Names}}'
```

Lo último tiene que enseñar solo los contenedores que ya estaban: `dex`, `dex-replica`,
`dex-dashboard`, `valkey`, `omniroute`, `omniroute-valkey`.

- [ ] **Paso 10: Commit de lo que las pruebas hayan cambiado**

Si alguna prueba destapó un fallo, el arreglo va en su propio commit con el porqué. Si no
cambió nada, no hay commit: las transcripciones van en la tarea 8.

---

## Tarea 8: Documentación

**Ficheros:**

- Modificar: `documentacion/despliegue-ansible.md`
- Modificar: `documentacion/specs/2026-09-03-despliegue-hoja-de-ruta.md`
- Modificar: `CHANGELOG.md`, `TODO.md`, `DONE.md`

- [ ] **Paso 1: La sección de MariaDB en `despliegue-ansible.md`**

Deja de decir «la base de datos es de otro». Tiene que llevar, todo esto:

1. **Una réplica no es una copia de seguridad**, con todas las letras y arriba del todo.
   Replica el `DROP TABLE` y el `UPDATE` sin `WHERE` al instante. Esta fase protege de que
   se muera una máquina, **no** de que alguien se equivoque. Las copias son trabajo propio,
   con su retención y sus restauraciones probadas.

2. **Cómo saber si la semisíncrona está activa de verdad**, con la corrección medida:

   ```sql
   SHOW STATUS WHERE Variable_name IN (
       'Rpl_semi_sync_master_status',    -- ON no basta
       'Rpl_semi_sync_master_clients',   -- tiene que ser >= 1
       'Rpl_semi_sync_master_no_tx');    -- transacciones SIN la garantia
   ```

   `status: ON` con `clients: 0` dice «habilitada», no «funcionando»: la siguiente
   escritura bloqueará 10 s y caerá a asíncrono. El número para una alarma es
   `Rpl_semi_sync_master_no_tx`, que cuenta las transacciones que se confirmaron sin la
   protección que se creía tener.

3. **Que la garantía se apaga sola y en silencio** al agotarse
   `rpl_semi_sync_master_timeout`, y que eso es lo correcto: una réplica muerta no puede
   bloquear los logins.

4. **Que la réplica aparece `DOWN` en HAProxy a propósito**, y no es una avería. Es lo que
   la sonda tiene que decir.

5. **Que el primer arranque tarda unos 10 s de más** y deja un
   `Timeout waiting for reply of binlog` en el log. Esperado, no roto.

6. **Cómo obtener el digest de orchestrator** (`docker manifest inspect`) y que su última
   versión es de 2022.

7. **Que el rol no clona la réplica** si el primario ya tiene datos, con los dos caminos
   (`mariadb-dump --master-data --gtid` o `mariabackup`).

- [ ] **Paso 2: Las dos entradas de la lista de verificación manual**

A la lista ejecutable donde ya están el failover de sentinel y los pasos de VRRP, con la
misma forma: órdenes copiables, `<host-N>` sustituible, y qué se espera ver.

- **La promoción automática entre máquinas**: matar el primario de verdad y comprobar que
  orchestrator promueve, que `ApplyMySQLPromotionAfterMasterFailover` quita el `read_only`
  y que HAProxy cambia de backend por la sonda. Tres instancias de raft en un portátil
  comparten demasiado para que el resultado signifique algo.
- **La pérdida de escrituras con `AFTER_COMMIT` frente a `AFTER_SYNC`**: exige matar el
  primario en una ventana de milisegundos, entre máquinas. Se documenta el razonamiento y
  el valor fijado, **no** una prueba de laboratorio que no prueba el caso real.

- [ ] **Paso 3: La hoja de ruta**

Fase 4 a **«Entregada»**. Y **arreglar de paso lo que quedó desincronizado**, porque un
documento que se contradice con otro del mismo repo es peor que uno incompleto:

- La sección «Qué hay que verificar y nadie ha verificado» lista «MariaDB con este fork»,
  pero `despliegue-ansible.md` ya dice verificado contra 10.11 y 11.4. Se quita de ahí.
- `despliegue-ansible.md` dice «Las fases 3, 4 y 5 … sin spec propia». La 3 está entregada
  y la 4 también al cerrar esto.

- [ ] **Paso 4: `TODO.md`**

- La entrada de las fases 4 y 5 pierde la 4 y **se corrige la 5**: sigue diciendo
  «ProxySQL como punto de entrada», que es justo la decisión que la hoja de ruta ya
  revocó.
- Entra lo que las pruebas hayan destapado (la prueba 6, dex y su pool, es la candidata).
- «Última actualización» al día.

- [ ] **Paso 5: `DONE.md`**

Sección 27 (la 26 es el panel), conservando la numeración histórica, con **lo que destapó
probarlo**: el `gtid_slave_pos` y el error exacto que produce sin él, y que
`Rpl_semi_sync_master_status` por sí solo miente. Esa es la parte que vale.

- [ ] **Paso 6: `CHANGELOG.md`**

En `[Unreleased]`: los dos roles nuevos, el frontend de MySQL, el cambio de
`dex_storage.host` al balanceador y el de `ssl_mode` a `"true"`, cada uno con su motivo.

- [ ] **Paso 7: `graphify update .`**

Solo si alguna prueba obligó a tocar código Go. Si la fase se quedó en Ansible y
documentación, no hace falta.

- [ ] **Paso 8: Lint y commit**

```bash
pre-commit run --files documentacion/despliegue-ansible.md \
    documentacion/specs/2026-09-03-despliegue-hoja-de-ruta.md \
    CHANGELOG.md TODO.md DONE.md
git add documentacion CHANGELOG.md TODO.md DONE.md
git commit -m "docs: cerrar la fase 4, mariadb en alta disponibilidad"
```

---

## Autorrevisión

**1. Cobertura de la spec.** Cada sección tiene tarea:

| Spec | Tarea |
| --- | --- |
| §3.1 HAProxy y no ProxySQL | 4 (el comentario de `mode tcp` lo justifica en el sitio) |
| §3.2 `AFTER_SYNC` | 1 (plantilla), 7 (prueba 1), 8 (documentación) |
| §3.3 Todo al primario | 4 (`balance first`, sin backend de lecturas) |
| §3.4 Dos datos + quorum en la decisión | 5 (assert del número impar), 6 (grupo con `lb-1`) |
| §3.5 La sonda es la fuente de verdad | 2, y 7 (prueba 5, que la prueba sin orchestrator) |
| §3.6 orchestrator por digest | 5 |
| §3.7 TLS de la base de datos | 1 (`ssl_*` en la config), 6 (`ssl_mode: "true"`) |
| §4.1–4.3 forma, propiedad, config | 1 |
| §4.4 bootstrap una vez | 3 |
| §5 el agente | 2 |
| §6 cambios en `haproxy` | 4 |
| §7 el rol `orchestrator` | 5 |
| §8 cambios en lo entregado | 6 |
| §9.1 las siete pruebas | 7 |
| §9.2 lo que no se puede probar | 8 (paso 2) |
| §10 documentación | 8 |

Sin huecos.

**2. Marcadores.** Sin «TBD», sin «gestionar los errores», sin «similar a la tarea N». El
único sitio donde el plan no da el valor final es el paso 1 de la tarea 5, y es
deliberado: los nombres de las claves de orchestrator se **confirman contra el fichero de
ejemplo de la imagen** antes de escribirlos, porque inventarlas no da error —orchestrator
ignora lo que no entiende— y el fallo aparecería como un failover que no ocurre.

**3. Consistencia de nombres.** `mariadb_probe_port` es del rol `mariadb` y el rol
`haproxy` lo lee sin declararlo (tareas 1 y 4). `mariadb_orchestrator_user` y
`mariadb_orchestrator_password` se crean en la tarea 3 y se consumen en la 5.
`internal_ca_cert` / `internal_ca_host_cert` / `internal_ca_host_key` son los nombres que
publica el rol `internal_ca`, verificados contra su `tasks/main.yml`. `haproxy_mysql_port`
solo lo define el rol `haproxy`.

**4. Lo que este plan añade sobre la spec, y que no se pierda:** el `gtid_slave_pos` de la
tarea 3 —sin él la fase no funciona, y no se puede deducir leyendo documentación—, la
corrección de `Rpl_semi_sync_master_status`, la espera de 90 s del primer arranque, y los
sufijos `_FILE` como la forma de cumplir la restricción de secretos sin inventar nada.
