# Fase 5 — La colección de Ansible: plan de implementación

> **Para quien lo ejecute:** SUB-SKILL OBLIGATORIA: usar
> superpowers:subagent-driven-development (recomendada) o
> superpowers:executing-plans, tarea a tarea. Los pasos llevan casilla (`- [ ]`).

**Objetivo:** que `ansible/` sea la colección `rasty94.dex`, instalable desde este
repositorio por git y fijada por etiqueta, con el checkout funcionando igual que hoy.

**Arquitectura:** `ansible/galaxy.yml` convierte el directorio en colección sin mover
nada. Un enlace simbólico versionado en `ansible/.collections/ansible_collections/rasty94/dex → ../../..`
más `collections_path` en `ansible.cfg` hacen que el checkout resuelva `rasty94.dex.*`
sin instalar. El playbook pasa a `playbooks/site.yml` con roles por FQCN y se invoca
como `rasty94.dex.site` en los dos casos. Un solo cambio de comportamiento:
`internal_ca_dir` se ancla en `inventory_dir`. CI construye, instala en limpio y
comprueba la sintaxis del playbook con el inventario de ejemplo.

**Tecnologías:** ansible-core 2.21.3, ansible-lint 26.8.0, `ansible-galaxy collection
build/install`, GitHub Actions.

**Spec:** [2026-09-07-coleccion-ansible.md](2026-09-07-coleccion-ansible.md)

## Restricciones globales

- **Documentación en español con tildes; comentarios de Ansible y YAML en español sin
  tildes (la ñ sí)**, como los ocho roles.
- **Commits en español, sin tildes** (la ñ sí), prefijo convencional, asunto en minúsculas
  y sin punto. **Sin atribución de ninguna herramienta.**
- **Nada de `git add -A`.** `references/`, `.converge/`, `.internal-ca/` y el binario de
  `cmd/dex-dashboard` no se versionan.
- **`pre-commit run --files <cambiados>`** antes de cada commit.
- **Ningún rol cambia de comportamiento** salvo `internal_ca_dir` (tarea 3). Un diff en
  `ansible/roles/` fuera de esa tarea es un error.
- **Toda invocación de `ansible*` en esta máquina va con `> fichero 2>&1 < /dev/null`**:
  sin el `< /dev/null` falla con «Ansible requires blocking IO».
- **Ansible es el del repositorio**: `.venv/bin/ansible-playbook`, `.venv/bin/ansible-galaxy`,
  `.venv/bin/ansible-lint`. Con `ANSIBLE_COLLECTIONS_PATH` apuntando solo a
  `ansible/.collections` cuando se quiera demostrar que el checkout resuelve sin nada
  instalado; a `~/.ansible/collections` además para que `community.*` se encuentre.
- **`ansible-lint` se ejecuta desde la raíz del repositorio** (`ansible-lint ansible/`),
  nunca desde `ansible/`: comprobado en el spike que desde dentro no ve `ansible.cfg`, no
  resuelve el FQCN y cambia los hallazgos.
- **Nada fuera del worktree** salvo el scratchpad. Sin contenedores en esta fase: no hay
  ninguno que levantar.
- **Cada test nuevo se sabotea una vez.**

---

## Lo que ya se verificó en el spike, y el plan da por hecho

Un repositorio mínimo con la misma forma, en el scratchpad, con el mismo `ansible-core`:

- **El enlace resuelve.** `ANSIBLE_CONFIG=ansible.cfg ansible-playbook rasty94.dex.site -i …`
  desde la raíz resuelve el playbook de la colección y el rol por FQCN. Sin el enlace:
  `[ERROR]: the playbook: rasty94.dex.site could not be found`.
- **`build_ignore: [.collections, inventories/local, "inventories/*/.internal-ca"]`**
  funciona tal cual: el tarball no lleva nada de eso y sí `roles/`, `playbooks/site.yml`,
  `inventories/ejemplo`, `meta/runtime.yml`.
- **Las tres formas del fragmento git funcionan igual**: `#ansible/,ref`, `#/ansible/,ref`
  y `#ansible,ref`. Y `requirements.yml` con `type: git` y `version: fork-vX.Y.Z`. El árbol
  instalado ya sale filtrado por `build_ignore`.
- **`ansible-galaxy collection install` resuelve las dependencias contra todas las rutas
  configuradas**, no solo `-p`: si `community.crypto` ya está en `~/.ansible/collections`,
  no la copia («already installed, skipping»). Un `-p` limpio no es un entorno limpio.
- **`ansible-lint` con `galaxy.yml` mínimo salta con `galaxy[tags]` y
  `galaxy[no-repository]`**: `tags` y `repository` son obligatorios en la práctica.
  Quitar `CHANGELOG.md` añade `galaxy[no-changelog]`.
