# Estado compartido entre réplicas con Redis

> Diseño validado el 2026-09-03. Cierra la sección 2 del TODO («Caché Distribuida») y
> «Sesiones compartidas entre réplicas» del panel.

## El problema

Dex y su panel guardan hoy tres cosas en memoria del proceso. Con una réplica funcionan;
con varias, cada una se comporta de forma distinta:

| Pieza | Qué guarda | Qué pasa con N réplicas |
| --- | --- | --- |
| `server/ratelimit` | cubos de intentos por `IP + usuario` | **fallo de seguridad**: el límite efectivo es `attempts × N` |
| `connector/keystone/cache.go` | identidad por token de Keystone | coste: cada réplica revalida el mismo token |
| `cmd/dex-dashboard` `sessionStore` | sesiones de administrador | los administradores pierden la sesión al saltar de réplica |

**No son el mismo problema.** El limitador es corrección; la caché es rendimiento; las
sesiones son usabilidad. Eso decide que cada una degrade de forma distinta cuando Redis
no está (§6), y es la razón de no tratarlas como un solo mecanismo.

**Fallo que existe hoy, con una sola réplica**: `timeCache` comprueba la expiración al
leer pero **nunca borra**. No hay barrido ni límite de tamaño, así que una entrada por
token que no se vuelva a ver se queda para siempre. Se arregla en este trabajo aunque no
tenga que ver con las réplicas.

## Alcance

Dentro: el limitador de login de dex, la caché de tokens de Keystone y las sesiones del
panel.

Fuera, a propósito:

- **El `attemptLimiter` del panel.** Su propio comentario lo dice: es un freno al ruido,
  no una defensa contra una botnet. Compartirlo no compra nada.
- **El almacenamiento de dex.** Ya es compartido: peticiones de auth, códigos, refresh
  tokens y sesiones de navegador viven en `storage.Storage`.

## Decisiones

### Un cliente compartido, sin abstracción nueva

Cada pieza **ya tiene** su interfaz local, pequeña y ajustada a lo que hace: `Allow/Reset`
en el limitador, `get/set` en la caché, `create/get/delete` en las sesiones. Una
implementación con Redis cumple esa misma forma y se inyecta donde hoy va la de memoria.
Lo único compartido es el cliente y su configuración.

Se descartó un `pkg/cache` con `Get/Set/Del` común: los tres necesitan primitivas
distintas —`INCR` atómico, `SETEX`, leer-modificar-escribir—, así que la interfaz acabaría
siendo la unión de todas, que es otra forma de no tener interfaz.

Se descartó usar `storage.Storage`: no tiene incremento atómico, y sería una escritura en
base de datos por cada intento de login.

### Redis opcional y apagado por defecto

La presencia de `redis.address` es el interruptor; no hay feature flag. Sin él no hay
dependencia nueva en ejecución ni cambio de comportamiento, que es lo que corresponde a un
despliegue de una réplica.

### El conector no recibe el cliente por construcción

`ConnectorConfig.Open(id, logger)` es una interfaz de upstream y no se toca. Dar a Keystone
su propio bloque `redis:` tampoco vale: **la configuración de un conector se guarda en la
base de datos**, así que la contraseña de Redis acabaría en el almacén y en el formulario
del panel.

En su lugar, `serve.go` registra el cliente en `pkg/redisclient` al arrancar y el conector
lo lee de ahí; su configuración solo declara `cacheShared: true`. Es estado global de
paquete, marcado con un comentario `ponytail:` que dice por qué.

`cacheShared` decide **dónde** vive la caché, no si la hay: el TTL lo sigue poniendo
`cacheTTL`, y sin `cacheTTL` no hay caché en ningún caso. Si `cacheShared` está puesto y no
hay cliente registrado, el conector **no abre**: pedir una caché compartida y quedarse con
una local sin decirlo es la clase de silencio que este trabajo viene a quitar.

## Configuración

```yaml
redis:
  address: redis:6379      # vacío = todo en memoria, como hoy
  username: dex
  password: "..."
  db: 0
  keyPrefix: "dex:"
  tls:
    caCert: /etc/ssl/redis-ca.pem
```

Con `address` puesto, dex hace `PING` al arrancar y **se niega a arrancar si falla**.
Configurar Redis es deliberado; descubrir en un incidente que el límite nunca llegó a
compartirse es peor que no arrancar. Los fallos **en ejecución** son otra cosa y degradan
según §6.

El panel lleva el mismo bloque en su propio fichero de configuración, con
`keyPrefix` propio (`dex-dashboard:` por defecto) para que un Redis compartido con dex no
mezcle las claves de los dos.

## Las tres piezas

