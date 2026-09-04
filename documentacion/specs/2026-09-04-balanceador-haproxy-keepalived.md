# Fase 3 — El balanceador: HAProxy y keepalived

> Fecha: 2026-09-04.
> Fase 3 de la
> [hoja de ruta del despliegue](2026-09-03-despliegue-hoja-de-ruta.md).
> Las fases 1 y 2 están en
> [2026-09-03-despliegue-ansible-valkey-ha.md](2026-09-03-despliegue-ansible-valkey-ha.md)
> y entregadas.

## 1. Qué se construye y por qué

El despliegue de las fases 1 y 2 no tiene entrada. Los inventarios ya escriben
`dex_issuer: https://sso.interno/dex` y `dex_dashboard_base_url:
https://sso.interno/dex-dashboard` —la URL del balanceador, nunca la de un nodo— para
que enchufarlo no obligue a reconfigurar nada, pero ese nombre no resuelve a nada. Hoy
hay dos réplicas de dex y dos del panel, y ningún sitio por donde entrar a ellas.

Esta fase pone ese sitio: dos nodos con HAProxy repartiendo, y una IP flotante entre
ellos que sobrevive a perder uno.

Lo que el balanceador tiene que hacer ya está escrito y no se decide aquí: está en
[despliegue-ansible.md](../despliegue-ansible.md), sección «Qué tiene que hacer el
balanceador». Esta spec lo implementa; no lo reinventa.

## 2. Alcance

**Dentro:**

- Rol `haproxy`: la configuración de HAProxy, en Docker como el resto.
- Rol `keepalived`: VRRP entre dos nodos, con la IP flotante.
- Grupo `loadbalancer` en el inventario y una tanda nueva en `playbooks/dex.yml`.
- Un cambio en el rol `internal_ca`: SAN adicionales en el certificado de los nodos.
- Dos variables nuevas en los roles `dex` y `dex_dashboard` para atar sus puertos a
  una interfaz concreta.
- El panel pasa a hablar con el gRPC de dex **por el balanceador**.

**Fuera, y por qué:**

- **Alta disponibilidad más allá de dos nodos con VRRP.** Dos nodos y una VIP, no un
  balanceador de balanceadores. Ya estaba decidido en la hoja de ruta.
- **La emisión y renovación del certificado público.** Lo aporta el operador como
  fichero más clave, cifrados con `ansible-vault`. Let's Encrypt exigiría que el nombre
  del issuer resolviera desde internet, que es justo lo que un `sso.interno` no hace.
- **WAF, límite de peticiones en el balanceador, o terminar el TLS de MariaDB.** No es
  el trabajo de esta fase.
- **La sonda de disponibilidad del panel.** Sigue mintiendo (devuelve 200 siempre) y
  sigue siendo una entrada abierta del TODO. Esta fase le quita el filo por otro camino —ver
  §3.3— pero no la cierra.

## 3. Decisiones y su porqué

### 3.1 El TLS se termina en el balanceador; por dentro va en claro

HAProxy termina el TLS público y habla HTTP a `:5556` y `:5560`. Es la opción elegida a
sabiendas: más simple, y la que no obliga a que cada nodo tenga un certificado que el
balanceador pueda verificar.

**Lo que cuesta, dicho entero:** entre el balanceador y los nodos viajan sin cifrar las
cookies de sesión del panel, las de dex y todo lo que pase por ahí. Vale cuando esa red
es de confianza —una VLAN privada, un vSwitch, máquinas del mismo hipervisor— y no vale
cuando no lo es. Queda documentado como condición, no como detalle.

### 3.2 El gRPC **no** se termina: pasa en TCP

El puerto 5557 de dex **ya sirve TLS** (`grpc.tlsCert` y `grpc.tlsKey` en la plantilla
del rol `dex`, con el certificado que emite la CA interna) y el panel ya lo verifica
(`dex.caCert` en la suya). Terminar ese TLS en el balanceador y volver a emitir en claro
cambiaría una credencial que hoy no sale de la máquina —el token gRPC va por loopback—
por una que viaja por la red sin cifrar. Sería un paso atrás y no lo damos.

HAProxy lo reparte en `mode tcp`, sin mirar dentro. El cifrado sigue siendo de punta a
punta entre el panel y el nodo de dex que le toque.

### 3.3 El panel deja de hablar con el dex de su propia máquina

`dex_dashboard_grpc_address` vale hoy `127.0.0.1:5557`: cada panel habla con el dex
colocado en su misma máquina. Es rápido y no toca la red, pero **si ese dex cae, ese
panel queda inútil** — y su `/healthz` devuelve 200 igualmente, así que el balanceador
le seguiría mandando administradores a páginas que van a fallar todas.

