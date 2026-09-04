# Valkey — el estado que las réplicas comparten

Valkey es **opcional y está apagado por defecto**. Sin `valkey.addresses` cada proceso
guarda su estado en memoria y no hace falta ningún servidor: es lo que quiere un
despliegue de una sola réplica de dex y un solo panel.

Se enciende cuando hay más de una réplica, porque entonces «cada proceso el suyo»
deja de ser un detalle interno y pasa a ser un agujero: el límite de intentos de login
se multiplica por el número de réplicas.

Esta página es el sitio donde vive lo que el servidor tiene que cumplir. Los detalles
de cada componente están en su propia documentación:

- Caché de tokens del conector Keystone → [keystone_connector.md](keystone_connector.md)
- Sesiones del panel de administración → [dashboard-administracion.md](dashboard-administracion.md)
- Contadores del limitador de login → sin configuración propia: van a Valkey en cuanto
  `valkey.addresses` está puesto en el `dex.yaml`.

## Configuración

```yaml
valkey:
  mode: standalone         # standalone (por defecto), sentinel o cluster
  addresses:
    - valkey.interno:6379
  keyPrefix: "dex:"        # por defecto "dex:"
  username: dex            # opcional
  password: "…"            # opcional
  db: 0                    # opcional; el modo cluster no admite otra que 0
  tls:
    caCert: /etc/dex/valkey-ca.pem
    insecureSkipVerify: false
```

`mode` es explícito a propósito: deducirlo del número de direcciones falla en
silencio. Dadas varias direcciones que resultan no formar un cluster, el cliente no
falla — cae a standalone contra una de ellas sin decir nada. `masterSet` (ver más
abajo) solo se usa, y hace falta, en modo `sentinel`.

El panel tiene un bloque `valkey` idéntico en su propio fichero. Pueden apuntar al
mismo servidor: las claves llevan prefijos distintos y no se pisan.

**Autentica la conexión.** Quien pueda escribir en este Valkey puede entrar en el panel
como administrador, porque la sesión guardada lleva el correo y los grupos de los que se
calculan los permisos. Es la misma superficie que un token de administrador robado, y
no es una recomendación opcional.

## Lo que el servidor tiene que cumplir

### `maxmemory-policy: noeviction`

Es el único requisito duro, y el que más fácil se incumple sin darse cuenta: mucha
gente ya tiene un Valkey o un Redis levantado **como caché**, con un `maxmemory` y una
política de desalojo, y apuntar dex ahí parece gratis.

No lo es. **Todo lo que dex guarda aquí lleva caducidad**, y eso es justo lo que las
políticas de desalojo buscan:

- `allkeys-*` puede tirar cualquier clave.
- `volatile-*` tira las que tienen TTL, es decir, **todas las de dex**.

Lo que se va con ellas son los contadores del limitador de login: bajo presión de
memoria, el presupuesto de intentos se reinicia solo, que es exactamente lo que el
límite existe para impedir. Y las sesiones del panel, que se cierran antes de tiempo.

`noeviction` rechaza escrituras en vez de tirar claves. Es el comportamiento correcto
aquí: un intento de login que no se puede contar es mejor que uno que se cuenta contra
un presupuesto que acaba de reiniciarse solo.

Con `maxmemory: 0` —el valor por defecto— no se desaloja nada, diga lo que diga la
política.

**Dex lo comprueba al arrancar**: pregunta por `maxmemory` y `maxmemory-policy` y deja
un aviso en el log si la combinación permite desalojos. Es solo un aviso, porque el
servidor puede no ser suyo. Si el servidor no responde a `CONFIG` —muchos servicios
gestionados lo desactivan— no dice nada: un aviso sobre el que nadie puede actuar es
ruido.

### Persistencia

Nada de lo que hay aquí es la fuente de verdad de nada: todo se puede reconstruir
volviendo a entrar. Pero un reinicio de Valkey **sí tiene consecuencias visibles**:

| Se pierde | Consecuencia |
| --- | --- |
| Contadores del limitador | Todo el mundo vuelve a tener el presupuesto entero de intentos |
| Sesiones del panel | Los administradores vuelven a la pantalla de login |
| Caché de tokens de Keystone | Los siguientes logins pagan un viaje a Keystone |

