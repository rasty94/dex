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
| 3 | El balanceador: HAProxy y keepalived | Pendiente de spec |
| 4 | MariaDB: despliegue y alta disponibilidad | Pendiente de spec |
| 5 | La colección de Ansible publicable | Pendiente de spec |

Las fases 1 y 2 están en
[2026-09-03-despliegue-ansible-valkey-ha.md](2026-09-03-despliegue-ansible-valkey-ha.md).

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
- **ProxySQL** (GPLv3) como punto de entrada: dex apunta ahí y no al primario.
- **orchestrator** (Apache 2.0) para detectar la caída y promover.

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

## Qué hay que verificar y nadie ha verificado

- **MariaDB con este fork.** El dialecto MySQL de `storage/sql` es conservador y todo
  apunta a que funciona, pero es una inferencia. Tarea propia en la fase 1: levantar un
  contenedor de MariaDB y correr `storage/sql` contra él con `DEX_MYSQL_HOST`.
- **El failover real entre máquinas**, tanto de Valkey como de MariaDB. Se verifica a
  mano, una vez por fase, con la transcripción fechada en la documentación.