Pasa a apuntar al balanceador. No arregla la sonda —eso sigue pendiente— pero le quita
el filo: un panel ya no depende de un solo dex.

### 3.4 Lo que el passthrough del gRPC obliga a cambiar

Ese cambio no funciona solo, y es la trampa de esta fase.

Con `mode tcp`, la sesión TLS se negocia contra el nodo de dex que HAProxy elija, pero
el cliente marcó `sso.interno:5557`. Go verifica el certificado contra **ese** nombre.
Los certificados que emite hoy el rol `internal_ca` llevan
`subject_alt_name: [DNS:{{ inventory_hostname }}, IP:{{ ansible_host }}]` — o sea
`DNS:dex-1`, `IP:192.0.2.21`. **No coinciden**, y el panel no conecta.

El rol `internal_ca` gana una variable `internal_ca_extra_sans` (lista, vacía por
defecto) que se añade a las SAN de cada host. El inventario le pasa el nombre y la VIP
del balanceador para los hosts del grupo `dex`.

Se hace así y no con un certificado propio del balanceador porque en `mode tcp` el
balanceador no presenta ninguno: el certificado que ve el cliente es el del backend.

### 3.5 Ninguna afinidad de sesión, y se comprueba

`balance roundrobin` y **ni `cookie` ni `stick-table` en ninguno de los dos backends
HTTP**. Es lo que el estado compartido en Valkey existe para hacer innecesario, y una
afinidad puesta «por si acaso» esconde precisamente el fallo que delataría que el
estado compartido no está funcionando.

No basta con no escribirla: la prueba 4 de §8.1 comprueba que peticiones consecutivas
de la misma sesión aterrizan en nodos distintos y siguen funcionando.

## 4. El rol `haproxy`

### 4.1 Forma

Como los demás: la configuración se renderiza en `{{ haproxy_config_dir }}` (por
defecto `/etc/haproxy`), un `docker-compose.yml` al lado, y `community.docker.
docker_compose_v2` lo levanta. Imagen `haproxy:3.0-alpine` fijada por variable
(`haproxy_image`), nunca `latest`.

`network_mode: host`. No es cosmético: el balanceador tiene que ver la dirección real
de quien llama para poder ponerla en `X-Forwarded-For`, y con la red por defecto de
Docker todas llegarían como la de la puerta de enlace.

### 4.2 El certificado

HAProxy quiere el certificado y la clave **en un solo fichero**, concatenados. El rol lo
arma a partir de dos variables, no de una:

- `haproxy_tls_cert` — el certificado, con su cadena si la tiene.
- `haproxy_tls_key` — la clave privada.

Se escriben juntos en `{{ haproxy_config_dir }}/tls/site.pem`, modo `0600`, con
`no_log: true` en la tarea: la clave privada entera se imprimiría con un `--diff`.

Un `assert` al principio del rol se niega a seguir si falta cualquiera de las dos. Es un
componente nuestro y sin certificado no hay nada que desplegar, así que se rechaza, no
se avisa — el mismo criterio que el `dex_valkey_password` del rol `valkey`.

### 4.3 Los frontends

**`fe_https`, puerto 443, `mode http`:**

- `bind :443 ssl crt /etc/haproxy/tls/site.pem alpn h2,http/1.1`
- `http-request set-header X-Forwarded-For %[src]` — **`set`, no `add`**. `add` deja la
  cabecera que trajera el cliente y añade la real detrás; el panel lee la primera y esa
  la elige quien llama. Con `set` se descarta lo que venga de fuera. Esto es la mitad
  del trato de `trustForwardedFor`; la otra mitad es §6.2.
- `http-request set-header X-Forwarded-Proto https`
- Reparto por ruta: `path_beg /dex-dashboard` → `be_dashboard`; `path_beg /dex` →
  `be_dex`. En ese orden: `/dex-dashboard` empieza por `/dex` y con el orden inverso el
  panel no recibiría nunca nada.
- `default_backend be_dex`.

**`fe_grpc`, puerto 5557, `mode tcp`:** sin `bind ssl`, sin cabeceras, `default_backend
be_grpc`. No hay nada que mirar.

### 4.4 Los backends

| Backend | Modo | Miembros | Sonda |
| --- | --- | --- | --- |
| `be_dex` | http | grupo `dex`, puerto `dex_http_port` | `option httpchk GET /dex/healthz` |
| `be_dashboard` | http | grupo `dex_dashboard`, puerto `dex_dashboard_listen_port` | `option httpchk GET /healthz` |
| `be_grpc` | tcp | grupo `dex`, puerto `dex_grpc_port` | `option tcp-check` |