Ninguna es grave, pero la primera merece pensarse si el reinicio se puede provocar
desde fuera. El ejemplo de `Ejemplos/dashboard` corre con `--save ""`, sin persistencia,
que para un ejemplo está bien.

### Alta disponibilidad

Con `valkey.addresses` puesto, dex se niega a arrancar si el servidor no responde —a
propósito: arrancar sin él significa arrancar sin límite de login compartido—, así que
un orquestador tiene que reintentar en vez de dar el contenedor por muerto. Eso vale
para las tres topologías siguientes.

`mode` no se deduce del número de direcciones: se declara. Dado un `standalone` con
varias direcciones que en realidad no forman un cluster, `valkey-go` no falla — cae a
hablar con una sola de ellas sin decir nada, y el operador se queda creyendo que tiene
un cluster cuando tiene un nodo.

#### Un solo nodo (`standalone`)

Sin réplicas de Valkey. Es el modo por defecto y el que usa `Ejemplos/dashboard`.

```yaml
valkey:
  mode: standalone
  addresses:
    - valkey:6379
```

#### Sentinel

Varios sentinels vigilan un maestro y sus réplicas, y hacen el failover cuando el
maestro cae. `addresses` lleva los sentinels, no el maestro: `valkey-go` les pregunta
quién es el maestro ahora mismo y sigue el failover sin que dex tenga que hacer nada.
`masterSet` es obligatorio — es el nombre bajo el que los sentinels vigilan ese
maestro, y sin él no hay forma de saber a cuál de los que vigilan.

```yaml
valkey:
  mode: sentinel
  addresses:
    - sentinel1:26379
    - sentinel2:26379
    - sentinel3:26379
  masterSet: dex
```

**La regla de los tres votantes**: un sentinel necesita mayoría para decidir que el
maestro está caído y promocionar una réplica. Con dos sentinels, uno solo ya no es
mayoría de nada — un corte de red entre ambos deja al sistema sin poder decidir. Tres
es el mínimo que sobrevive a la caída de uno sin perder la capacidad de votar.

#### Cluster

Los datos se reparten en particiones (`hash slots`) entre varios nodos. `addresses`
lleva un subconjunto de los nodos del cluster — no hacen falta todos, `valkey-go`
descubre el resto al conectar — y las redirecciones (`MOVED`, `ASK`) las sigue el
cliente, no dex.

```yaml
valkey:
  mode: cluster
  addresses:
    - valkey1:6379
    - valkey2:6379
    - valkey3:6379
```

Un cluster no tiene más base de datos que la 0: `db` distinto de `0` en este modo es
un error de arranque, no una conexión que falla más tarde en el primer comando.

Para desplegar cualquiera de las tres topologías con Ansible —el rol que monta Valkey,
dex y el panel en máquinas reales, con TLS y los secretos cifrados—, ver
[despliegue-ansible.md](despliegue-ansible.md).

## Qué pasa si Valkey se cae con dex ya sirviendo

Cada componente elige distinto, y a propósito:

| Componente | Elección | Por qué |
| --- | --- | --- |
| Limitador de login | Cae al contador local del proceso | Un corte no puede apagar el límite; degradar a «una réplica» sí |
| Caché de Keystone | Falla abierto: es un fallo de caché | Un login no se rompe porque la optimización no esté |
| Sesiones del panel | Falla cerrado: pide login | No se puede dar por buena una sesión que no se puede leer |

Todas las llamadas llevan un plazo de 2 segundos, así que un servidor que ha dejado de
contestar no cuelga peticiones: las convierte en una de las tres cosas de arriba.

### Cómo se ve en las métricas

Que un corte sea invisible es peor que el corte. Las dos métricas que lo dicen:

- `dex_login_rate_limit_backend_errors_total` sube cuando el limitador ha tenido que
  caer al contador local. Un cliente que corta su propia petición **no** cuenta aquí.
- `keystone_token_cache_lookups_total{result="error"}` distingue «Valkey no responde» de
  `result="miss"`, que es un token que sencillamente no estaba cacheado.

Cualquiera de las dos subiendo de forma sostenida significa que las réplicas han dejado
de compartir estado, aunque todo lo demás siga funcionando.