- **Con la colección instalada, `inventory_dir` es el directorio del `-i`** y
  `playbook_dir` cae dentro de `…/ansible_collections/rasty94/dex/playbooks`. Es la
  razón de la tarea 3.
- **`--syntax-check` no renderiza plantillas.** Comprueba estructura, YAML y que cada rol
  y playbook por FQCN existan. Renderizar el inventario de ejemplo sigue siendo
  `--check --diff` con `-e ansible_connection=local`, que en CI exigiría Docker y
  `community.docker` en modo comprobación: se queda como paso local (tarea 4, paso 6) y la
  spec se corrige en ese punto.

---

## Estructura de ficheros

**Se crean:**

| Fichero | Responsabilidad |
| --- | --- |
| `ansible/galaxy.yml` | Identidad, versión, dependencias, `build_ignore` |
| `ansible/meta/runtime.yml` | `requires_ansible` |
| `ansible/README.md` | Instalar y ejecutar sin el repositorio |
| `ansible/CHANGELOG.md` | Cambios de la colección por versión |
| `ansible/.collections/ansible_collections/rasty94/dex` | Enlace simbólico `→ ../../..` |
| `ansible/playbooks/site.yml` | El playbook, con FQCN (renombrado desde `dex.yml`) |

**Se modifican:**

| Fichero | Cambio |
| --- | --- |
| `ansible.cfg` | `collections_path` en vez de `roles_path` |
| `.gitignore` | `.internal-ca/` a cualquier profundidad |
| `ansible/inventories/local/hosts.yml` | `inventory_dir` en vez de `playbook_dir` y de rutas absolutas |
| `ansible/roles/internal_ca/defaults/main.yml` | `internal_ca_dir` |
| `.github/workflows/release.yaml` | Trabajo `coleccion` |
| `.pre-commit-config.yaml` | `rev` del hook de ansible-lint |
| `documentacion/despliegue-ansible.md`, hoja de ruta, `CHANGELOG.md`, `TODO.md`, `DONE.md` | Tarea 6 |

---

## Tarea 1: La colección existe y se construye

**Ficheros:**

- Crear: `ansible/galaxy.yml`, `ansible/meta/runtime.yml`, `ansible/README.md`,
  `ansible/CHANGELOG.md`, `ansible/.collections/ansible_collections/rasty94/dex` (enlace)
- Modificar: `ansible.cfg`, `.gitignore`

**Interfaces:**

- Produce: el nombre `rasty94.dex` resoluble desde el checkout; `ansible-galaxy
  collection build ansible/` funcionando. Todo lo demás depende de esto.

- [ ] **Paso 1: `galaxy.yml`**

```yaml
---
# La identidad de la coleccion. namespace y name forman el FQCN de cada rol
# (rasty94.dex.mariadb). version es la MISMA cifra que la etiqueta fork-vX.Y.Z
# del repositorio, sin el prefijo: una sola cadena de versiones para el
# binario, la imagen Docker y la coleccion, y release.yaml falla si no
# coinciden. La primera version con coleccion es la SIGUIENTE etiqueta.
namespace: rasty94
name: dex
version: 2.2.0
readme: README.md
authors:
  - rasty94
description: >-
  Despliegue en alta disponibilidad del fork rasty94/dex: dex, su panel de
  administracion, Valkey, MariaDB y el balanceador.
license:
  - Apache-2.0
tags:
  - dex
  - oidc
  - docker
  - mariadb
  - valkey
  - haproxy
repository: https://github.com/rasty94/dex
documentation: https://github.com/rasty94/dex/blob/master/documentacion/despliegue-ansible.md
issues: https://github.com/rasty94/dex/issues
dependencies:
  community.docker: ">=5.2.1"
  community.crypto: ">=3.3.0"
# Lo que NO entra en el tarball. .collections es el enlace que hace que el
# checkout se resuelva como la coleccion: dentro del paquete apuntaria a un
# ../../.. que no existe. inventories/local es el converge de desarrollo.
# .internal-ca es la CA de cada despliegue (ver internal_ca_dir): privada.
build_ignore:
  - .collections
  - inventories/local
  - "inventories/*/.internal-ca"
  - .ansible-lint
```

`tags` y `repository` no son adorno: sin ellos `ansible-lint` en perfil `production`
falla con `galaxy[tags]` y `galaxy[no-repository]` (comprobado).

- [ ] **Paso 2: `meta/runtime.yml`**

```yaml
---
# 2.15 es lo mas antiguo que se ha considerado: los playbooks de coleccion
# (rasty94.dex.site) existen desde 2.11, ignore_unreachable desde 2.7 y el
# filtro split desde 2.11. Desarrollado y probado con 2.21.3.
requires_ansible: ">=2.15.0"
```

