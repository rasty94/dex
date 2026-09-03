# Despliegue con Ansible y Valkey en alta disponibilidad

> Estado: aprobada, pendiente de plan de implementación.
> Fecha: 2026-09-03.
> Cierra las entradas «Rol de Ansible», «Un `docker compose` de producción» y
> «Valkey es hoy un punto único de fallo» de [TODO.md](../../TODO.md).

## 1. Qué se construye y por qué

Hoy no hay forma de poner este fork en una máquina de verdad sin repetir pasos a mano.
Lo que existe es un ejemplo para probar en local —que en su primera línea dice que no se
use tal cual— y una guía de TLS escrita a mano. Al mismo tiempo, el estado compartido
con Valkey acaba de entrar y **añade un punto único de fallo**: con `valkey.address`
configurado, dex se niega a arrancar si ese servidor no responde.

Este trabajo entrega dos cosas que se necesitan a la vez:

1. **Alta disponibilidad de Valkey** en el código, porque hoy `pkg/valkey` solo admite
   una dirección: ni sentinel, ni cluster.
2. **Un despliegue reproducible** con Ansible y Docker que sepa montar las tres
   topologías, dex, el panel y el TLS entre ellos.

El orden no es negociable: sin lo primero, el rol no tiene nada que configurar.

## 2. Alcance

**Entra:**

- `pkg/valkey` con soporte de standalone, sentinel y cluster.
- Tres roles de Ansible: `valkey`, `dex`, `dex_dashboard`, y un playbook que los ordena.
- La CA interna que emite los certificados de Valkey y del gRPC entre el panel y dex.
- Un inventario de ejemplo y la documentación de operación.
- Tests de integración para las topologías nuevas, y una verificación manual documentada
  del failover entre máquinas.

**No entra, y es deliberado:**

- **Desplegar la base de datos.** El inventario da un MariaDB o MySQL que ya existe; el
  rol comprueba que responde y falla claro si no. Desplegar y dar alta disponibilidad a
  un motor SQL es un producto entero y ya hay roles hechos que lo hacen mejor.
- **El balanceador de carga.** Queda documentado qué tiene que hacer —repartir entre los
  nodos de dex, comprobar `/healthz`— pero no lo gestiona el rol.
- **Una colección de Ansible publicable.** Los roles se empaquetan el día que haga falta
  instalarlos desde varios sitios; hacerlo ahora no cambia nada de lo que hay que
  escribir.
- **Alta disponibilidad del panel.** Es una consola de administración: una instancia.

## 3. Decisiones y su porqué

| Decisión | Alternativa descartada | Motivo |
| --- | --- | --- |
| Tres roles separados | Un rol con la topología como variable | El rol de `valkey` es el único con estado y con operaciones destructivas si se repiten. Aislarlo evita que conviva con las tareas triviales de dex, y permite saltárselo a quien ya tenga un Valkey |
| Compose renderizado por máquina | `docker_container` por servicio | El compose de producción que pedía el TODO **es** esa plantilla: un solo artefacto imposible de desincronizar, y queda en la máquina un fichero que un operador puede leer sin Ansible delante |
| MariaDB/MySQL existente | Postgres, o desplegar la base | Elección del proyecto. El rol lo consume y lo comprueba |
| CA propia solo para tráfico interno | Certificados aportados para todo, o Let's Encrypt | El certificado público de dex lo aporta el inventario, que tiene su propio ciclo de vida. Los de Valkey y del gRPC interno no los ve nadie de fuera y emitirlos ahorra pasos manuales sin mezclar ciclos |
| Secretos en ansible-vault, escritos a fichero 0600 | Variables de entorno en el compose | Cualquiera con acceso al demonio de Docker lee el entorno con `docker inspect` |
| `mode` explícito en la configuración de Valkey | Deducirlo del número de direcciones | Ver 4.2: la deducción falla en silencio |

## 4. El cambio en `pkg/valkey`

### 4.1 Lo que ya hace la librería

`valkey-go` cubre las dos topologías sin trabajo por nuestra parte, incluido seguir un
failover —se suscribe a `+switch-master`— y reencaminar los `MOVED`/`ASK` del cluster:

- **Sentinel**: `InitAddress` con las direcciones de los sentinels y
  `Sentinel: SentinelOption{MasterSet: "..."}`.
- **Cluster**: `InitAddress` con varias direcciones; se detecta solo. Se le añade
  `ShuffleInit: true` para no golpear siempre al mismo nodo al arrancar.

### 4.2 La forma de la configuración

