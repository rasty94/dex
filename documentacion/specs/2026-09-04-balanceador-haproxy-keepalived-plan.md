# Fase 3 — El balanceador: plan de implementación

> **Para quien lo ejecute:** SUB-SKILL OBLIGATORIA: usar
> superpowers:subagent-driven-development (recomendada) o
> superpowers:executing-plans, tarea a tarea. Los pasos llevan casilla (`- [ ]`).

**Objetivo:** dar entrada al despliegue — dos nodos con HAProxy repartiendo entre las
réplicas de dex y del panel, con una IP flotante que sobrevive a perder uno.

**Arquitectura:** dos roles nuevos (`haproxy`, `keepalived`) con la misma forma que los
tres ya entregados: configuración renderizada en `/etc/<servicio>`, `docker-compose.yml`
al lado, `community.docker.docker_compose_v2` levantando, y un handler que fuerza
`recreate: always` porque compose no considera cambio el contenido de un bind mount. Más
tres cambios quirúrgicos en roles ya entregados, que van juntos porque por separado
rompen.

**Tecnologías:** Ansible, `community.docker`, `community.crypto`, HAProxy 3.0,
keepalived, Docker Compose.

**Spec:**
[2026-09-04-balanceador-haproxy-keepalived.md](2026-09-04-balanceador-haproxy-keepalived.md)

## Restricciones globales

- **Documentación en español, código y comentarios en inglés.** Los comentarios de las
  plantillas Jinja y de los ficheros de Ansible van en **español**, como los de los tres
  roles ya entregados; el inglés es para el código Go.
- **Commits en español, sin tildes** (la ñ sí), prefijo convencional, asunto en
  minúsculas y sin punto. **Sin atribución de ninguna herramienta.**
- **Nada de `git add -A`.** `references/` no se versiona y `go build ./...` deja un
  binario en `cmd/dex-dashboard`.
- **`pre-commit run --files <cambiados>`** antes de cada commit. El hook de
  `ansible-lint` funciona: si falla, es un fallo de verdad, no del hook.
- **Imágenes fijadas por etiqueta, nunca `latest`**, y siempre por variable.
- **Ningún secreto en el entorno de un contenedor** ni en el `argv` de un proceso:
  `docker inspect` y `docker top` los leen. Fichero en disco modo `0600`, y `no_log: true`
  en la tarea que lo escribe.
- **Los defaults de los roles ya entregados no cambian de comportamiento.** Lo que
  cambia lo declara el inventario.
- **Nada fuera del worktree**: ni ficheros, ni contenedores, ni imágenes, ni volúmenes
  que no haya creado esta tarea. La máquina tiene `Ejemplos/dashboard` corriendo
  (contenedores `dex`, `dex-replica`, `dex-dashboard`, `valkey`) y **no se toca**.
  Cualquier contenedor propio lleva prefijo `t13-`.

---

## Estructura de ficheros

**Se crean:**

| Fichero | Responsabilidad |
| --- | --- |
| `ansible/roles/haproxy/defaults/main.yml` | Variables e imagen |
| `ansible/roles/haproxy/tasks/main.yml` | Asserts, TLS, configuración, levantar |
| `ansible/roles/haproxy/handlers/main.yml` | `Reiniciar haproxy` |
| `ansible/roles/haproxy/templates/haproxy.cfg.j2` | Frontends y backends |
| `ansible/roles/haproxy/templates/docker-compose.yml.j2` | El servicio |
| `ansible/roles/keepalived/defaults/main.yml` | VIP, interfaz, prioridad |
| `ansible/roles/keepalived/tasks/main.yml` | Asserts, configuración, levantar |
| `ansible/roles/keepalived/handlers/main.yml` | `Reiniciar keepalived` |
| `ansible/roles/keepalived/templates/keepalived.conf.j2` | VRRP y el `vrrp_script` |
| `ansible/roles/keepalived/templates/docker-compose.yml.j2` | El servicio |

**Se modifican:**

| Fichero | Cambio |
| --- | --- |
| `ansible/roles/internal_ca/defaults/main.yml` | `internal_ca_extra_sans: []` |
| `ansible/roles/internal_ca/tasks/main.yml` | Concatenar las SAN adicionales |
| `ansible/roles/dex/defaults/main.yml` | `dex_http_bind`, `dex_grpc_bind` |
| `ansible/roles/dex/templates/docker-compose.yml.j2` | Anteponer el bind a `ports:` |
| `ansible/roles/dex_dashboard/defaults/main.yml` | `dex_dashboard_bind` |
| `ansible/roles/dex_dashboard/templates/docker-compose.yml.j2` | Ídem |
| `ansible/playbooks/dex.yml` | Grupo en la tanda de la CA, tanda del balanceador |
| `ansible/inventories/ejemplo/hosts.yml` | Grupo `loadbalancer` y las variables |
| `ansible/inventories/ejemplo/group_vars/all.yml` | Certificado y clave del sitio |
| `documentacion/despliegue-ansible.md` | §9 |
| `CHANGELOG.md`, `TODO.md`, `DONE.md` | §9 |

---

## Tarea 1: El rol `haproxy`

**Ficheros:**
- Crear: `ansible/roles/haproxy/{defaults,tasks,handlers,templates}/`
- Probar: renderizado con Jinja2 más `haproxy -c -f` dentro de la propia imagen

**Interfaces:**
- Consume: los grupos `dex` y `dex_dashboard` del inventario, con
  `hostvars[h].ansible_host`; `dex_http_port`, `dex_grpc_port`,
  `dex_dashboard_listen_port`, y `dex_issuer` leído por `hostvars` del primer host de
  `dex` (un host del grupo `loadbalancer` no lo tiene).