- [ ] **Paso 3: `README.md` de la colección**

En español, con tildes, autosuficiente para quien no tiene el repositorio. Secciones,
en este orden y con este contenido:

1. **Qué despliega** — una frase por rol, los ocho, y el orden de las tandas
   (`CA interna → MariaDB → orchestrator → Valkey → dex → panel → balanceador`).
2. **Instalar** — las dos formas:
   ```yaml
   # requirements.yml
   collections:
     - name: https://github.com/rasty94/dex.git#ansible/
       type: git
       version: fork-v2.2.0
   ```
   y el tarball adjunto a la release de GitHub:
   `ansible-galaxy collection install rasty94-dex-2.2.0.tar.gz`.
3. **Requisitos** — `ansible-core >= 2.15`, Docker en cada nodo gestionado,
   `community.docker` y `community.crypto` (se instalan con la colección).
4. **El inventario** — tabla con los siete grupos (`dex`, `dex_dashboard`, `valkey`,
   `valkey_sentinel`, `mariadb`, `orchestrator`, `loadbalancer`), qué hosts van en cada
   uno y las variables obligatorias de cada uno (las que un `assert` exige:
   `mariadb_server_id`, `keepalived_priority`, `mariadb_port`/`mariadb_probe_port`
   declarados, `internal_ca_extra_sans`). El de ejemplo viene dentro:
   `~/.ansible/collections/ansible_collections/rasty94/dex/inventories/ejemplo/`.
5. **Ejecutar** — `ansible-playbook rasty94.dex.site -i inventario/hosts.yml`, y que la
   CA interna se crea en `<directorio del inventario>/.internal-ca/` y hay que guardarla.
6. **Documentación completa** — enlace a `documentacion/despliegue-ansible.md` en GitHub,
   con la frase de que la lista de verificación manual y los huecos operativos están allí.

- [ ] **Paso 4: `CHANGELOG.md` de la colección**

```markdown
# Registro de cambios de la colección `rasty94.dex`

Solo lo que cambia en la colección. El registro del fork entero está en la raíz del
repositorio.

## [2.2.0] — sin publicar

Primera versión como colección. Los ocho roles (`internal_ca`, `valkey`, `dex`,
`dex_dashboard`, `haproxy`, `keepalived`, `mariadb`, `orchestrator`) y el playbook
`site.yml`, tal como se entregaron en las fases 1 a 4 del despliegue.

### Cambiado

- **`internal_ca_dir` se ancla en el inventario** (`{{ inventory_dir }}/.internal-ca`)
  y ya no en el repositorio. La CA es del despliegue, no del código: instalada la
  colección, el ancla anterior la mandaba a `~/.ansible/collections`. **Migración:**
  quien desplegara desde el checkout con `.internal-ca/` en la raíz del repositorio
  tiene que moverlo junto a su inventario antes de volver a desplegar; si no, el rol
  emite una CA nueva y reemite todos los certificados.
```

- [ ] **Paso 5: El enlace y `ansible.cfg`**

```bash
mkdir -p ansible/.collections/ansible_collections/rasty94
ln -s ../../.. ansible/.collections/ansible_collections/rasty94/dex
ls -la ansible/.collections/ansible_collections/rasty94/   # dex -> ../../..
readlink -f ansible/.collections/ansible_collections/rasty94/dex   # .../dex/ansible
```

`ansible.cfg` pasa a:

```ini
[defaults]
# El checkout se resuelve como la coleccion rasty94.dex sin instalar nada:
# ansible/.collections lleva un enlace ansible_collections/rasty94/dex que
# apunta al propio ansible/. ~/.ansible/collections va detras para que
# community.docker y community.crypto se encuentren. roles_path desaparece:
# los roles se nombran por FQCN y solo por FQCN.
collections_path = ansible/.collections:~/.ansible/collections
```

- [ ] **Paso 6: `.gitignore`**

Bajo el comentario existente de la línea 44:

```gitignore
# converge local escribe su propia configuracion desechable en el worktree.
/.internal-ca/
/.converge/
# La CA de cada despliegue vive junto a su inventario (internal_ca_dir) y es
# privada: a cualquier profundidad, no solo en la raiz.
.internal-ca/
```

- [ ] **Paso 7: Se construye, y el tarball lleva lo que debe**

```bash
rm -rf /tmp/t15 && mkdir -p /tmp/t15/build
.venv/bin/ansible-galaxy collection build ansible/ --output-path /tmp/t15/build \
    > /tmp/t15/build.log 2>&1 < /dev/null; tail -2 /tmp/t15/build.log
tar tzf /tmp/t15/build/rasty94-dex-2.2.0.tar.gz | sort > /tmp/t15/tar.txt
grep -c '^roles/' /tmp/t15/tar.txt                      # > 0
grep -E '^(playbooks/dex.yml|meta/runtime.yml|inventories/ejemplo/hosts.yml|README.md|CHANGELOG.md)$' /tmp/t15/tar.txt   # los cinco
grep -E '\.collections|inventories/local|\.internal-ca|\.ansible-lint' /tmp/t15/tar.txt || echo "excluidos: bien"
```

