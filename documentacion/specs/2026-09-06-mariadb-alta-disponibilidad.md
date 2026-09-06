# Fase 4 — MariaDB en alta disponibilidad

> Fecha: 2026-09-06.
> Fase 4 de la
> [hoja de ruta del despliegue](2026-09-03-despliegue-hoja-de-ruta.md).
> Las fases 1 y 2 en
> [2026-09-03-despliegue-ansible-valkey-ha.md](2026-09-03-despliegue-ansible-valkey-ha.md),
> la 3 en
> [2026-09-04-balanceador-haproxy-keepalived.md](2026-09-04-balanceador-haproxy-keepalived.md).
> Las tres entregadas.

## 1. Qué se construye y por qué

Es la fase que quita el punto único de fallo que queda. Hoy el despliegue tiene dos dex,
dos paneles, Valkey en sentinel o cluster y dos balanceadores con una IP flotante — y
**una sola base de datos**, que es donde viven las claves de firma, los refresh tokens y
las sesiones. Perderla no degrada el servicio: lo apaga.

Se entrega: MariaDB desplegada por el rol, primario y réplica con replicación
semisíncrona, HAProxy como entrada con una sonda que sabe cuál es el primario, y
orchestrator para detectar la caída y promover.

## 2. Alcance

**Dentro:**

- Rol `mariadb`: los dos nodos en Docker, con su TLS y su replicación.
- El agente de sonda que dice si un nodo es primario escribible.
- Un frontend y un backend nuevos en el rol `haproxy`, para el 3306.
- Rol `orchestrator`, en modo raft a tres.
- Grupos `mariadb` y `orchestrator` en el inventario, y una tanda nueva.
- El rol `dex` deja de apuntar a una máquina y apunta al balanceador.

**Fuera, y por qué:**

- **Copias de seguridad.** Y hay que decirlo con todas las letras porque es la confusión
  más cara de este terreno: **una réplica no es una copia de seguridad**. Replica al
  instante el `DROP TABLE` y el `UPDATE` sin `WHERE`. Esta fase protege de que se muera
  una máquina, no de que alguien se equivoque. Las copias son trabajo propio, con su
  retención y sus restauraciones probadas, y no caben aquí.
- **Más de dos nodos de datos.** Dos y el quorum en la capa de decisión (§3.4).
- **Reparto de lecturas.** §3.3.
- **El camino `ent`.** Sigue sin soportarse con MariaDB: `atlas` hace sondeo de versión y
  MariaDB devuelve `5.5.5-10.11.2-MariaDB`, que no interpreta como MySQL. Ya estaba
  documentado y no cambia.

## 3. Decisiones y su porqué

### 3.1 HAProxy y no ProxySQL, y no es reutilizar por comodidad

La hoja de ruta decía ProxySQL. **Se corrige**, y el razonamiento entero está en su
propia sección; aquí lo que importa para construir:

Dex mete `transaction_isolation: 'SERIALIZABLE'` en los parámetros del DSN
([storage/sql/config.go:236](../../storage/sql/config.go#L236)), así que viaja como un
`SET` en cada conexión. Un proxy que **multiplexa** conexiones de cliente sobre conexiones
de backend tiene que rastrear esa variable, o dex acaba corriendo en el `REPEATABLE-READ`
por defecto de MariaDB sin decir una palabra: la misma pérdida silenciosa de corrección
por la que se descartó Galera, entrando por otra puerta.

**HAProxy en `mode tcp` no multiplexa**: es un tubo de bytes y la sesión es de punta a
punta con el backend. No hay nada que rastrear y no hay nada que demostrar. Dex además ya
hace su propio pool (`MaxOpenConns` 5 por réplica), así que el pooling de un proxy no
resuelve ningún problema que tengamos.

Lo que ProxySQL sí daba —saber solo cuál es el primario, vigilando el `read_only`— lo
sustituye §3.5.

### 3.2 `rpl_semi_sync_master_wait_point = AFTER_SYNC`, y no el valor de fábrica

**Comprobado contra un `mariadb:11.4` de verdad**, no leído en la documentación: la
semisíncrona viene integrada (no hace falta instalar plugin) y **el valor por defecto de
`rpl_semi_sync_master_wait_point` en MariaDB es `AFTER_COMMIT`**. MySQL trae `AFTER_SYNC`;
MariaDB no.

Con `AFTER_COMMIT` el primario **confirma y después** espera el ack de la réplica. Si
muere en ese hueco, la transacción está confirmada localmente y en ninguna réplica: un
cliente pudo ver el commit, y la promoción se lo lleva por delante. Eso deshace justo el
motivo por el que se eligió semisíncrona sobre asíncrona — no perder los refresh tokens
recién emitidos.

El rol fija `AFTER_SYNC`. Está disponible y se puede poner en caliente, probado.

Los otros dos que el rol fija, y por qué:

- **`rpl_semi_sync_master_timeout`**: al agotarse, el primario **cae a asíncrono y sigue**.
  Es lo correcto —una réplica muerta no puede bloquear los logins— pero significa que la
  garantía se apaga sola y en silencio. El rol lo deja en el valor de fábrica (10 s) y la
  documentación dice qué mirar (`Rpl_semi_sync_master_status`) para saber si está activa
  de verdad.
- **`rpl_semi_sync_master_wait_no_slave = ON`** (el de fábrica): con una sola réplica, es
  lo que evita que un reinicio de esa réplica deje al primario confirmando a ciegas
  durante la ventana.

### 3.3 Todo va al primario: ni una lectura a la réplica

Con replicación por debajo, una lectura enviada a la réplica puede no ver un código de
autorización recién escrito, y eso son logins que fallan sin motivo aparente y sin dejar
rastro útil. Dex además no distingue: usa el mismo pool para todo, así que no hay forma de
marcar «esta consulta tolera retraso».

El backend de lecturas no existe. No es una optimización pendiente: es una decisión.

### 3.4 Dos nodos de datos, y el quorum en la capa de decisión

Dos MariaDB: primario y réplica. Con dos nodos no hay forma de que uno solo distinga «el
otro se ha muerto» de «me he quedado aislado», así que **el failover automático no puede
decidirse ahí**.

Se decide en orchestrator, que corre **en modo raft a tres**. El tercer voto vive en una
máquina que no es de base de datos —un balanceador, o un nodo de dex— exactamente como el
tercer sentinel vive en un nodo de dex desde la fase 2. Es el mismo patrón y por el mismo
motivo.

### 3.5 El agente de sonda, y por qué es él la fuente de verdad

HAProxy no habla SQL, así que cada nodo de MariaDB corre un agente que responde por HTTP:
**200 si es primario escribible, 503 si no**. HAProxy sonda con `option httpchk` y manda
al único backend que pasa. La réplica está `DOWN` a propósito y todo el rato: no es una
avería, es lo que la sonda tiene que decir.

Qué mira: `@@read_only` y `@@innodb_read_only`, los dos a 0. Un nodo que no responde a la
consulta —MariaDB caída, disco lleno, demasiadas conexiones— también da 503, que es lo
correcto: no sirve aunque técnicamente sea el primario.

**No hace falta escribir un binario ni construir una imagen.** La imagen `mariadb:11.4`
que el rol ya despliega trae `socat` y el cliente `mariadb`; el agente son un script y una
línea de compose. Comprobado.

La contraseña **no va en la línea de órdenes**: `docker top` deja el `argv` a la vista de
cualquiera en la máquina. Va por fichero de opciones montado en modo `0600`, que es la
misma lección que ya costó una corrección con `REDISCLI_AUTH` en el rol de Valkey.

**Y esta es la razón de que la sonda mande y no orchestrator:** si orchestrator se
equivoca, se queda sin quorum o se muere, HAProxy sigue mandando el tráfico al nodo que se
declara escribible. Un failover puede no ocurrir; lo que no puede ocurrir es que el
tráfico vaya a un nodo de solo lectura porque una herramienta externa creyó otra cosa.

### 3.6 orchestrator se fija por digest, y es software en mantenimiento

Dos cosas que hay que decir en vez de callar:

- **No publica etiquetas de versión en Docker Hub**, solo `latest`. La regla del
  repositorio es no usar `latest` nunca, y lo que esa regla protege es la inmutabilidad,
  así que se fija **por digest**: `openarkcode/orchestrator@sha256:789b9e7c…`. El
  inventario lo declara y la documentación explica cómo obtener uno nuevo.
- **Su última versión es de 2022.** No está muerto pero no está vivo. Es una razón más
  para §3.5: el componente que decide promociones puede quedarse atrás sin arrastrar al
  que decide a dónde va el tráfico.

Si algún día deja de ser sostenible, se sustituye la pieza que promueve sin tocar la
entrada. Esa separación es la que hace el cambio barato.

### 3.7 El TLS de la base de datos ya tiene su sitio

El rol `dex` lleva desde la fase 2 `dex_storage.ssl_mode` y `ssl_ca_file`, y avisa —no se
niega— cuando se deja en `"false"`, porque la base de datos no era nuestra. **Ahora sí lo
es**, así que el criterio cambia al de los componentes propios: el rol `mariadb` sirve TLS
con el certificado que le emite la CA interna, y el inventario de ejemplo pasa a
`ssl_mode: "true"` con la CA interna, que es lo que ya trae.

El aviso del rol `dex` se queda: sigue siendo posible apuntarlo a una base de datos ajena.

## 4. El rol `mariadb`

### 4.1 Forma

Como los demás: configuración renderizada en `{{ mariadb_config_dir }}`, compose al lado,
`community.docker.docker_compose_v2` levantando, handler con `recreate: always`. Imagen
`mariadb:11.4` fijada por variable.

Los datos van en un volumen con nombre, no en un bind mount del árbol: un `docker compose
down -v` mal dado no puede llevárselos por delante desde el rol.

### 4.2 La regla de propiedad de la configuración

La misma que en Valkey, y por la misma razón. `50-dex.cnf` es de Ansible y se reescribe en
cada pasada. Lo que MariaDB escriba por su cuenta —`auto.cnf`, los ficheros de estado de
la replicación— no se toca nunca.

### 4.3 Lo que fija la configuración

- `server_id`, distinto por nodo y **explícito en el inventario**: derivarlo del índice del
  grupo hace que reordenar el inventario reasigne identidades de replicación.
- `log_bin`, `binlog_format=ROW`, `gtid_strict_mode=ON`. GTID porque es lo que permite a
  orchestrator repuntar una réplica sin coordenadas de fichero y posición.
- `rpl_semi_sync_master_enabled=ON`, `rpl_semi_sync_slave_enabled=ON` en los dos nodos
  —los dos papeles, porque cualquiera puede acabar siendo cualquier cosa— y
  `rpl_semi_sync_master_wait_point=AFTER_SYNC` (§3.2).
- `read_only=ON` **en el arranque de los dos**. El primario lo quita el bootstrap; una
  réplica que reinicia vuelve a solo lectura sola, que es lo que se quiere. Sin esto, un
  nodo que arranca tras un failover puede aceptar escrituras y bifurcar el historial.
- `transaction_isolation` **no se toca**: lo pone dex por conexión y ahí es donde tiene que
  estar.

### 4.4 El bootstrap, que se hace una vez y no se repite

El problema de siempre: la replicación se establece una vez, y volver a establecerla sobre
un par vivo lo rompe. Igual que `--cluster create` en Valkey.

- El primario es el primer host del grupo **solo en el arranque inicial**. A partir de ahí
  manda orchestrator, y el rol no vuelve a asignar el papel: lo lee.
- Se detecta si ya hay replicación con `SHOW REPLICA STATUS` antes de tocar nada.
- El usuario de replicación y el de orchestrator se crean idempotentes.
- Con datos ya en el primario, el rol **no** clona la réplica: se niega y dice cómo
  hacerlo (`mariadb-dump --master-data` o `mariabackup`). Clonar bases de datos en
  producción desde un playbook, sin ventana ni comprobación de tamaño, es un mecanismo que
  no se puede probar aquí y que borra datos si se equivoca.

## 5. El agente de sonda

Un servicio más en el compose del rol `mariadb`, con la misma imagen:

```
socat -T2 TCP-LISTEN:{{ mariadb_probe_port }},fork,reuseaddr SYSTEM:/probe.sh
```

Y `probe.sh`, rendido por el rol:

```sh
#!/bin/sh
# Devuelve 200 solo si este nodo es primario escribible. La replica da 503 todo
# el rato y a proposito: no es una averia, es lo que la sonda tiene que decir.
#
# Las credenciales van en /probe.cnf (modo 0600), NO en el argv: docker top
# deja la linea de ordenes a la vista de cualquiera en la maquina.
if mariadb --defaults-file=/probe.cnf -N -B -e \
     "SELECT @@read_only + @@innodb_read_only" 2>/dev/null | grep -qx 0
then
    printf 'HTTP/1.1 200 OK\r\nContent-Length: 8\r\nConnection: close\r\n\r\nprimario'
else
    printf 'HTTP/1.1 503 Service Unavailable\r\nContent-Length: 7\r\nConnection: close\r\n\r\nreplica'
fi
```

El usuario de la sonda tiene `USAGE` y nada más: solo necesita leer dos variables de
sesión. Un agente que se pueda alcanzar desde la red no debe poder hacer nada más.

## 6. Los cambios en el rol `haproxy`

Un frontend y un backend nuevos, en `mode tcp`:

```
frontend fe_mysql
    bind :{{ haproxy_mysql_port }}
    mode tcp
    option tcplog
    default_backend be_mysql

backend be_mysql
    mode tcp
    # Un solo nodo activo a la vez: el que la sonda declara primario. No es
    # balanceo, es enrutado. balance first lo deja explicito.
    balance first
    option httpchk
    http-check send meth GET uri /
    http-check expect status 200
{% for h in groups['mariadb'] %}
    server {{ h }} {{ hostvars[h].ansible_host }}:3306 check port {{ mariadb_probe_port }}
{% endfor %}
```

`check port` es la pieza: HAProxy comprueba la salud en el puerto del agente y manda el
tráfico al 3306. Sin eso, sondearía el propio 3306 y no sabría distinguir primario de
réplica.

`haproxy_mysql_port` es del rol `haproxy`. `mariadb_probe_port` **no**: es del rol
`mariadb`, y el rol `haproxy` lo lee porque los dos corren en la misma ejecución. Es el
mismo acoplamiento que ya existe entre `keepalived` y `haproxy`, y se resuelve igual — sin
valor por defecto en el rol que no es su dueño, para que correrlo suelto falle con
«undefined variable» en vez de sondear un puerto inventado que daría todos los backends
`DOWN`.

**El TLS no se termina aquí**, igual que el gRPC: MariaDB lo sirve con el certificado de
la CA interna y dex lo verifica contra ella. Y por lo mismo, el certificado de cada nodo
de MariaDB necesita el nombre del balanceador entre sus SAN — `internal_ca_extra_sans`, la
variable que la fase 3 ya añadió.

## 7. El rol `orchestrator`

Tres instancias en raft. Variables: los pares del raft, el usuario y contraseña con que
habla a MariaDB (`orchestrator_topology_user`), y el digest de la imagen.

Lo que la configuración fija y por qué:

- `RaftEnabled: true` con los tres nodos. Sin raft, tres orchestrator independientes
  pueden promover tres cosas distintas.
- `FailureDetectionPeriodBlockMinutes` y `RecoveryPeriodBlockSeconds` con valores
  explícitos: los de fábrica son razonables, pero un failover automático con ventanas que
  nadie eligió es un failover que nadie puede explicar después.
- `ApplyMySQLPromotionAfterMasterFailover: true`, que es lo que quita el `read_only` del
  promovido. Sin esto la promoción ocurre y la sonda sigue dando 503 en todos: el
  despliegue se queda sin escrituras con un failover «correcto».
- **Sin `PostFailoverProcesses` que toquen HAProxy.** La sonda ya lo resuelve, y acoplarlos
  sería devolver a orchestrator el poder que §3.5 le quita a propósito.

## 8. Los cambios en los roles ya entregados

- **`dex`**: `dex_storage.host` pasa a ser el balanceador, no una máquina. El default no
  cambia.
- **`internal_ca`**: nada nuevo; los nodos de MariaDB entran en la tanda de la CA y usan
  `internal_ca_extra_sans` como los de dex.
- **`playbooks/dex.yml`**: tanda de MariaDB **antes** que la de dex —dex no arranca sin
  storage— y la de orchestrator **después** de MariaDB. El balanceador sigue al final.

Orden final:

```
CA interna → MariaDB → orchestrator → Valkey → dex → panel → balanceador
```

## 9. Pruebas

### 9.1 Lo que se puede probar aquí

1. **`AFTER_SYNC` está puesto de verdad.** `SELECT @@rpl_semi_sync_master_wait_point`
   contra el nodo desplegado. Es una línea, y es la que evita reintroducir el fallo de
   §3.2 en el siguiente refactor.
2. **La sonda dice lo que tiene que decir.** 200 en el primario, 503 en la réplica, 503
   con MariaDB parada. Los tres, y el tercero es el que suele faltar.
3. **La contraseña de la sonda no está en el `argv`.** `docker top` sobre el contenedor del
   agente. Se sabotea pasándola con `-p` y comprobando que entonces sí aparece.
4. **HAProxy manda solo al primario.** Conectarse por el balanceador y escribir; comprobar
   que la réplica figura `DOWN` y que eso es lo correcto.
5. **Un failover manual mueve el tráfico sin tocar HAProxy.** Promover la réplica a mano
   (`STOP REPLICA; SET GLOBAL read_only=OFF`) y comprobar que HAProxy cambia de backend él
   solo, por la sonda. Prueba el mecanismo entero sin necesitar orchestrator.
6. **Dex sobrevive al cambio.** Su pool tiene conexiones abiertas contra el primario viejo.
   Comprobar que tras el failover vuelve a servir sin reiniciarlo — y si no, esa es la
   entrada de TODO que sale de esta fase, no un detalle a esconder.
7. **La suite de `storage/sql` contra el par**, con `DEX_MYSQL_HOST` apuntando al
   balanceador.

### 9.2 Lo que no se puede probar aquí, y se dice

- **El raft de orchestrator y una promoción automática entre máquinas.** Tres instancias en
  un portátil comparten demasiado para que el resultado signifique algo.
- **La pérdida de escrituras con `AFTER_COMMIT` frente a `AFTER_SYNC`.** Demostrarla exige
  matar el primario en una ventana de milisegundos, entre máquinas. Se documenta el
  razonamiento y el valor fijado, no una prueba de laboratorio que no prueba el caso real.

Los dos van a la lista ejecutable de verificación manual de
[despliegue-ansible.md](../despliegue-ansible.md), donde ya están el failover de sentinel
y los pasos de VRRP.

## 10. Documentación a escribir

- **`despliegue-ansible.md`**: la sección de MariaDB deja de decir «la base de datos es de
  otro». Se añaden: que **una réplica no es una copia de seguridad**, cómo comprobar que la
  semisíncrona está activa de verdad (`Rpl_semi_sync_master_status`) y que puede apagarse
  sola al agotarse el timeout, cómo obtener el digest de orchestrator, y que la réplica
  aparece `DOWN` en HAProxy a propósito.
- **La lista de verificación manual**: la promoción automática entre máquinas.
- **La hoja de ruta**: fase 4 a «Entregada».
- **`CHANGELOG.md`**, **`TODO.md`** y **`DONE.md`** al cerrar, con lo que destapó probarlo.