- Produce: `haproxy_config_dir`, `haproxy_image`, `haproxy_tls_cert`,
  `haproxy_tls_key`, `haproxy_https_port`, `haproxy_grpc_port`. La Tarea 2 usa
  `haproxy_https_port` en el `vrrp_script`.

- [ ] **Paso 1: `defaults/main.yml`**

```yaml
# La configuracion es entera de Ansible, al reves que en el rol valkey: HAProxy
# no reescribe su propio fichero, asi que aqui no hace falta la separacion
# managed.conf / valkey.conf.
haproxy_config_dir: /etc/haproxy

# Parametrizado por el mismo motivo que dex_container_name y
# valkey_container_name: poder correr un converge de prueba en una maquina que
# ya tenga otro haproxy suelto sin chocar con el.
haproxy_container_name: haproxy
haproxy_image: haproxy:3.0-alpine

haproxy_https_port: 443
haproxy_grpc_port: 5557

# El certificado publico del sitio. Lo aporta el operador cifrado con
# ansible-vault: no se emite aqui. La CA interna no vale para esto --el issuer
# lo consumen clientes que no la conocen-- y Let's Encrypt exigiria que el
# nombre resolviera desde internet, que es justo lo que un "sso.interno" no
# hace. Sin valor por defecto a proposito: el assert de tasks/main.yml se
# niega a desplegar sin ellos.
haproxy_tls_cert: ""
haproxy_tls_key: ""

# La ruta de la sonda de dex sale del issuer y NO se escribe a mano: dex sirve
# todo bajo la ruta de su issuer, asi que con https://sso.interno/dex la sonda
# es /dex/healthz y con un issuer sin ruta seria /healthz. Fijarla funcionaria
# en el inventario de ejemplo y dejaria todos los backends en DOWN en cualquier
# otro.
#
# OJO: dex_issuer lo define el inventario en las vars del grupo dex, asi que un
# host del grupo loadbalancer NO lo tiene. Se lee del primer host de dex por
# hostvars, igual que las plantillas de dex y del panel leen las direcciones de
# Valkey. Escribir "dex_issuer" a secas aqui rompe con undefined variable en el
# primer despliegue de verdad y funciona en cualquier converge donde el mismo
# host este en los dos grupos, que es la peor combinacion posible.
haproxy_dex_issuer: "{{ hostvars[groups['dex'][0]].dex_issuer }}"
haproxy_dex_health_path: "{{ (haproxy_dex_issuer | urlsplit('path')) | regex_replace('/$', '') }}/healthz"

# El /healthz del panel cuelga de la raiz de su propio puerto, no del baseURL.
haproxy_dashboard_health_path: /healthz
```

- [ ] **Paso 2: `handlers/main.yml`**

```yaml
# Igual que en los roles dex y valkey: docker compose solo recrea un contenedor
# cuando cambia la definicion del propio servicio, no el contenido de un fichero
# montado por bind mount (aqui, haproxy.cfg y el .pem). recreate: always fuerza
# el reinicio cuando este handler salta.
- name: Reiniciar haproxy
  community.docker.docker_compose_v2:
    project_src: "{{ haproxy_config_dir }}"
    state: present
    recreate: always
```

- [ ] **Paso 3: `templates/haproxy.cfg.j2`**

```
# Renderizado por Ansible. No editar aqui: los cambios se pierden en el
# proximo despliegue. La plantilla esta en ansible/roles/haproxy/templates/.
global
    log stdout format raw local0
    maxconn 4096

defaults
    log     global
    mode    http
    option  httplog
    timeout connect 5s
    timeout client  60s
    timeout server  60s

frontend fe_https
    bind :{{ haproxy_https_port }} ssl crt /usr/local/etc/haproxy/tls/site.pem alpn h2,http/1.1
    mode http

    # set, no add. Con "add" queda delante la cabecera que trajera el cliente
    # y el panel lee la primera: la elegiria quien llama, y entonces cada
    # peticion anonima estrena clave en un Valkey que corre con noeviction.
    # Esta es la mitad del trato de trustForwardedFor; la otra es que los
    # puertos de los nodos no sean alcanzables por otro camino (dex_http_bind
    # y dex_dashboard_bind).
    http-request set-header X-Forwarded-For %[src]
    http-request set-header X-Forwarded-Proto https

    # Una sola regla y el resto por defecto. La tentacion es escribir tambien
    # "use_backend be_dex if { path_beg <ruta del issuer> }", y con un issuer
    # sin ruta eso renderiza "path_beg " sin argumento: HAProxy no arranca.
    # Con default_backend no hace falta.
    #
    # El orden importaria si hubiera dos reglas: /dex-dashboard empieza por
    # /dex, asi que al reves el panel no recibiria nunca nada. Queda escrito
    # porque es el fallo que aparece en cuanto alguien anada la segunda.
    use_backend be_dashboard if { path_beg /dex-dashboard }
    default_backend be_dex

frontend fe_grpc
    # mode tcp y sin "ssl": el 5557 de dex ya sirve TLS con el certificado de
    # la CA interna y el panel lo verifica. Terminarlo aqui y volver a emitir
    # en claro cambiaria una credencial que hoy no sale de la maquina --el
    # token gRPC-- por una que viaja por la red sin cifrar.
    bind :{{ haproxy_grpc_port }}
    mode tcp
    default_backend be_grpc

# balance roundrobin y NI cookie NI stick-table en ningun backend HTTP. La
# afinidad de sesion es justo lo que el estado compartido en Valkey existe
# para hacer innecesaria, y ponerla "por si acaso" esconderia el fallo que
# delataria que el estado compartido no funciona.
backend be_dex
    mode http
    balance roundrobin
    option httpchk
    http-check send meth GET uri {{ haproxy_dex_health_path }}
{% for h in groups['dex'] %}
    server {{ h }} {{ hostvars[h].ansible_host }}:{{ dex_http_port | default(5556) }} check
{% endfor %}

backend be_dashboard
    mode http
    balance roundrobin
    # Aviso, y no es un descuido: el /healthz del panel devuelve 200 SIEMPRE,
    # sin mirar si su conexion gRPC con dex o Valkey responden
    # (cmd/dex-dashboard/main.go). Saca de rotacion un proceso muerto, no uno
    # ciego. Es una sonda de vida, no de disponibilidad, y no puede ser otra
    # cosa mientras la entrada del TODO siga abierta.
    option httpchk
    http-check send meth GET uri {{ haproxy_dashboard_health_path }}
{% for h in groups['dex_dashboard'] %}
    server {{ h }} {{ hostvars[h].ansible_host }}:{{ dex_dashboard_listen_port | default(5560) }} check
{% endfor %}

backend be_grpc
    mode tcp
    balance roundrobin
    option tcp-check
{% for h in groups['dex'] %}
    server {{ h }} {{ hostvars[h].ansible_host }}:{{ dex_grpc_port | default(5557) }} check
{% endfor %}
```