(El playbook aún se llama `dex.yml` en esta tarea; lo renombra la 2.)

- [ ] **Paso 8: El checkout resuelve el nombre**

```bash
ANSIBLE_COLLECTIONS_PATH=ansible/.collections .venv/bin/ansible-galaxy collection list \
    > /tmp/t15/list.log 2>&1 < /dev/null; grep -A2 'ansible/.collections' /tmp/t15/list.log
```

Esperado: `rasty94.dex  2.2.0` bajo `…/ansible/.collections/ansible_collections`.

**Sabotaje:** `mv ansible/.collections/ansible_collections/rasty94/dex{,.off}`, repetir:
la lista tiene que salir sin `rasty94.dex`. Restaurar.

- [ ] **Paso 9: Lint y commit**

```bash
pre-commit run --files ansible/galaxy.yml ansible/meta/runtime.yml ansible/README.md \
    ansible/CHANGELOG.md ansible.cfg .gitignore
git add ansible/galaxy.yml ansible/meta ansible/README.md ansible/CHANGELOG.md \
    ansible/.collections ansible.cfg .gitignore
git commit -m "feat(ansible): ansible/ es la coleccion rasty94.dex"
```

`ansible-lint` puede sacar hallazgos nuevos por las reglas `galaxy[*]`: se arreglan aquí,
no se silencian. Si el hook falla por otra cosa que no sea de esta tarea, se anota en el
informe y se sigue.

---

## Tarea 2: El playbook por FQCN, y el inventario local sin rutas del repositorio

**Ficheros:**

- Crear (renombrando): `ansible/playbooks/site.yml` desde `ansible/playbooks/dex.yml`
- Modificar: `ansible/inventories/local/hosts.yml`

**Interfaces:**

- Consume: `rasty94.dex` resoluble (tarea 1).
- Produce: `rasty94.dex.site`, el nombre por el que se invoca el playbook en los dos
  caminos. Las tareas 4 y 6 lo usan.

- [ ] **Paso 1: Renombrar y poner FQCN**

```bash
git mv ansible/playbooks/dex.yml ansible/playbooks/site.yml
```

Las siete líneas `roles:` (hoy 15, 27, 40, 58, 64, 70 y 88) pasan a:

```yaml
  roles: [rasty94.dex.internal_ca]
  roles: [rasty94.dex.mariadb]
  roles: [rasty94.dex.orchestrator]
  roles: [rasty94.dex.valkey]
  roles: [rasty94.dex.dex]
  roles: [rasty94.dex.dex_dashboard]
  roles: [rasty94.dex.haproxy, rasty94.dex.keepalived]
```

Los comentarios del playbook no cambian. Encima de la primera tanda, un comentario nuevo:

```yaml
# Los roles van por FQCN (rasty94.dex.<rol>) y solo por FQCN: es lo que hace
# que este playbook sea el mismo desde el checkout (ansible.cfg resuelve la
# coleccion al propio arbol) y con la coleccion instalada. Se invoca como
# ansible-playbook rasty94.dex.site -i <inventario>.
```

- [ ] **Paso 2: El inventario local**

Las seis líneas `ansible_python_interpreter: "{{ playbook_dir }}/../../.venv/bin/python3"`
(29, 34, 44, 55, 60, 65) pasan a `"{{ inventory_dir }}/../../../.venv/bin/python3"`.
Las tres de `*_config_dir` con `playbook_dir` (75, 89, 107) pasan a
`"{{ inventory_dir }}/../../../.converge/<lo mismo>"`. Las ocho rutas absolutas
`/Users/antoniorodriguez/GITHUB-RASTY/dex/.converge/…` (133, 140, 171, 190, 195, 200)
pasan a `"{{ inventory_dir }}/../../../.converge/…"`, y los dos comentarios que las
justificaban («en ruta absoluta y NO via playbook_dir…», líneas 122-126 y 163-166) se
sustituyen por uno solo, arriba del fichero, junto al que ya explica el inventario:

```yaml
# Las rutas del repositorio van por inventory_dir y NO por playbook_dir: el
# playbook vive dentro de la coleccion, y con la coleccion instalada
# playbook_dir cae en ~/.ansible/collections. inventory_dir es este fichero,
# que si sabe donde esta el repositorio. Tres niveles arriba es la raiz.
```

- [ ] **Paso 3: Comprobar que los dos inventarios pasan por el nombre nuevo**

