# Colección `rasty94.dex`

Despliegue en alta disponibilidad del fork [`rasty94/dex`](https://github.com/rasty94/dex):
dex, su panel de administración, Valkey, MariaDB y el balanceador. Esta colección es
autosuficiente: no hace falta el repositorio completo para instalarla ni para
ejecutarla.

## Qué despliega

Ocho roles, en siete tandas, en este orden:

1. **CA interna** (`internal_ca`) — emite la CA propia del despliegue y el certificado
   TLS de cada nodo.
2. **MariaDB** (`mariadb`) — despliega el servidor de base de datos con replicación
   maestro/réplica entre los dos nodos del grupo `mariadb`.
3. **Orchestrator** (`orchestrator`) — vigila el par de MariaDB y promueve al
   superviviente cuando el primario falla, con un tercer voto para el quorum.
4. **Valkey** (`valkey`) — despliega Valkey en la topología elegida (`standalone`,
   `sentinel` o `cluster`).
5. **Dex** (`dex`) — despliega el propio servidor dex (OIDC), contenedorizado.
6. **Panel de administración** (`dex_dashboard`) — despliega el panel web de dex.
7. **Balanceador** (`haproxy` + `keepalived`) — reparte el tráfico HTTPS hacia dex y
   el panel, hacia el nodo de MariaDB que la sonda declara primario escribible, y
   mantiene la VIP entre los dos nodos del balanceador.

## Instalar

**Desde git**, fijada por etiqueta, en un `requirements.yml`:

```yaml
# requirements.yml
collections:
  - name: https://github.com/rasty94/dex.git#ansible/
    type: git
    version: fork-v2.2.0
```

```bash
ansible-galaxy collection install -r requirements.yml
```

**Desde el tarball** adjunto a la release de GitHub:

```bash
ansible-galaxy collection install rasty94-dex-2.2.0.tar.gz
```

## Requisitos

- `ansible-core >= 2.15`.
- Docker en cada nodo gestionado.
- Las colecciones `community.docker` y `community.crypto`: se instalan solas junto con
  esta colección, por sus `dependencies` en `galaxy.yml`.

## El inventario

El inventario de ejemplo viene dentro de la colección instalada, en
`~/.ansible/collections/ansible_collections/rasty94/dex/inventories/ejemplo/`.

| Grupo | Qué hosts van | Variables obligatorias |
| --- | --- | --- |
| `dex` | los nodos que corren el servidor dex | `dex_issuer` (la URL del balanceador), `dex_storage` (tipo, host, puerto, base, usuario, `ssl_mode`) |
| `dex_dashboard` | los nodos que corren el panel (normalmente los mismos que `dex`) | `dex_dashboard_base_url` (obligatoria); `dex_dashboard_admin_emails`/`dex_dashboard_admin_groups` recomendadas — el default es `[]`, y sin ninguna de las dos el panel se despliega sin ningún administrador |
| `valkey` | las máquinas con un `valkey-server` propio | `valkey_topology` (`standalone`, `sentinel` o `cluster`); `valkey_master_set` en `sentinel` |
| `valkey_sentinel` | los votantes del quorum de sentinel (puede incluir un host del grupo `dex`) | — solo pertenencia al grupo |
| `mariadb` | los nodos de base de datos | `mariadb_server_id` (identidad de replicación, un entero distinto por nodo), `mariadb_port` y `mariadb_probe_port` (los lee el rol `haproxy` desde otra tanda) |
| `orchestrator` | los votantes del quorum de failover: los nodos de `mariadb` más un tercero que no es de base de datos | — solo pertenencia al grupo |
| `loadbalancer` | los nodos del balanceador | `keepalived_priority` por host (decide quién arranca de `MASTER`) |

Además, `internal_ca_extra_sans` en los grupos `dex` y `mariadb` cuando el balanceador
tiene nombre propio (`dex_issuer`/`dex_storage.host` distintos del nombre de cada nodo):
sin esas SAN adicionales, Go rechaza el certificado por nombre y el servicio no arranca.

## Ejecutar

**Copia el inventario de ejemplo fuera de la colección antes de tocarlo.** Vive
instalado dentro de `~/.ansible/collections/ansible_collections/rasty94/dex/inventories/ejemplo/`,
y ese directorio se sobrescribe en la próxima actualización de la colección. Además,
`internal_ca_dir` se ancla junto al inventario (`-i` que uses): apuntar `-i` al que
viaja dentro de la colección instalada dejaría la CA del despliegue mezclada con las
colecciones y borrada con ellas, justo lo que sacar `internal_ca_dir` del repositorio
quería evitar.

```bash
cp -r ~/.ansible/collections/ansible_collections/rasty94/dex/inventories/ejemplo ./mi-inventario
ansible-playbook rasty94.dex.site -i mi-inventario/hosts.yml
```

La CA interna del despliegue se crea en `<directorio del inventario>/.internal-ca/` la
primera vez que se ejecuta. Hay que guardarla: es la que firma todos los certificados
del despliegue, y perderla obliga a reemitirlos todos. `internal_ca_dir` exige un
inventario de verdad —un fichero o directorio pasado a `-i`—: con una lista de hosts
suelta (`-i host1,host2,`) el rol se niega con un `assert` explícito, y hay que fijar
`internal_ca_dir` a mano en ese caso.

## Documentación completa

La lista de verificación manual y los huecos operativos del despliegue están en
[`documentacion/despliegue-ansible.md`](https://github.com/rasty94/dex/blob/master/documentacion/despliegue-ansible.md),
en el repositorio del fork.