- [ ] **Paso 4: `templates/docker-compose.yml.j2`**

```yaml
# Renderizado por Ansible. No editar aqui: los cambios se pierden en el
# proximo despliegue. La plantilla esta en ansible/roles/haproxy/templates/.
services:
    haproxy:
        image: {{ haproxy_image }}
        container_name: {{ haproxy_container_name }}
        restart: unless-stopped
        # Red de host, y no es cosmetico: el balanceador tiene que ver la
        # direccion real de quien llama para ponerla en X-Forwarded-For. Con
        # la red por defecto de Docker todas llegarian como la de la puerta
        # de enlace, y el limitador de intentos del panel contaria a todo el
        # mundo en el mismo cubo.
        network_mode: host
        volumes:
            - {{ haproxy_config_dir }}/haproxy.cfg:/usr/local/etc/haproxy/haproxy.cfg:ro
            - {{ haproxy_config_dir }}/tls/site.pem:/usr/local/etc/haproxy/tls/site.pem:ro
```

- [ ] **Paso 5: `tasks/main.yml`**

```yaml
# Este servicio es nuestro (lo instala y lo arranca este rol), asi que sin
# certificado se falla en vez de avisar: mismo criterio que el
# dex_valkey_password del rol valkey.
- name: El certificado y la clave del sitio son obligatorios
  ansible.builtin.assert:
    that:
      - haproxy_tls_cert | length > 0
      - haproxy_tls_key | length > 0
    fail_msg: >-
      haproxy_tls_cert y haproxy_tls_key son obligatorias: HAProxy termina
      aqui el TLS del issuer y sin certificado no hay nada que desplegar. Van
      cifradas con ansible-vault. La CA interna no vale: el issuer lo
      consumen clientes que no la conocen.
    success_msg: El certificado del sitio esta presente.

- name: Directorio de configuracion
  ansible.builtin.file:
    path: "{{ haproxy_config_dir }}/tls"
    state: directory
    mode: "0750"

- name: Certificado y clave, en el fichero unico que HAProxy espera
  # HAProxy quiere los dos concatenados en un solo .pem. no_log: la clave
  # privada entera se imprimiria con un -v o un --diff.
  ansible.builtin.copy:
    content: |
      {{ haproxy_tls_cert }}
      {{ haproxy_tls_key }}
    dest: "{{ haproxy_config_dir }}/tls/site.pem"
    mode: "0600"
  notify: Reiniciar haproxy
  no_log: true

- name: Configuracion
  ansible.builtin.template:
    src: haproxy.cfg.j2
    dest: "{{ haproxy_config_dir }}/haproxy.cfg"
    mode: "0644"
  notify: Reiniciar haproxy

- name: Compose
  ansible.builtin.template:
    src: docker-compose.yml.j2
    dest: "{{ haproxy_config_dir }}/docker-compose.yml"
    mode: "0644"
  notify: Reiniciar haproxy

- name: Levantar haproxy
  community.docker.docker_compose_v2:
    project_src: "{{ haproxy_config_dir }}"
    state: present
```

- [ ] **Paso 6: probar que el fichero parsea, que es lo que decide si arranca**

Script en el scratchpad, con el mismo enfoque que ya se usó para las plantillas de
`dex` y `dex_dashboard`: Jinja2 con `StrictUndefined` y los defaults del rol cargados
desde el YAML, y después `haproxy -c -f` **dentro de la imagen**.