```bash
for inv in local ejemplo; do
  ANSIBLE_COLLECTIONS_PATH=ansible/.collections:~/.ansible/collections \
  .venv/bin/ansible-playbook rasty94.dex.site -i ansible/inventories/$inv/hosts.yml \
      --syntax-check > /tmp/t15/syntax-$inv.log 2>&1 < /dev/null; echo "$inv exit=$?"; tail -1 /tmp/t15/syntax-$inv.log
done
```

Esperado: `exit=0` los dos y `playbook: …/ansible/playbooks/site.yml` (resuelto a
través del enlace).

**Sabotaje:** dejar una tanda con `roles: [mariadb]` a secas → `ERROR! the role 'mariadb'
was not found`. Restaurar.

- [ ] **Paso 4: Que `inventory_dir` da la raíz**

```bash
ANSIBLE_COLLECTIONS_PATH=ansible/.collections:~/.ansible/collections \
.venv/bin/ansible localhost -i ansible/inventories/local/hosts.yml \
    -m debug -a "msg={{ inventory_dir }}/../../../.venv/bin/python3" \
    > /tmp/t15/invdir.log 2>&1 < /dev/null; grep msg /tmp/t15/invdir.log
ls "$(grep -o '"msg": "[^"]*' /tmp/t15/invdir.log | cut -d'"' -f4)"   # existe
```

- [ ] **Paso 5: Lint y commit**

```bash
pre-commit run --files ansible/playbooks/site.yml ansible/inventories/local/hosts.yml
git add ansible/playbooks ansible/inventories/local/hosts.yml
git commit -m "feat(ansible): el playbook es rasty94.dex.site, con los roles por fqcn"
```

---

## Tarea 3: `internal_ca_dir` se ancla en el inventario

El único cambio de comportamiento en un rol entregado. Tiene su propia tarea para que
un revisor pueda rechazarla sola.

**Ficheros:**

- Modificar: `ansible/roles/internal_ca/defaults/main.yml:1-4`

- [ ] **Paso 1: El default y su comentario**

```yaml
# La clave privada de la CA no sale del nodo de control. Vive junto al
# INVENTARIO y no junto al playbook: la CA es del despliegue --de estas
# maquinas, de este inventario--, no del codigo que lo despliega. Con la
# coleccion instalada, playbook_dir cae dentro de ~/.ansible/collections y
# ahi es donde iria a parar la clave con el ancla antigua. Dos despliegues con
# dos inventarios tienen dos CA, que es lo correcto.
#
# Lo que la protege de un "git add" es el .gitignore de la raiz
# (.internal-ca/ a cualquier profundidad) y el build_ignore de galaxy.yml:
# nunca entra en el repositorio ni en el tarball de la coleccion.
internal_ca_dir: "{{ inventory_dir }}/.internal-ca"
```

Las tres rutas derivadas (`internal_ca_key_path`, `csr`, `cert`) no cambian.

- [ ] **Paso 2: Un converge real del rol, por FQCN**

Playbook desechable en el scratchpad (`converge-ca.yml`):

```yaml
- hosts: localhost
  gather_facts: false
  roles: [rasty94.dex.internal_ca]
```

```bash
S=/private/tmp/claude-502/-Users-antoniorodriguez-GITHUB-RASTY-dex/a054f267-03f8-4d6c-87c5-7d376cd736c6/scratchpad
ANSIBLE_COLLECTIONS_PATH=ansible/.collections:~/.ansible/collections \
.venv/bin/ansible-playbook $S/converge-ca.yml -i ansible/inventories/local/hosts.yml \
    > /tmp/t15/ca.log 2>&1 < /dev/null; tail -3 /tmp/t15/ca.log
ls ansible/inventories/local/.internal-ca/          # ca-key.pem, ca-cert.pem, localhost-*.pem
git status --short                                  # NO enseña .internal-ca
```

Es también la prueba de que un rol por FQCN **corre**, no solo que pasa el
`--syntax-check`: el rol genera una CA de verdad con `community.crypto`.

- [ ] **Paso 3: El tarball no la lleva**

Reconstruir (paso 7 de la tarea 1) y `grep internal-ca /tmp/t15/tar.txt || echo "fuera"`.

- [ ] **Paso 4: Sabotaje**

Volver a `{{ playbook_dir }}/../../.internal-ca` en el default, repetir el paso 2: la CA
aparece en `<scratchpad>/../../.internal-ca` (fuera del repositorio, en el sitio
equivocado). Restaurar y borrar lo que el sabotaje creó.

- [ ] **Paso 5: Lint y commit**

```bash
pre-commit run --files ansible/roles/internal_ca/defaults/main.yml
git add ansible/roles/internal_ca/defaults/main.yml
git commit -m "feat(ansible): la ca interna vive junto al inventario, no junto al codigo"
```