### Limitador de login (`server/ratelimit`)

`Allow(key)` pasa a `Allow(ctx, key)`; los dos puntos de llamada
(`server/authflow/password.go`, `server/grants/password.go`) ya tienen `ctx`. `Limiter`
gana un backend opcional: si es nil, los cubos locales de hoy.

En Redis, **ventana fija** mediante un script Lua (`INCR`, y `EXPIRE` si es el primero) para
que el par sea atómico: hacerlo en dos órdenes deja una clave sin TTL si el proceso muere
en medio. `Reset` es un `DEL`.

La clave se hashea con SHA-256. Hoy es `IP + "\x00" + usuario`, y eso no puede ser el
nombre de una clave en un Redis compartido.

**Cambio de semántica**: `golang.org/x/time/rate` es un cubo de fichas suave y no se puede
compartir. Una ventana fija permite hasta `2 × attempts` a caballo entre dos ventanas. Es
lo que hace casi todo el mundo para frenar logins, y es correcto entre réplicas, que es el
objetivo; pero no es exactamente el comportamiento de ahora y va al CHANGELOG.

### Caché de Keystone (`connector/keystone`)

- **Arreglar la fuga**: barrer al escribir, como hace el limitador.
- **Estrechar el tipo**: `set(key string, value interface{})` solo llega a guardar
  `connector.Identity`. Pasa a decirlo, que es lo que permite serializar.
- Clave `sha256(subjectToken)`, nunca el token en crudo.
- Valor: la identidad en JSON.

Cualquier error de Redis se trata como fallo de caché y se pregunta a Keystone. Un login
no se cae nunca por esto. Los contadores `keystone_token_cache_lookups_total` siguen
contando igual con la caché compartida.

### Sesiones del panel (`cmd/dex-dashboard`)

Misma forma `create/get/delete`, valor en JSON. La caducidad por inactividad la lleva **el
propio Redis**: `GETEX` refresca el TTL a `min(idleTTL, lo que quede de la absoluta)`. Con
eso desaparece el `LastSeen` que hoy hay que escribir en cada petición.

## Degradación

| Pieza | Redis caído | Por qué |
| --- | --- | --- |
| Limitador | vuelve a los cubos locales | degrada a lo de hoy, no a «sin límite» |
| Caché de Keystone | fallo de caché | el login sigue; Keystone recibe más carga |
| Sesiones del panel | pide login | no se puede fingir una sesión |

Los errores de backend se registran y se cuentan en una métrica; el log va limitado para
que un Redis caído no llene el disco.

## Seguridad

- **La sesión del panel guarda `CanWrite` y `Groups`.** Quien pueda escribir en ese Redis
  se hace administrador con permiso de escritura. Eso pone la autenticación de Redis y la
  TLS en el camino crítico, no en la lista de deseos. Va documentado donde se explique el
  despliegue.
- **La caché guarda datos personales** —correo y grupos—. No se cifran: no son una
  credencial viva, a diferencia de la cookie de `mfaTrust`, y cifrar una caché obliga a
  gestionar una clave para algo que se puede regenerar. Se documenta qué hay ahí dentro.
- **Ninguna clave lleva un secreto en el nombre**: ni tokens de Keystone, ni nombres de
  usuario, ni identificadores de sesión sin hashear.

## Pruebas

`miniredis` para las tres implementaciones. La prueba que demuestra el cambio es
**cruzada**: dos instancias contra el mismo Redis, y la segunda tiene que ver lo que hizo
la primera. Un cubo local reintroducido por error la hace fallar.

- Limitador: la segunda instancia rechaza cuando la primera agotó el límite.
- Caché: la segunda acierta con lo que guardó la primera; un error de Redis se traduce en
  fallo de caché y no en error de login.
- Sesiones: una sesión creada en A vale en B; la caducidad por inactividad se sigue
  aplicando; con Redis caído se pide login.

En vivo, sobre `Ejemplos/dashboard`: se añade un servicio `redis` y **una segunda réplica
de dex** en otro puerto. Se agota el límite contra la primera y se comprueba que la segunda
ya lo rechaza. No hace falta tocar el almacenamiento: el limitador no lo usa.

## Ficheros

`pkg/redisclient/` (nuevo), `server/ratelimit/`, `connector/keystone/`,
`cmd/dex/{config,serve}.go`, `cmd/dex-dashboard/{auth,config}.go`, `config.dev.yaml`,
`config.docker.yaml`, `Ejemplos/dashboard/`, `CHANGELOG.md`.

Dependencias nuevas: `github.com/redis/go-redis/v9` y, para pruebas,
`github.com/alicebob/miniredis/v2`. Son las primeras de infraestructura externa del fork.