```python
import json, io
from jinja2 import Environment, FileSystemLoader, StrictUndefined
import yaml

env = Environment(loader=FileSystemLoader("ansible/roles/haproxy/templates"),
                  undefined=StrictUndefined, trim_blocks=True,
                  keep_trailing_newline=True)
env.filters["urlsplit"] = lambda u, part: __import__("urllib.parse", fromlist=["x"]).urlsplit(u)._asdict()[part]
env.filters["regex_replace"] = lambda s, pat, rep: __import__("re").sub(pat, rep, s)

defaults = yaml.safe_load(open("ansible/roles/haproxy/defaults/main.yml"))
v = dict(defaults,
         groups={"dex": ["dex-1", "dex-2"], "dex_dashboard": ["dex-1", "dex-2"]},
         hostvars={"dex-1": {"ansible_host": "192.0.2.21"},
                   "dex-2": {"ansible_host": "192.0.2.22"}},
         dex_issuer="https://sso.interno/dex",
         dex_http_port=5556, dex_grpc_port=5557, dex_dashboard_listen_port=5560)
# haproxy_dex_health_path es un default que a su vez usa dex_issuer: se
# re-renderiza a mano, porque Jinja no resuelve recursivamente los defaults.
v["haproxy_dex_health_path"] = env.from_string(defaults["haproxy_dex_health_path"]).render(**v)
open("/tmp/haproxy.cfg", "w").write(env.get_template("haproxy.cfg.j2").render(**v))
```

Y después:

```bash
docker run --rm -v /tmp/haproxy.cfg:/c.cfg:ro haproxy:3.0-alpine haproxy -c -f /c.cfg
```

Esperado: `Configuration file is valid`. Un fichero que no parsea es un balanceador que
no arranca, y un `--check` de Ansible no lo descubre.

- [ ] **Paso 7: sabotear la prueba una vez**

Cambiar `option httpchk` por `option httpchkk` y comprobar que `haproxy -c -f` **falla**
nombrando la línea. Revertir. Sin esto, la prueba podría estar validando un fichero que
nunca se leyó.

- [ ] **Paso 8: comprobar la ruta de la sonda con dos issuers**

Renderizar dos veces: con `dex_issuer=https://sso.interno/dex` la sonda tiene que salir
`/dex/healthz`; con `dex_issuer=https://sso.interno` tiene que salir `/healthz`. Si sale
`//healthz`, el `regex_replace` del default está mal.

- [ ] **Paso 9: commit**

```bash
git add ansible/roles/haproxy/
git commit -m "feat(ansible): rol haproxy con los dos frontends"
```

---

## Tarea 2: El rol `keepalived`

**Ficheros:**
- Crear: `ansible/roles/keepalived/{defaults,tasks,handlers,templates}/`
- Probar: renderizado, más los asserts

**Interfaces:**
- Consume: `haproxy_https_port` y `haproxy_dex_health_path` de la Tarea 1.
- Produce: `keepalived_vip`, que el inventario usa en `internal_ca_extra_sans`
  (Tarea 3).

- [ ] **Paso 1: `defaults/main.yml`**

```yaml
keepalived_config_dir: /etc/keepalived
keepalived_container_name: keepalived
keepalived_image: osixia/keepalived:2.0.20

# Sin valor por defecto: los tres asserts de tasks/main.yml los exigen.
keepalived_vip: ""
keepalived_interface: ""
# El virtual_router_id de VRRP. Dos despliegues distintos en la misma L2 con
# el mismo id se pelean por la misma VIP, asi que se exige explicito en vez de
# inventarse uno: un choque asi se manifiesta como una VIP que parpadea entre
# maquinas de despliegues que no se conocen, y cuesta dias encontrarlo.
keepalived_router_id: 0
keepalived_auth_pass: ""

# El de mas prioridad es el MASTER. La diferencia entre los dos nodos tiene
# que ser MENOR que keepalived_health_weight en valor absoluto, o la bajada
# por sonda no llega a cambiar quien manda: ver el assert.
keepalived_priority: 100
keepalived_health_weight: -20
keepalived_health_interval: 2
keepalived_health_fall: 2
keepalived_health_rise: 2
```

- [ ] **Paso 2: `handlers/main.yml`**

```yaml
- name: Reiniciar keepalived
  community.docker.docker_compose_v2:
    project_src: "{{ keepalived_config_dir }}"
    state: present
    recreate: always
```

- [ ] **Paso 3: `templates/keepalived.conf.j2`**

```
# Renderizado por Ansible. No editar aqui: los cambios se pierden en el
# proximo despliegue. La plantilla esta en ansible/roles/keepalived/templates/.

# Una peticion de verdad, no un pidof. Un HAProxy vivo pero que no responde
# --sin backends sanos, o con el fichero de configuracion roto tras un
# despliegue a medias-- sigue estando en la tabla de procesos, asi que pidof
# diria que todo va bien y la VIP se quedaria en un nodo que no sirve.
vrrp_script chk_haproxy {
    script "/bin/sh -c 'wget -q -O /dev/null --no-check-certificate https://127.0.0.1:{{ haproxy_https_port }}{{ haproxy_dex_health_path }}'"
    interval {{ keepalived_health_interval }}
    fall {{ keepalived_health_fall }}
    rise {{ keepalived_health_rise }}
    # weight negativo y no "fall a BACKUP": bajar la prioridad deja que el
    # nodo se recupere solo cuando HAProxy vuelva, sin intervencion.
    weight {{ keepalived_health_weight }}
}

vrrp_instance dex {
    interface {{ keepalived_interface }}
    virtual_router_id {{ keepalived_router_id }}
    priority {{ keepalived_priority }}
    advert_int 1

    authentication {
        auth_type PASS
        # VRRP trunca esto a 8 caracteres. El assert del rol avisa si llega
        # uno mas largo: un secreto que el operador cree de 20 y en realidad
        # es de 8 es peor que uno corto a sabiendas.
        auth_pass {{ keepalived_auth_pass }}
    }

    virtual_ipaddress {
        {{ keepalived_vip }}
    }

    track_script {
        chk_haproxy
    }
}
```

- [ ] **Paso 4: `templates/docker-compose.yml.j2`**