El cuerpo cuenta la migración (mover `.internal-ca/` junto al inventario) y por qué.

---

## Tarea 4: Instalada en limpio, por tarball y por git; y CI lo comprueba

**Ficheros:**

- Modificar: `.github/workflows/release.yaml`
- Modificar: `documentacion/specs/2026-09-07-coleccion-ansible.md` §3.6 (una corrección)

**Interfaces:**

- Consume: `rasty94.dex.site` (tarea 2), el tarball (tarea 1).

- [ ] **Paso 1: Instalar el tarball en un directorio limpio y ejecutar el playbook desde ahí**

```bash
rm -rf /tmp/t15/inst && mkdir /tmp/t15/inst
.venv/bin/ansible-galaxy collection build ansible/ --output-path /tmp/t15/build --force \
    > /tmp/t15/build2.log 2>&1 < /dev/null
.venv/bin/ansible-galaxy collection install /tmp/t15/build/rasty94-dex-2.2.0.tar.gz \
    -p /tmp/t15/inst > /tmp/t15/inst.log 2>&1 < /dev/null; tail -3 /tmp/t15/inst.log
ls /tmp/t15/inst/ansible_collections/rasty94/dex/            # sin .collections, con roles/
cd /tmp && ANSIBLE_COLLECTIONS_PATH=/tmp/t15/inst:~/.ansible/collections \
  /Users/antoniorodriguez/GITHUB-RASTY/dex/.venv/bin/ansible-playbook rasty94.dex.site \
  -i /Users/antoniorodriguez/GITHUB-RASTY/dex/ansible/inventories/ejemplo/hosts.yml \
  --syntax-check > /tmp/t15/inst-syntax.log 2>&1 < /dev/null; echo "exit=$?"; tail -1 /tmp/t15/inst-syntax.log
cd /Users/antoniorodriguez/GITHUB-RASTY/dex
```

`cd /tmp` no es decorativo: fuera del repositorio no hay `ansible.cfg`, así que lo que
resuelve es la instalación y solo la instalación. El `playbook:` de la salida tiene que
estar bajo `/tmp/t15/inst/`.

- [ ] **Paso 2: Instalar por git con subdirectorio, desde un clon local**

```bash
git clone --bare -q . /tmp/t15/dex.git
rm -rf /tmp/t15/inst2 && mkdir /tmp/t15/inst2
.venv/bin/ansible-galaxy collection install \
    "git+file:///tmp/t15/dex.git#ansible/,$(git rev-parse --abbrev-ref HEAD)" \
    -p /tmp/t15/inst2 > /tmp/t15/inst2.log 2>&1 < /dev/null; tail -3 /tmp/t15/inst2.log
ls /tmp/t15/inst2/ansible_collections/rasty94/dex/playbooks/   # site.yml
```

Y la forma de `requirements.yml`, que es la que va en la documentación:

```bash
cat > /tmp/t15/req.yml <<EOF
collections:
  - name: file:///tmp/t15/dex.git#ansible/
    type: git
    version: $(git rev-parse --abbrev-ref HEAD)
EOF
rm -rf /tmp/t15/inst3 && mkdir /tmp/t15/inst3
.venv/bin/ansible-galaxy collection install -r /tmp/t15/req.yml -p /tmp/t15/inst3 \
    > /tmp/t15/inst3.log 2>&1 < /dev/null; tail -2 /tmp/t15/inst3.log
```

- [ ] **Paso 3: El trabajo de CI**

Mirar primero cómo fija las acciones `.github/workflows/artifacts.yaml` (por SHA o por
etiqueta) y hacer lo mismo. Añadir a `release.yaml`, después de `artifacts`:

```yaml
  coleccion:
    name: Coleccion de Ansible
    runs-on: ubuntu-latest
    permissions:
      contents: write   # adjuntar el tarball a la release
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.12"
      - run: pip install ansible-core==2.21.3
      - name: La version de galaxy.yml es la de la etiqueta
        # Es la unica forma de que la cifra no se olvide: nada mas la lee.
        run: |
          v=$(python3 -c "import yaml; print(yaml.safe_load(open('ansible/galaxy.yml'))['version'])")
          if [ "fork-v$v" != "$GITHUB_REF_NAME" ]; then
            echo "galaxy.yml dice $v y la etiqueta es $GITHUB_REF_NAME"; exit 1
          fi
      - run: ansible-galaxy collection build ansible/ --output-path build/
      - name: Instalada en limpio, el playbook se resuelve con el inventario de ejemplo
        # --syntax-check comprueba estructura, YAML y que cada rol y playbook
        # existan por FQCN en la coleccion INSTALADA, no en el checkout. No
        # renderiza plantillas: eso sigue siendo el --check --diff local.
        run: |
          ansible-galaxy collection install build/rasty94-dex-*.tar.gz -p inst/
          ANSIBLE_COLLECTIONS_PATH=inst ansible-playbook rasty94.dex.site \
            -i ansible/inventories/ejemplo/hosts.yml --syntax-check
      - uses: softprops/action-gh-release@v2
        with:
          files: build/rasty94-dex-*.tar.gz
```