Los miembros salen de los grupos del inventario con `hostvars[h].ansible_host`, igual
que ya hacen las plantillas de `dex` y `dex_dashboard` para las direcciones de Valkey.

La ruta de la sonda de dex **se deriva de `dex_issuer`**, no se escribe a mano: dex
sirve todo bajo la ruta de su issuer, así que con `https://sso.interno/dex` la sonda es
`/dex/healthz` y con un issuer sin ruta sería `/healthz`. Fijarla a `/dex/healthz`
funcionaría en el inventario de ejemplo y dejaría todos los backends en `DOWN` en
cualquier otro. Lo mismo vale para el `vrrp_script` de §5.3.

Sobre la sonda del panel, y escrito en la propia plantilla para que nadie lo lea como un
descuido: `GET /healthz` en el panel **devuelve 200 siempre**. Sirve para sacar de
rotación un proceso muerto, no un proceso ciego. Es una sonda de vida, no de
disponibilidad, y mientras la entrada del TODO siga abierta no puede ser otra cosa.

## 5. El rol `keepalived`

### 5.1 Forma

Imagen fijada por variable, `network_mode: host` y `cap_add: [NET_ADMIN, NET_BROADCAST,
NET_RAW]`: VRRP manda paquetes crudos y mueve direcciones en la interfaz del host. Sin
esas capacidades el contenedor arranca y no hace nada, que es peor que fallar.

### 5.2 Variables

- `keepalived_vip` — la IP flotante. Sin valor por defecto; un `assert` la exige.
- `keepalived_interface` — la interfaz donde vive.
- `keepalived_router_id` — el `virtual_router_id` de VRRP. Dos despliegues distintos en
  la misma L2 con el mismo id se pelean por la misma VIP, así que el rol lo exige
  explícito en vez de inventarse uno.
- `keepalived_priority` — por host. El de más prioridad es el MASTER.
- `keepalived_auth_pass` — desde el vault. VRRP lo trunca a 8 caracteres, así que el rol
  avisa si le llega uno más largo: un secreto que el operador cree de 20 caracteres y en
  realidad es de 8 es peor que uno corto a sabiendas.

### 5.3 La comprobación de salud

Un `vrrp_script` que hace una petición real a HAProxy, no un `pidof`:

```
vrrp_script chk_haproxy {
    script "/bin/sh -c 'wget -q -O /dev/null --no-check-certificate https://127.0.0.1/dex/healthz'"
    interval 2
    fall 2
    rise 2
    weight -20
}
```

El motivo está en el fallo que evita: un HAProxy vivo pero que no responde —sin
backends sanos, con el fichero de configuración roto tras un despliegue a medias— deja
el proceso en la tabla y `pidof` diría que todo va bien. La VIP se quedaría en un nodo
que no sirve. Con una petición de verdad, la prioridad baja y la VIP se va al otro.

`weight -20` y no `fall` a MASTER directo: la bajada tiene que ser mayor que la
diferencia de prioridades entre los dos nodos, y menor que la propia prioridad, para que
el nodo pueda recuperarse solo cuando HAProxy vuelva.

## 6. Los cambios en los roles ya entregados

### 6.1 `internal_ca`: SAN adicionales

```yaml
internal_ca_extra_sans: []
```

Se concatenan a las que ya hay en la tarea «Peticion de certificado del host». El
inventario de ejemplo se las pone a los hosts del grupo `dex`, con el nombre del
balanceador y su VIP.

**Cuidado con la re-emisión**: `community.crypto.openssl_csr` reescribe el CSR cuando
cambian las SAN, y `x509_certificate` con `provider: ownca` reemite. Un nodo que ya
estuviera desplegado recibe certificado nuevo y el handler lo reinicia. Es correcto y es
lo que se quiere, pero hay que decirlo: **este cambio reinicia dex en todos los nodos**
la primera vez que se aplica. Con `serial: 1`, de uno en uno.

### 6.2 `dex` y `dex_dashboard`: atar los puertos

```yaml
dex_http_bind: "0.0.0.0"
dex_grpc_bind: "0.0.0.0"
dex_dashboard_bind: "0.0.0.0"
```

Se anteponen al `ports:` del compose (`{{ dex_http_bind }}:{{ dex_http_port }}:5556`).
El valor de fábrica no cambia el comportamiento de hoy; lo que cambia es que ahora se
puede atar a la interfaz privada, y el inventario de ejemplo lo hace.