```yaml
# Renderizado por Ansible. No editar aqui: los cambios se pierden en el
# proximo despliegue. La plantilla esta en ansible/roles/keepalived/templates/.
services:
    keepalived:
        image: {{ keepalived_image }}
        container_name: {{ keepalived_container_name }}
        restart: unless-stopped
        # VRRP manda paquetes crudos y mueve direcciones en la interfaz del
        # host. Sin red de host y sin estas capacidades el contenedor arranca
        # y no hace nada, que es peor que fallar.
        network_mode: host
        cap_add:
            - NET_ADMIN
            - NET_BROADCAST
            - NET_RAW
        command: ["--vrrp", "--log-detail", "--dont-fork", "-f", "/container/service/keepalived/assets/keepalived.conf"]
        volumes:
            - {{ keepalived_config_dir }}/keepalived.conf:/container/service/keepalived/assets/keepalived.conf:ro
```

- [ ] **Paso 5: `tasks/main.yml`**

```yaml
- name: Las variables sin valor por defecto son obligatorias
  ansible.builtin.assert:
    that:
      - keepalived_vip | length > 0
      - keepalived_interface | length > 0
      - keepalived_router_id | int > 0
      - keepalived_auth_pass | length > 0
    fail_msg: >-
      keepalived_vip, keepalived_interface, keepalived_router_id y
      keepalived_auth_pass son obligatorias. El router_id tiene que ser
      explicito: dos despliegues en la misma L2 con el mismo id se pelean por
      la misma VIP.
    success_msg: Las variables obligatorias de keepalived estan puestas.

- name: La contrasena de VRRP se trunca a 8 caracteres
  # Aviso y no fallo: VRRP funciona igual, y esto no es una credencial de
  # acceso a datos sino la que evita que un vecino se meta en el grupo. Lo
  # que no puede pasar es que el operador crea que tiene 20 caracteres de
  # entropia cuando tiene 8.
  ansible.builtin.debug:
    msg: >-
      keepalived_auth_pass tiene {{ keepalived_auth_pass | length }}
      caracteres y VRRP solo usa los 8 primeros. Los demas no cuentan.
  when: keepalived_auth_pass | length > 8

- name: La bajada por sonda tiene que poder cambiar quien manda
  # Si la diferencia de prioridades entre los dos nodos es mayor que la
  # bajada, el MASTER con HAProxy caido sigue siendo el de mas prioridad y la
  # VIP no se mueve: keepalived queda configurado, arrancado, y sin hacer lo
  # unico para lo que esta.
  ansible.builtin.assert:
    that:
      - (keepalived_health_weight | int | abs) > (
          (groups['loadbalancer'] | map('extract', hostvars, 'keepalived_priority') | map('int') | max)
          - (groups['loadbalancer'] | map('extract', hostvars, 'keepalived_priority') | map('int') | min))
    fail_msg: >-
      keepalived_health_weight ({{ keepalived_health_weight }}) no supera la
      diferencia de prioridades entre los nodos del grupo loadbalancer. Con
      esto, HAProxy puede morir en el MASTER y la VIP no se movera.
    success_msg: La bajada por sonda supera la diferencia de prioridades.
  run_once: true

- name: Directorio de configuracion
  ansible.builtin.file:
    path: "{{ keepalived_config_dir }}"
    state: directory
    mode: "0750"

- name: Configuracion
  # no_log: lleva auth_pass en claro.
  ansible.builtin.template:
    src: keepalived.conf.j2
    dest: "{{ keepalived_config_dir }}/keepalived.conf"
    mode: "0600"
  notify: Reiniciar keepalived
  no_log: true

- name: Compose
  ansible.builtin.template:
    src: docker-compose.yml.j2
    dest: "{{ keepalived_config_dir }}/docker-compose.yml"
    mode: "0644"
  notify: Reiniciar keepalived

- name: Levantar keepalived
  community.docker.docker_compose_v2:
    project_src: "{{ keepalived_config_dir }}"
    state: present
```

- [ ] **Paso 6: probar el assert de la bajada**

Antes, comprobar que `map('extract', hostvars, 'keepalived_priority')` **ve** el valor
en los dos hosts. `hostvars` incluye los defaults de un rol solo cuando ese host ya ha
entrado en la tanda; con `run_once` en la primera pasada puede no ser así. Si no lo ve,
la alternativa es exigir `keepalived_priority` explícito en los dos hosts (un assert de
presencia) en vez de leerlo con `default`.


Con dos hosts de prioridades 100 y 90 y `keepalived_health_weight: -5`, el assert tiene
que **fallar**. Con `-20`, pasar. Es el único de los tres asserts que codifica un
razonamiento y no una presencia, así que es el que hay que ver fallar.

- [ ] **Paso 7: renderizar y comprobar que la VIP y el `track_script` salen**

Mismo script de la Tarea 1. Comprobar que `virtual_ipaddress` lleva la VIP y que
`track_script { chk_haproxy }` está: sin él keepalived arranca, la sonda corre, y no
sirve para nada.

- [ ] **Paso 8: commit**

```bash
git add ansible/roles/keepalived/
git commit -m "feat(ansible): rol keepalived con la ip flotante"
```

---

## Tarea 3: Los tres cambios en los roles ya entregados

Van **juntos y en un solo commit**: el gRPC por el balanceador no funciona sin las SAN
nuevas, y el TLS terminado no es seguro sin los puertos atados. Por separado, cada uno
deja el despliegue peor que antes.