- [ ] **Paso 4: Reproducir el trabajo a mano**

Los mismos comandos, en local, con `GITHUB_REF_NAME=fork-v2.2.0` exportado: la
comprobación de versión pasa. Con `GITHUB_REF_NAME=fork-v9.9.9`: falla con el mensaje
propio. Es el sabotaje.

Si `act` está instalado, además `act push --eventpath` con un evento de etiqueta; si no,
se dice en el informe que no se corrió.

- [ ] **Paso 5: Corregir la spec**

En `documentacion/specs/2026-09-07-coleccion-ansible.md` §3.6, el párrafo «El tercer
paso es el que importa…» afirma que el `--syntax-check` renderiza el inventario de
ejemplo. **No lo hace**: comprueba estructura y resolución por FQCN. Reescribirlo para
que diga lo que es —y que renderizar sigue siendo el `--check --diff` local del paso 6—,
sin borrar la referencia al `bind_address` de la fase 4: lo que CI sí habría cazado es
un rol o un playbook mal nombrados, y lo que no, se dice.

- [ ] **Paso 6: Renderizar el inventario de ejemplo contra la colección instalada**

Lo que CI no puede hacer y aquí sí. Desde `/tmp`, con `ANSIBLE_COLLECTIONS_PATH=/tmp/t15/inst`:

```bash
ansible-playbook rasty94.dex.site -i <repo>/ansible/inventories/ejemplo/hosts.yml \
    --check --diff --limit db-1,lb-1 \
    -e ansible_connection=local -e ansible_become=false \
    -e internal_ca_dir=/tmp/t15/ca -e mariadb_config_dir=/tmp/t15/r/mariadb \
    -e haproxy_config_dir=/tmp/t15/r/haproxy > /tmp/t15/render.log 2>&1 < /dev/null
grep -E 'bind_address|DiscoverySeeds|server db-' /tmp/t15/render.log
```

Que `docker_compose_v2` falle en `--check` no importa; lo que se quiere ver es el
`--diff` de `50-dex.cnf` y `haproxy.cfg`, y que salen con las variables del ejemplo
desde la colección instalada.

- [ ] **Paso 7: Lint y commit**

```bash
pre-commit run --files .github/workflows/release.yaml documentacion/specs/2026-09-07-coleccion-ansible.md
git add .github/workflows/release.yaml documentacion/specs/2026-09-07-coleccion-ansible.md
git commit -m "ci: construir la coleccion en cada etiqueta y comprobarla instalada en limpio"
```

---

## Tarea 5: Una sola versión de `ansible-lint`

**Ficheros:**

- Modificar: `.pre-commit-config.yaml:26`

- [ ] **Paso 1: Subir el hook**

`rev: v25.9.2` → `rev: v26.8.0` (la del `.venv`, documentada en
`despliegue-ansible.md:49`). Comprobar antes que la etiqueta existe:
`git ls-remote --tags https://github.com/ansible/ansible-lint v26.8.0`.

- [ ] **Paso 2: Lint entero, con el hook y con el venv, y que digan lo mismo**

```bash
pre-commit run ansible-lint --all-files > /tmp/t15/hook.log 2>&1; tail -3 /tmp/t15/hook.log
ANSIBLE_COLLECTIONS_PATH=ansible/.collections:~/.ansible/collections \
.venv/bin/ansible-lint ansible/ > /tmp/t15/venv.log 2>&1 < /dev/null; tail -2 /tmp/t15/venv.log
```

Los dos en verde. Lo que salte con la versión nueva se arregla en el sitio, no se
silencia; si es una regla que exige algo fuera de esta fase, se anota en el informe con
la regla y el fichero.

- [ ] **Paso 3: Commit**

```bash
git add .pre-commit-config.yaml
git commit -m "ci: el hook de ansible-lint lleva la misma version que el venv"
```

---

## Tarea 6: Documentación

**Ficheros:**

- Modificar: `documentacion/despliegue-ansible.md`, `documentacion/specs/2026-09-03-despliegue-hoja-de-ruta.md`,
  `CHANGELOG.md`, `TODO.md`, `DONE.md`

- [ ] **Paso 1: `despliegue-ansible.md`**

- La sección «Requisitos → En el nodo de control» (líneas 31-67) pasa a tener las dos
  formas: **instalar la colección** (`requirements.yml` con `type: git` y
  `version: fork-v2.2.0`, o el tarball de la release) y **desde el checkout** (`.venv`
  como hoy, y que `ansible.cfg` resuelve `rasty94.dex` al árbol). El `roles_path` y el
  `-p .venv/ansible_collections` desaparecen.