```yaml
valkey:
  mode: standalone          # standalone | sentinel | cluster. Por defecto standalone
  addresses: ["valkey-1:6379"]
  masterSet: dex            # solo sentinel. En sentinel, addresses son los sentinels
  sentinelUsername: ""      # opcional; por defecto, username
  sentinelPassword: ""      # opcional; por defecto, password
  username: ""
  password: ""
  db: 0
  keyPrefix: "dex:"
  tls: {caCert: "", insecureSkipVerify: false}
```

`Config.Address` (cadena) desaparece y pasa a `Addresses` (lista). `valkey.address` está
en `[Unreleased]` del CHANGELOG y no lo usa nadie fuera de este repositorio, así que se
cambia en limpio en vez de arrastrar un alias; la entrada del CHANGELOG se corrige, no
se añade una nueva.

**`mode` es explícito a propósito.** Se podría deducir del número de direcciones y sería
un error: con varias direcciones que resultan no formar un cluster, `valkey-go` no falla,
cae a standalone contra uno de los nodos. El operador pidió cluster, tiene un nodo suelto
y nada se lo dice. Con `mode`, eso es un fallo al arrancar.

### 4.3 Validación

Vive en `pkg/valkey`, en un `Config.Validate()` llamado desde `New`, **no** en
`cmd/dex/config.go`: el panel no tiene `Validate()` y así los dos binarios la heredan.

1. `mode` desconocido → error nombrando los tres válidos.
2. `sentinel` sin `masterSet` → error.
3. `standalone` con más de una dirección → error que apunta a `mode`.
4. `cluster` con `db != 0` → error. Un cluster de Valkey no tiene más base que la 0;
   un `db: 1` heredado de otro despliegue conecta y luego falla en cada comando.
5. `addresses` vacío sigue significando «todo en memoria», que es el valor por defecto y
   no es un error.

### 4.4 Lo que no hay que tocar

Todas las operaciones son de una sola clave —`GET`, `SET`, `DEL` y un script Lua con un
único `KEYS[1]`—, así que son compatibles con cluster sin cambios. `WarnIfKeysCanBeEvicted`
pregunta a un nodo cualquiera del cluster: para un aviso de configuración vale, y el
comentario de la función lo dirá.

### 4.5 Variables de entorno de la imagen

`DEX_VALKEY_ADDRESS` se mantiene y renderiza una lista de un elemento, para no romper el
ejemplo ni la imagen. Se le suman `DEX_VALKEY_ADDRESSES` (separadas por comas),
`DEX_VALKEY_MODE` y `DEX_VALKEY_MASTER_SET` en `config.docker.yaml`.

## 5. El rol `valkey`

### 5.1 Topologías

- **standalone**: una máquina en el grupo `valkey`.
- **sentinel**: un master, el resto réplicas, y los sentinels que diga el grupo
  `valkey_sentinel`. **El primer host del grupo es el master solo en el arranque
  inicial**; a partir de ahí quien manda es sentinel y el rol no vuelve a asignar el
  papel de nadie, ni siquiera si el inventario no ha cambiado (ver 5.2).
  **El rol falla si el número de votantes es par o menor que tres**,
  en vez de dejar montado un failover que no funciona el día que haga falta. El caso que
  motiva esto: dos máquinas de datos y el tercer voto en un nodo de dex.
- **cluster**: tres masters y tres réplicas, repartidos de forma que ninguna réplica caiga
  en la misma máquina que su master.

### 5.2 La regla central: Ansible no es dueño del fichero de configuración

Valkey reescribe su propia configuración —sentinel hace `CONFIG REWRITE` en cada
failover, el cluster mantiene su `nodes.conf`—. Un rol que sobrescriba `valkey.conf` en
cada pasada le devuelve al nodo un `replicaof` caducado y deshace la promoción.

- `managed.conf` lo escribe Ansible en cada despliegue: puertos, TLS, credenciales,
  `maxmemory-policy noeviction`, `appendonly yes`.
- `valkey.conf` se crea **una sola vez** (`force: no`), contiene `include managed.conf`
  en su primera línea y a partir de ahí es de Valkey. El `include` va arriba porque las
  directivas posteriores ganan: lo que el propio Valkey escriba después manda.
- `sentinel.conf` igual: se crea una vez con su `sentinel monitor` y no se vuelve a
  tocar. Los cambios posteriores van por `SENTINEL SET`.

Esto es lo que hace el rol idempotente de verdad y no solo «vuelve a poner el estado
inicial».

### 5.3 Bootstrap del cluster

Se ejecuta una vez y se detecta antes:

1. `CLUSTER INFO` en cada nodo. Si `cluster_state:ok` y el número de nodos conocidos es
   el esperado, no se toca nada.
2. Si ningún nodo conoce a nadie, `valkey-cli --cluster create ... --cluster-replicas 1`
   desde un solo host delegado (`run_once`).
3. **Si aparece medio formado** —nodos que se conocen pero el estado no es `ok`— el rol
   para con un mensaje que dice qué encontró. Reparar automáticamente un cluster a medias
   es como se pierden datos.

### 5.4 Red

Los contenedores de Valkey van en `network_mode: host`. No es pereza: cluster y sentinel
anuncian su IP real por gossip, y dentro de una red bridge un nodo anuncia la IP del
contenedor, que desde otra máquina no existe. La alternativa es `cluster-announce-ip`,
`cluster-announce-port` y `cluster-announce-bus-port` correctos en cada nodo, más
publicar el puerto del bus (el del nodo +10000). En una máquina dedicada, la red del host
elimina el problema entero.

En cluster, cada máquina corre dos procesos (un master y una réplica de otro master) en
puertos distintos: 6379 y 6380 por defecto, configurables.

### 5.5 TLS

La CA se genera en la máquina de control con `community.crypto` y **su clave no sale de
ahí**; a cada nodo van solo su certificado, su clave y el certificado de la CA. En los
nodos: `port 0` para apagar el texto plano, `tls-port`, `tls-replication yes` y, en
cluster, `tls-cluster yes`, que cifra también el bus.

### 5.6 Dos ajustes con motivo

- `maxmemory-policy noeviction` cierra el círculo con el aviso de arranque que ya
  existe: nuestros propios despliegues no lo dispararán nunca.
- `appendonly yes` porque un master que reinicia con el conjunto vacío y vuelve a ser
  master replica ese vacío a los demás.

## 6. Los roles `dex` y `dex_dashboard`

### 6.1 Dónde acaban los secretos

El panel tiene `dex.tokenFile` y lo usa. **Dex no tiene equivalente**: ni la contraseña
de la base, ni la de Valkey, ni `cookieEncryptionKey` se pueden leer de fichero. Por
entorno es peor, porque `docker inspect` las enseña.

Decisión: **el fichero de configuración de dex es el secreto**. Ansible lo renderiza
desde vault con modo 0600 y propietario el usuario de dex, y el compose lo monta en solo
lectura. En el `docker-compose.yml` no queda nada sensible.

**Trampa documentada, no arreglada**: el feature flag `expand_env` viene encendido por
defecto y aplica `os.ExpandEnv` a todas las cadenas de la configuración. Una contraseña
que contenga `$` se expande y se convierte en otra cosa o en nada. El rol genera
contraseñas sin `$` y la documentación lo advierte. No se toca el flag: es de upstream y
hay quien depende de él.

### 6.2 Comprobaciones previas

Antes de arrancar dex, el rol verifica que la base responde y acepta al usuario, y que
Valkey responde. Lo segundo porque dex se niega a arrancar sin él, y un mensaje de
Ansible es mejor que un contenedor en bucle de reinicio.

### 6.3 Orden de arranque

Entre máquinas lo ordena el playbook: `valkey` → `dex` → `dex_dashboard`. Dentro de cada
máquina, `depends_on` y `restart: unless-stopped`, para que un dex que llegue antes de
tiempo reintente en vez de morir. El compose declara `healthcheck` contra `/healthz`.

**Lo que esto no resuelve**: el `/healthz` del panel devuelve 200 sin mirar si la conexión
gRPC con dex o Valkey responden. Sirve como sonda de vida, no de disponibilidad. Esa
entrada del TODO sigue abierta y la documentación del rol la cita en vez de dar a
entender que está cubierta.

### 6.4 Detalles del despliegue

- El panel es su propio grupo del inventario, normalmente una máquina.
- El `issuer` de dex es la URL del balanceador, nunca la del nodo.
- **No hace falta afinidad de sesión** en el balanceador: es justo lo que se compra con
  el estado compartido, y la documentación lo dice explícitamente.
- La imagen se fija por variable a una etiqueta `fork-vX.Y.Z`, nunca `latest`, y la
  actualización usa `serial: 1` sobre el grupo `dex`.

## 7. Inventario

```yaml
valkey:
  hosts: {valkey-1: {}, valkey-2: {}}
  vars: {valkey_topology: sentinel}
valkey_sentinel:
  hosts: {valkey-1: {}, valkey-2: {}, dex-1: {}}
dex:
  hosts: {dex-1: {}, dex-2: {}}
  vars:
    dex_issuer: https://sso.interno/dex
    dex_storage: {type: mysql, host: mariadb.interno, database: dex, user: dex}
dex_dashboard:
  hosts: {dex-1: {}}
```