**Ficheros:**
- Modificar: `ansible/roles/internal_ca/{defaults,tasks}/main.yml`
- Modificar: `ansible/roles/dex/defaults/main.yml`,
  `ansible/roles/dex/templates/docker-compose.yml.j2`
- Modificar: `ansible/roles/dex_dashboard/defaults/main.yml`,
  `ansible/roles/dex_dashboard/templates/docker-compose.yml.j2`

**Interfaces:**
- Consume: `keepalived_vip` de la Tarea 2.
- Produce: `internal_ca_extra_sans`, `dex_http_bind`, `dex_grpc_bind`,
  `dex_dashboard_bind`, que la Tarea 4 pone en el inventario.

- [ ] **Paso 1: `internal_ca/defaults/main.yml`**

```yaml
# SAN adicionales para el certificado de cada host, sobre las dos que ya lleva
# (su propio hostname y su ansible_host). Existen por el passthrough del gRPC:
# con HAProxy en mode tcp, el panel marca el nombre del balanceador pero la
# sesion TLS se negocia contra el nodo de dex que toque, y Go verifica el
# certificado contra el nombre que marco. Sin el nombre del balanceador aqui,
# el panel no conecta.
#
# Formato de community.crypto: "DNS:nombre" o "IP:1.2.3.4".
internal_ca_extra_sans: []
```

- [ ] **Paso 2: `internal_ca/tasks/main.yml`, concatenar**

```yaml
- name: Peticion de certificado del host
  community.crypto.openssl_csr:
    path: "{{ internal_ca_dir }}/{{ inventory_hostname }}.csr"
    privatekey_path: "{{ internal_ca_dir }}/{{ inventory_hostname }}-key.pem"
    common_name: "{{ inventory_hostname }}"
    subject_alt_name: "{{ [
        'DNS:' ~ inventory_hostname,
        'IP:' ~ (ansible_host | default('127.0.0.1')),
      ] + internal_ca_extra_sans }}"
  delegate_to: localhost
```

- [ ] **Paso 3: probar que la re-emisión ocurre, que es lo que reinicia dex**

Contra un directorio de CA de usar y tirar en el scratchpad:

1. Emitir con `internal_ca_extra_sans: []`. Leer las SAN:
   `openssl x509 -noout -ext subjectAltName -in <cert>`.
2. Volver a ejecutar con `internal_ca_extra_sans: ["DNS:sso.interno"]`.
3. Comprobar que el certificado **cambia** y que la SAN nueva está.

Esto no es ceremonia: si `openssl_csr` no considera cambio la lista nueva, el
certificado no se reemite, el panel no conecta por el balanceador y el fallo aparece
mucho más tarde y en otro sitio. Anotar en el commit lo que salga, porque de ahí sale la
advertencia de documentación sobre el reinicio de todos los nodos.

- [ ] **Paso 4: los binds en `dex/defaults/main.yml`**

```yaml
# Interfaz por la que se publican los puertos del contenedor. El valor de
# fabrica no cambia nada de lo de hoy.
#
# Con el balanceador delante (fase 3) esto pasa a ser un REQUISITO y no un
# ajuste: HAProxy termina el TLS y habla en claro con estos puertos, y el
# trato de trustForwardedFor es que el balanceador sea el unico camino hasta
# el panel. Quien alcance el 5560 de un nodo directamente elige su propia
# X-Forwarded-For, y entonces cada peticion anonima estrena clave en un Valkey
# que corre con noeviction: llenarlo deja a los administradores sin poder
# crear sesiones. Un cortafuegos delante sirve igual; este rol no lo gestiona.
dex_http_bind: "0.0.0.0"
dex_grpc_bind: "0.0.0.0"
```

- [ ] **Paso 5: `dex/templates/docker-compose.yml.j2`**

```yaml
        ports:
            - "{{ dex_http_bind }}:{{ dex_http_port }}:5556"
            - "{{ dex_grpc_bind }}:{{ dex_grpc_port }}:5557"
            - "127.0.0.1:{{ dex_telemetry_port }}:5558"
```

- [ ] **Paso 6: lo mismo en `dex_dashboard`**

`dex_dashboard_bind: "0.0.0.0"` con un comentario que apunte al del rol `dex`, y
`- "{{ dex_dashboard_bind }}:{{ dex_dashboard_listen_port }}:5560"`.

- [ ] **Paso 7: renderizar los dos compose y comprobar que siguen siendo YAML válido**

Con el valor de fábrica, la línea tiene que salir idéntica a la de antes salvo el
`0.0.0.0:` delante. Con `dex_http_bind: 192.0.2.21`, tiene que salir esa dirección.
Docker acepta las dos formas; lo que se comprueba aquí es que la plantilla no genera
`::5556` cuando el bind está vacío — si alguien pone `dex_http_bind: ""`, el resultado
es un puerto publicado en todas las interfaces sin decirlo. Decidir y dejarlo escrito:
o el default no admite vacío, o el vacío se trata como `0.0.0.0` explícitamente.

- [ ] **Paso 8: `pre-commit` y commit**

```bash
git add ansible/roles/internal_ca/ ansible/roles/dex/ ansible/roles/dex_dashboard/
git commit -m "feat(ansible): san adicionales y binds por interfaz"
```

---

## Tarea 4: El inventario y el playbook

**Ficheros:**
- Modificar: `ansible/playbooks/dex.yml`
- Modificar: `ansible/inventories/ejemplo/hosts.yml`,
  `ansible/inventories/ejemplo/group_vars/all.yml`

- [ ] **Paso 1: la tanda nueva en `playbooks/dex.yml`**

