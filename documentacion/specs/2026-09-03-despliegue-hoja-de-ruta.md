# Despliegue en alta disponibilidad — hoja de ruta

> Fecha: 2026-09-03.
> Este documento no especifica nada: ordena el trabajo y guarda las decisiones ya
> tomadas para que ninguna se pierda entre fases. Cada fase tiene su propia spec y su
> propio plan.

## Por qué en fases

El encargo es desplegar el fork entero en alta disponibilidad: dex, el panel, Valkey,
la base de datos y la entrada. Son cinco subsistemas que se entregan y se prueban por
separado, y cada uno deja software funcionando por sí solo. Un plan único para todo
sería un plan que nadie puede ejecutar ni revisar.

El orden no es arbitrario: cada fase necesita que la anterior exista.

| Fase | Qué entrega | Estado |
| --- | --- | --- |
| 1 | `pkg/valkey` con standalone, sentinel y cluster | Especificada |
| 2 | Roles `valkey`, `dex` y `dex_dashboard`, con el panel replicado | Especificada |
| 3 | El balanceador: HAProxy y keepalived | Entregada |
| 4 | MariaDB: despliegue y alta disponibilidad | Entregada |
| 5 | La colección de Ansible publicable | Pendiente de spec |

Las fases 1 y 2 están en
[2026-09-03-despliegue-ansible-valkey-ha.md](2026-09-03-despliegue-ansible-valkey-ha.md),
la 3 en
[2026-09-04-balanceador-haproxy-keepalived.md](2026-09-04-balanceador-haproxy-keepalived.md)
y la 4 en
[2026-09-06-mariadb-alta-disponibilidad.md](2026-09-06-mariadb-alta-disponibilidad.md).

## Decisiones ya tomadas

Se anotan aquí porque condicionan fases que todavía no tienen spec.

### La licencia importa

Se eligió Valkey sobre Redis por su licencia. El mismo criterio se aplica al resto:
**MaxScale queda descartado** —es BSL desde la 2.x, no open source— pese a ser el proxy
natural de MariaDB. Lo que entra es GPL, Apache o BSD.

### MariaDB: Galera está descartado, y no por gusto

Dex fija `transaction_isolation = SERIALIZABLE` en cada conexión, en los dos caminos de
almacenamiento: [storage/sql/config.go:236](../../storage/sql/config.go#L236) y
[storage/ent/mysql.go:53](../../storage/ent/mysql.go#L53). **Galera no soporta
SERIALIZABLE**: su replicación por certificación solo garantiza REPEATABLE READ.

Es justo la topología a la que se llega primero al decir «MariaDB en alta
disponibilidad», así que conviene que quede escrito el motivo. La única forma de usar
Galera sería relajar el aislamiento de dex, y eso es una decisión de corrección de
upstream que exigiría auditar antes qué invariantes dependen de él.

La topología elegida para la fase 4:

- **Primario y réplica con replicación semisíncrona.** Semisíncrona y no asíncrona
  porque en un failover asíncrono se pierden las escrituras más recientes, y aquí eso
  son refresh tokens recién emitidos.
- **HAProxy** como punto de entrada: dex apunta ahí y no al primario.
- **orchestrator** (Apache 2.0) para detectar la caída y promover, en modo raft a tres
  para que la decisión tenga quorum con solo dos nodos de datos. El tercer voto vive en
  una máquina que no es de base de datos, igual que el tercer sentinel vive en un nodo
  de dex.

### Corregido: HAProxy en vez de ProxySQL

Esta decisión decía **ProxySQL**, y se toma al revés. Se escribió antes de que existiera
la fase 3, cuando no había ningún balanceador en el despliegue; ahora hay uno desplegado,
entendido y probado.

Lo único que ProxySQL aporta de verdad —enrutado por consulta y reparto de lecturas— está
descartado: todo va al primario, porque con replicación por debajo una lectura enviada a
una réplica puede no ver un código de autorización recién escrito. Lo que quedaba era el
multiplexado de conexiones, y aquí eso no es una ventaja sino un riesgo: dex manda
`SET transaction_isolation='SERIALIZABLE'` en cada conexión
([storage/sql/config.go:236](../../storage/sql/config.go#L236)), y un proxy que multiplexa
tiene que rastrear esa variable o dex acaba corriendo en el `REPEATABLE-READ` por defecto
de MariaDB sin decir nada — la misma pérdida silenciosa de corrección por la que se
descartó Galera, entrando por otra puerta. **Con HAProxy en `mode tcp` el problema no
existe**: es un tubo de bytes y la sesión es de punta a punta con el backend. Dex además
ya hace su propio pool, así que el de ProxySQL no resuelve nada que tengamos.

Lo que ProxySQL sí daba y hay que sustituir: sabía cuál era el primario solo, vigilando el
`read_only` de cada nodo. HAProxy no habla SQL, así que cada nodo corre un **agente de
sonda** que responde 200 solo si es primario escribible, y HAProxy manda al único backend
que pasa la sonda. La sonda es la fuente de verdad: si orchestrator se equivoca o se cae,
HAProxy sigue mandando al nodo que se declara escribible.

### El panel se replica, y eso abre un agujero conocido

Con las sesiones de administrador en Valkey, poner dos instancias del panel es barato:
no hace falta afinidad de sesión y el estado ya se comparte.

Pero `cmd/dex-dashboard/auth.go` tiene un `attemptLimiter` **local al proceso**, y el
TODO lo justificaba diciendo que con una sola réplica del panel el límite por proceso
ya es el límite real. **Replicar el panel invalida esa premisa**: el límite de intentos
de login del panel pasa a valer `intentos × réplicas`, que es exactamente el agujero
que se cerró en dex con el estado compartido.

Entra en la fase 2: el `attemptLimiter` pasa a contar en Valkey, reutilizando el
`sharedCounter` de `server/ratelimit`. La entrada del TODO se corrige, no se cierra sin
más: su razonamiento era correcto y dejó de serlo por un cambio nuestro.

### Lo que sigue fuera

- **El camino `ent` con MariaDB** (feature flag `DEX_ENT_ENABLED`, apagado por
  defecto): atlas hace sondeo de versión y MariaDB devuelve una cadena compuesta del
  tipo `5.5.5-10.11.2-MariaDB`. Se documenta como no soportado.
- **Alta disponibilidad del balanceador más allá de keepalived**: dos nodos con VRRP,
  no un balanceador de balanceadores.

### MariaDB espera el ack DESPUÉS de confirmar, y eso deshace media decisión

Comprobado contra un `mariadb:11.4` de verdad, no leído: la semisíncrona viene integrada
(no hace falta instalar plugin) y
**`rpl_semi_sync_master_wait_point` vale `AFTER_COMMIT` por defecto**. MySQL trae
`AFTER_SYNC`; MariaDB no.

Importa porque deshace justo el motivo por el que se eligió semisíncrona. Con
`AFTER_COMMIT` el primario **confirma y después** espera el ack de la réplica: si muere
en ese hueco, la transacción está confirmada localmente y en ninguna réplica. Un cliente
pudo ver el commit, y la promoción se la lleva por delante. Con `AFTER_SYNC` el primario
espera el ack **antes** de confirmar, que es lo que se creía estar comprando.

`AFTER_SYNC` está disponible y se puede poner en caliente (probado). La fase 4 lo fija.

## Qué hay que verificar y nadie ha verificado

- **El failover real entre máquinas**, tanto de Valkey como de MariaDB. Se verifica a
  mano, una vez por fase, con la transcripción fechada en la documentación.