`valkey_sentinel` está separado de `valkey` precisamente para permitir el caso de dos
máquinas de datos con el tercer voto fuera. Las contraseñas van en un `group_vars`
cifrado con `ansible-vault`.

## 8. Pruebas

Tres capas, y solo dos se automatizan con honestidad.

### 8.1 El código

Siguiendo el patrón que ya existe en este repositorio: los tests de `storage/sql` se
saltan solos salvo que `DEX_MYSQL_HOST` esté definida, y CI levanta el servicio. Igual
aquí, con `DEX_VALKEY_SENTINEL_ADDRS` y `DEX_VALKEY_CLUSTER_ADDRS`, servicios en el
`docker-compose.yaml` de la raíz y en CI. Sin esas variables, `go test ./pkg/valkey/`
sigue corriendo contra miniredis como hoy.

El test que justifica el trabajo: **matar el master, esperar la promoción y comprobar que
el limitador de login sigue contando el mismo presupuesto**. Sin eso, la HA no está
probada, solo configurada.

### 8.2 Lo que el rol renderiza

La salida real del rol son ficheros. Se comprueban con una pasada que renderiza contra un
directorio temporal y afirma sobre el contenido, más `ansible-lint`, más el criterio que
de verdad delata un rol malo: **converger dos veces y que la segunda no cambie nada**.
Ahí saltaría el error de sobrescribir `valkey.conf`.

### 8.3 El gossip entre máquinas

No se automatiza con honestidad. Cluster y sentinel se anuncian por IP real, así que
probarlos de verdad exige tres espacios de red separados con su propio Docker. Molecule
sobre contenedores no lo da —comparten demonio, colisionan nombres y redes de host— y
montar Docker-in-Docker privilegiado para tres nodos es más frágil que lo que prueba.

Por tanto: **una verificación manual, hecha una vez, sobre tres máquinas de usar y tirar,
con la transcripción fechada en la documentación**, igual que se hizo con las dos réplicas
y el limitador compartido. Un `molecule` verde que no prueba la topología es peor que una
transcripción escrita.

### 8.4 MariaDB

Nadie ha verificado nunca este fork contra MariaDB. El dialecto MySQL de `storage/sql` es
deliberadamente conservador —`blob`, `datetime(3)`, `varchar(384)`, comillas invertidas en
`keys` y `groups`, arrays JSON guardados como `blob`— y no usa nada exclusivo de MySQL 8;
el `docker-compose.yaml` de upstream lista `mariadb:10.5` como alternativa comentada, y
el reintento con `tx_isolation` de `storage/sql/config.go` cubre las versiones anteriores
a MariaDB 11.1. Todo apunta a que funciona, pero **es una inferencia**: una tarea propia
levanta un contenedor de MariaDB y corre `storage/sql` contra él con `DEX_MYSQL_HOST`.

El camino `ent` (feature flag `DEX_ENT_ENABLED`, apagado por defecto) queda fuera: atlas
hace sondeo de versión y MariaDB devuelve una cadena compuesta. Se documenta como no
soportado en este despliegue.

## 9. Documentación a escribir

- `documentacion/despliegue-ansible.md`: inventario, variables, las tres topologías,
  la actualización, y qué tiene que hacer el balanceador.
- Ampliar [valkey.md](../valkey.md): la sección de alta disponibilidad deja de decir «hoy
  la conexión admite una sola dirección» y pasa a documentar las tres.
- La transcripción de la verificación manual del failover, fechada.

## 10. Trampas conocidas

Recogidas aquí para que el plan no las redescubra:

1. Sobrescribir `valkey.conf` deshace una promoción de sentinel (§5.2).
2. Repetir `--cluster create` sobre un cluster vivo destruye la asignación de slots (§5.3).
3. Con red bridge, el gossip anuncia IPs de contenedor inalcanzables (§5.4).
4. `expand_env` se come los `$` de las contraseñas (§6.1).
5. Un cluster de Valkey solo tiene la base 0 (§4.3).
6. Dos réplicas de dex con bases separadas tienen claves de firma distintas: un token
   emitido por una no valida contra el JWKS de la otra. De ahí que la base compartida sea
   un requisito y no una opción.

## 11. Qué queda fuera y va al TODO

- Sonda de disponibilidad de verdad para el panel (ya anotada).
- Colección de Ansible publicable.
- Despliegue y alta disponibilidad de la base de datos.
- El camino `ent` con MariaDB.