El grupo `loadbalancer` entra en la tanda de la CA interna (sus hosts pueden necesitar
certificado propio) y **no** en la de Valkey.

```yaml
- name: CA interna
  hosts: dex:dex_dashboard:valkey:loadbalancer
  roles: [internal_ca]

# ... valkey, dex, panel sin cambios ...

- name: Balanceador
  # Al final a proposito: HAProxy sondea /healthz de los dos servicios, y con
  # backends que todavia no existen arrancaria con todo en DOWN. Se
  # recuperaria solo, pero un despliegue que empieza con todo caido no se
  # distingue de uno roto.
  #
  # Que el issuer no resuelva mientras tanto no importa: el panel reintenta el
  # discovery (newAuthenticatorWithRetry en cmd/dex-dashboard/main.go), que es
  # justo el caso para el que se escribio.
  hosts: loadbalancer
  serial: 1        # de uno en uno: la VIP se queda en el otro mientras tanto
  become: true
  roles: [haproxy, keepalived]
```

- [ ] **Paso 2: el grupo en `inventories/ejemplo/hosts.yml`**

```yaml
    loadbalancer:
      hosts:
        lb-1:
          ansible_host: 192.0.2.31
          keepalived_priority: 110      # el MASTER de partida
        lb-2:
          ansible_host: 192.0.2.32
          keepalived_priority: 100
      vars:
        keepalived_vip: 192.0.2.30
        keepalived_interface: eth0
        # Explicito a proposito: dos despliegues en la misma L2 con el mismo
        # id se pelean por la misma VIP.
        keepalived_router_id: 51
```

- [ ] **Paso 3: las SAN y el gRPC en el grupo `dex`**

```yaml
    dex:
      vars:
        # El passthrough del gRPC hace que el panel marque el nombre del
        # balanceador; sin estas dos SAN, Go rechaza el certificado del nodo
        # por nombre y el panel no conecta.
        internal_ca_extra_sans:
          - "DNS:sso.interno"
          - "IP:192.0.2.30"
        # Atar los puertos a la interfaz privada. Ver el comentario de
        # dex_http_bind en los defaults del rol: con el TLS terminado en el
        # balanceador esto es un requisito, no un ajuste.
        dex_http_bind: "{{ ansible_host }}"
        dex_grpc_bind: "{{ ansible_host }}"
```

Y en `dex_dashboard`:

```yaml
        dex_dashboard_bind: "{{ ansible_host }}"
        # Por el balanceador, no por el dex de esta misma maquina: si ese dex
        # cae, este panel quedaria inutil y su /healthz seguiria devolviendo
        # 200, asi que el balanceador le seguiria mandando administradores.
        dex_dashboard_grpc_address: "sso.interno:5557"
```

- [ ] **Paso 4: `group_vars/all.yml`**

```yaml
# El certificado publico del sitio, para el TLS que HAProxy termina. Va aqui
# porque este fichero se cifra entero con ansible-vault; la clave privada no
# puede vivir en hosts.yml, que va en claro.
haproxy_tls_cert: |
  -----BEGIN CERTIFICATE-----
  cambiame
  -----END CERTIFICATE-----
haproxy_tls_key: |
  -----BEGIN PRIVATE KEY-----
  cambiame
  -----END PRIVATE KEY-----
# VRRP la trunca a 8 caracteres: los demas no cuentan.
keepalived_auth_pass: cambiam
```

- [ ] **Paso 5: `ansible-lint` en perfil de producción sobre todo `ansible/`**

```bash
ansible-lint --profile production ansible/
```

- [ ] **Paso 6: commit**

```bash
git add ansible/playbooks/ ansible/inventories/
git commit -m "feat(ansible): grupo loadbalancer y la tanda del balanceador"
```

---

## Tarea 5: Las seis pruebas en vivo

Es la tarea que decide si esto funciona. Converge local con un HAProxy de verdad
delante de los dos dex y los dos paneles que ya levanta `Ejemplos/dashboard`… **no**:
esos contenedores no son nuestros. Se levanta un stack propio, con prefijo `t13-`, y se
retira al terminar.

- [ ] **Paso 1: montar el stack de prueba**

Un `docker-compose` en el scratchpad con: dos dex (`t13-dex-1`, `t13-dex-2`), dos
paneles (`t13-panel-1`, `t13-panel-2`), un Valkey (`t13-valkey`) y un HAProxy
(`t13-haproxy`) con el `haproxy.cfg` renderizado por la plantilla del rol. Imagen
`dex-con-panel:local`, que ya existe en la máquina.

Certificado del sitio: uno autofirmado de usar y tirar, generado con `openssl` en el
scratchpad.

- [ ] **Paso 2: prueba 1 — el fichero parsea y HAProxy arranca**

`docker logs t13-haproxy` sin errores y los cuatro backends en estado conocido.

- [ ] **Paso 3: prueba 2 — el orden de las rutas**

```bash
curl -sk https://127.0.0.1:8443/dex-dashboard/ -o /dev/null -w '%{http_code}\n'
curl -sk https://127.0.0.1:8443/dex/healthz    -o /dev/null -w '%{http_code}\n'
```

La primera tiene que llegar al **panel** y la segunda a **dex**. Se comprueba en los
logs de los contenedores, no por el código de estado: los dos pueden devolver 200.

**Sabotaje obligatorio:** añadir a mano la regla que el comentario de la plantilla
desaconseja —`use_backend be_dex if { path_beg /dex }`— **delante** de la del panel,
recargar, y comprobar que `/dex-dashboard/` acaba en dex. Es el fallo concreto que el
comentario previene, y verlo una vez es lo que justifica que el comentario siga ahí.