**Por qué está en esta fase y no es un extra:** con el TLS terminado en el balanceador,
los puertos de los nodos hablan HTTP en claro, y el trato de `trustForwardedFor` es que
el balanceador sea el único camino hasta el panel. Quien alcance el 5560 de un nodo
directamente elige su propia `X-Forwarded-For`, y entonces cada petición anónima estrena
clave en un Valkey que corre con `noeviction`: llenarlo deja a los administradores sin
poder crear sesiones. Atar los puertos es lo que hace cierto el supuesto sobre el que
`trustForwardedFor: true` ya está encendido en el inventario de ejemplo.

Es un requisito, no un consejo, y la documentación lo dirá así. Un cortafuegos delante
sirve igual; el rol no lo gestiona.

### 6.3 `dex_dashboard`: el gRPC por el balanceador

`dex_dashboard_grpc_address` deja de valer `127.0.0.1:...` en el inventario de ejemplo y
pasa a `{{ balanceador }}:5557`. El **default del rol no cambia**: sin balanceador, la
máquina de al lado sigue siendo la respuesta correcta.

## 7. El orden en el playbook

Tanda nueva al final de `playbooks/dex.yml`:

```
CA interna → Valkey → dex → panel → balanceador
```

Al final a propósito. El balanceador sondea `/healthz` de los dos servicios y con
backends que aún no existen arrancaría con todo en `DOWN`; se recuperaría solo, pero un
despliegue que empieza con todo caído no se distingue de uno roto.

Que el `issuer` no resuelva mientras tanto no es un problema: el panel reintenta el
discovery (`newAuthenticatorWithRetry`), que es justo el caso para el que se escribió.

El grupo `loadbalancer` entra también en la tanda de la CA interna, para que sus hosts
tengan certificado propio si alguna vez lo necesitan, y **no** en la de Valkey.

## 8. Pruebas

### 8.1 Lo que se puede probar aquí

Converge local, con lo que ya existe (`inventories/local`) más un host de balanceador:

1. **Renderizado.** `haproxy.cfg` y `keepalived.conf` en las tres formas del inventario,
   validados con `haproxy -c -f` dentro de la propia imagen. Un fichero que no parsea es
   un balanceador que no arranca, y eso no se descubre en un `--check`.
2. **El orden de las rutas.** Una petición a `/dex-dashboard/...` llega al panel y no a
   dex. Es el fallo que el orden inverso produce y hay que verlo fallar una vez.
3. **`X-Forwarded-For` con `set`.** Una petición que trae la cabecera falsificada llega
   al panel con la dirección real, no con la inventada. Se comprueba en el log del
   panel, no leyendo la configuración.
4. **Sin afinidad.** Peticiones consecutivas con la misma cookie de sesión aterrizan en
   nodos distintos —se ve en los logs de los dos dex— y todas funcionan. Esto prueba de
   paso que el estado compartido está haciendo su trabajo.
5. **El passthrough del gRPC.** El panel, apuntando al balanceador, lista clientes.
   Prueba las dos cosas a la vez: que el TCP pasa y que las SAN nuevas valen. Se
   sabotea quitando `internal_ca_extra_sans` y comprobando que falla **por nombre de
   certificado** y no por otra cosa.
6. **La sonda saca un nodo.** Parar el dex de un nodo y ver que HAProxy deja de mandarle
   tráfico, y que vuelve al levantarlo.

### 8.2 Lo que no se puede probar aquí, y se dice

**VRRP.** Necesita dos máquinas y una L2 de verdad. En un portátil no hay forma honesta:
dos contenedores con `NET_ADMIN` en la misma máquina comparten pila de red y el
resultado no dice nada sobre el caso real.

Va a la lista ejecutable de verificación manual de
[despliegue-ansible.md](../despliegue-ansible.md), donde ya están el failover de
sentinel entre máquinas y la pérdida de un nodo del cluster. Con sus comandos y su
transcripción fechada, como los otros:

- La VIP está en el MASTER y responde.
- Parar HAProxy en el MASTER mueve la VIP al BACKUP en menos de `interval × fall`
  segundos.
- Una sesión abierta en el panel **sobrevive al salto** — es la prueba de que el estado
  compartido y el balanceador se entienden.
- Levantar el primero recupera la VIP (o no, según `nopreempt`, y el resultado se anota
  tal cual salga).

## 9. Documentación a escribir

- **`documentacion/despliegue-ansible.md`**: la sección «Qué tiene que hacer el
  balanceador» deja de describir algo pendiente y pasa a describir el rol. Se añaden:
  el requisito de atar los puertos, la condición de que la red interna sea de confianza,
  y el reinicio de todos los dex la primera vez que se aplican las SAN nuevas.
- **La lista de verificación manual**: los cuatro pasos de VRRP de §8.2.
- **La hoja de ruta**: la fase 3 pasa a «Especificada».
- **`CHANGELOG.md`** y **`DONE.md`** al cerrar, con lo que destapó probarlo.