- Cada `ansible/playbooks/dex.yml` (líneas 212, 592, 616, 632) pasa a `rasty94.dex.site`.
- Un párrafo nuevo en «Secretos» o donde esté la CA: `.internal-ca/` vive junto al
  inventario, con la migración.
- En la lista de verificación manual, una entrada nueva: **instalar por
  `git+https://github.com/rasty94/dex.git#ansible/,fork-vX.Y.Z` de verdad**, la primera
  vez que se empuje una etiqueta con colección, con el comando y lo que se espera ver.

- [ ] **Paso 2: La hoja de ruta**

Fase 5 a «Entregada». La tabla queda con las cinco fases entregadas; una frase debajo
que lo diga.

- [ ] **Paso 3: `CHANGELOG.md` de la raíz**

En `[Unreleased]`, «Añadido»: la colección `rasty94.dex` con su forma de instalación;
«Cambiado»: `internal_ca_dir`, el playbook renombrado, `ansible.cfg`. Y una línea que
diga que los cambios de la colección por versión están en `ansible/CHANGELOG.md`.

- [ ] **Paso 4: `TODO.md`**

- Entrada nueva: **`meta/argument_specs.yml` por rol** — documentación formal y
  validación de cada variable; hoy vive en los comentarios de `defaults/`.
- Entrada nueva: **CI que renderice el inventario de ejemplo**, no solo `--syntax-check`:
  exige `docker_compose_v2` en modo comprobación sobre el runner. Con la razón de por
  qué se quedó fuera.
- «Última actualización» al día.

- [ ] **Paso 5: `DONE.md`**

Sección 28, con la misma forma que la 27: qué se entregó, y **lo que destapó probarlo**
— lo que el spike demostró y el plan da por hecho arriba (las tres formas del fragmento
git, que `-p` limpio no es entorno limpio, que `tags` y `repository` son obligatorios en
la práctica, que `--syntax-check` no renderiza, que `ansible-lint` tiene que correr desde
la raíz), más lo que las tareas añadan.

- [ ] **Paso 6: Lint y commit**

```bash
pre-commit run --files documentacion/despliegue-ansible.md \
    documentacion/specs/2026-09-03-despliegue-hoja-de-ruta.md CHANGELOG.md TODO.md DONE.md
git add documentacion CHANGELOG.md TODO.md DONE.md
git commit -m "docs: cerrar la fase 5, la coleccion de ansible"
```

---

## Autorrevisión

**1. Cobertura de la spec.**

| Spec | Tarea |
| --- | --- |
| §3.1 `ansible/` es la colección, namespace y nombre | 1 |
| §3.2 los dos caminos: enlace y `ansible.cfg`; FQCN en los playbooks | 1 (enlace), 2 (FQCN) |
| §3.3 versión = etiqueta, comprobada en CI | 1 (`version`), 4 (comprobación) |
| §3.4 `internal_ca_dir` en el inventario, `.gitignore`, `build_ignore`, migración | 3, 1 (`.gitignore` y `build_ignore`), 1 y 6 (CHANGELOG) |
| §3.5 `CHANGELOG.md` propio | 1 |
| §3.6 CI construye, instala, `--syntax-check`, adjunta el tarball | 4 — con la corrección de que no renderiza |
| §3.7 una sola versión de `ansible-lint` | 5 |
| §4 la forma de la colección, `build_ignore`, sin `meta/main.yml` por rol | 1 |
| §5 `site.yml`, `ansible.cfg`, inventario local, `.gitignore`, hook, documentación | 2, 1, 2, 1, 5, 6 |
| §6.1 pruebas 1-7 | 1 (1, 4), 4 (2, 3, 7), 3 (5), 5 (6) |
| §6.2 lo que no se puede probar | 6 (lista manual) |
| §7 documentación | 1 (README, CHANGELOG), 6 (el resto) |

Sin huecos. Una corrección a la spec, en la tarea 4: `--syntax-check` no renderiza.

**2. Marcadores.** Ninguno. El README de la tarea 1 se describe por secciones con su
contenido en vez de pegarse entero porque es prosa en español que el implementador
escribe con la tabla de grupos delante; cada sección dice qué lleva.

**3. Consistencia de nombres.** `rasty94.dex.site` en las tareas 2, 4 y 6.
`/tmp/t15/build/rasty94-dex-2.2.0.tar.gz` en 1 y 4. `ansible/.collections` en 1, 2, 3 y
5. `version: 2.2.0` en `galaxy.yml`, en el README, en el CHANGELOG de la colección y en
la comprobación de CI.