- [ ] **Paso 4: prueba 3 — `X-Forwarded-For` con `set`**

```bash
curl -sk -H 'X-Forwarded-For: 1.2.3.4' https://127.0.0.1:8443/dex-dashboard/ -o /dev/null
```

En el log del panel tiene que aparecer la dirección **real**, no `1.2.3.4`. Sabotaje:
cambiar `set-header` por `add-header` y comprobar que entonces sí aparece `1.2.3.4`.

- [ ] **Paso 5: prueba 4 — sin afinidad, y el estado compartido lo sostiene**

Iniciar sesión en el panel a través del balanceador y hacer varias peticiones con la
misma cookie. Comprobar en los logs que **las atienden los dos paneles** y que todas
funcionan. Esto prueba a la vez que no hay afinidad y que las sesiones compartidas en
Valkey hacen su trabajo.

- [ ] **Paso 6: prueba 5 — el passthrough del gRPC y las SAN**

El panel apunta a `t13-haproxy:5557`. Listar clientes desde el panel tiene que
funcionar.

**Sabotaje obligatorio:** reemitir el certificado de los dex **sin** la SAN del
balanceador y comprobar que el panel falla **por nombre de certificado**
(`x509: certificate is valid for t13-dex-1, not ...`) y no por otra cosa. Es la trampa
de esta fase y hay que verla morder.

- [ ] **Paso 7: prueba 6 — la sonda saca un nodo**

`docker stop t13-dex-1`, esperar, y comprobar que HAProxy lo marca `DOWN` y que todo el
tráfico va al otro. Levantarlo y ver que vuelve.

- [ ] **Paso 8: retirar el stack**

```bash
docker compose -f <scratchpad>/t13-compose.yml down -v
```

Comprobar con `docker ps -a` que no queda nada con prefijo `t13-` y que los contenedores
de `Ejemplos/dashboard` siguen en pie.

- [ ] **Paso 9: commit de lo que las pruebas hayan corregido**

Si alguna prueba obligó a cambiar una plantilla, va en su propio commit con lo que
destapó. Si no cambió nada, no hay commit: las pruebas viven en la transcripción y en la
documentación, no en el árbol.

---

## Tarea 6: Documentación

**Ficheros:** `documentacion/despliegue-ansible.md`, `CHANGELOG.md`, `TODO.md`,
`DONE.md`, la hoja de ruta.

- [ ] **Paso 1: `despliegue-ansible.md`**

- La sección «Qué tiene que hacer el balanceador» deja de describir algo pendiente y
  pasa a describir el rol, conservando la lista de obligaciones como está.
- **Nuevo:** el requisito de atar los puertos, con el porqué entero.
- **Nuevo:** la condición de que la red entre el balanceador y los nodos sea de
  confianza, porque va en claro.
- **Nuevo:** el aviso de que aplicar las SAN nuevas **reemite el certificado de todos
  los nodos de dex y los reinicia**, de uno en uno.
- **Nuevo:** los cuatro pasos de VRRP en la lista ejecutable de verificación manual,
  junto a los del failover de sentinel:
  1. La VIP está en el MASTER y responde.
  2. Parar HAProxy en el MASTER mueve la VIP al BACKUP en menos de
     `interval × fall` segundos.
  3. Una sesión abierta en el panel **sobrevive al salto**.
  4. Levantar el primero recupera la VIP, o no según `nopreempt`; se anota lo que salga.

- [ ] **Paso 2: la hoja de ruta**

Fase 3 → «Entregada».

- [ ] **Paso 3: `CHANGELOG.md`**

Sección `[Unreleased]` nueva, con el balanceador en «Añadido» y el cambio de
`dex_dashboard_grpc_address` del inventario de ejemplo en «Cambiado».

- [ ] **Paso 4: `TODO.md` → `DONE.md`**

La entrada «Fases 3, 4 y 5» pierde la fase 3. Sección nueva en `DONE.md` con **lo que
destapó probarlo** — esa es la parte que vale, y sale de los sabotajes de la Tarea 5.

- [ ] **Paso 5: commit**

```bash
git add documentacion/ CHANGELOG.md TODO.md DONE.md
git commit -m "docs: cerrar la fase 3, el balanceador"
```

---

## Autorrevisión

**Cobertura de la spec.** §3.1 → Tareas 1 y 6. §3.2 → Tarea 1 paso 3 (`fe_grpc`) y
Tarea 5 paso 6. §3.3 → Tarea 4 paso 3. §3.4 → Tarea 3 pasos 1-3 y Tarea 5 paso 6
(sabotaje). §3.5 → Tarea 1 paso 3 y Tarea 5 paso 5. §4 → Tarea 1. §5 → Tarea 2. §6 →
Tarea 3. §7 → Tarea 4 paso 1. §8 → Tarea 5. §9 → Tarea 6. Sin huecos.

**Cosas que este plan deja decididas y no debe redecidir el ejecutor:**

- El `haproxy_dex_health_path` sale del issuer. Fijarlo a `/dex/healthz` funciona en el
  inventario de ejemplo y rompe en cualquier otro.
- El gRPC **no** se termina. Si alguien encuentra cómodo terminarlo, es un cambio de
  spec, no de plan.
- Los tres cambios de la Tarea 3 van en un solo commit.

**Lo que este plan sabe que no puede probar:** VRRP. Va a la lista manual. No inventar
una prueba local que dé verde sin probar nada — dos contenedores con `NET_ADMIN` en la
misma máquina comparten pila de red.
