# Fase 5 — La colección de Ansible publicable

> Fecha: 2026-09-07.
> Fase 5, y última, de la
> [hoja de ruta del despliegue](2026-09-03-despliegue-hoja-de-ruta.md).
> Las fases 1 y 2 en
> [2026-09-03-despliegue-ansible-valkey-ha.md](2026-09-03-despliegue-ansible-valkey-ha.md),
> la 3 en
> [2026-09-04-balanceador-haproxy-keepalived.md](2026-09-04-balanceador-haproxy-keepalived.md)
> y la 4 en
> [2026-09-06-mariadb-alta-disponibilidad.md](2026-09-06-mariadb-alta-disponibilidad.md).
> Las cuatro entregadas.

## 1. Qué se construye y por qué

Hoy los ocho roles solo se pueden usar clonando este repositorio y ejecutando el
playbook desde dentro. Funciona, pero no es instalable: nadie puede escribir
`ansible-galaxy collection install` y tener `rasty94.dex.mariadb` en su propio proyecto,
con su propio inventario, sin arrastrar un fork entero de dex en Go.

Se entrega: `ansible/` convertido en la colección `rasty94.dex`, instalable **desde el
propio repositorio por git** y fijada por etiqueta, con el checkout funcionando igual que
hoy. Ninguna fase anterior cambia de comportamiento; lo que cambia es cómo se nombra y
desde dónde se instala.

## 2. Alcance

**Dentro:**

- `ansible/galaxy.yml`, `ansible/meta/runtime.yml`, `ansible/README.md` y
  `ansible/CHANGELOG.md`: lo que hace que `ansible-galaxy collection build` produzca algo
  y que `ansible-lint` en perfil `production` lo acepte como colección.
- El playbook pasa a `ansible/playbooks/site.yml` y se invoca como `rasty94.dex.site`;
  todos los roles se referencian por FQCN.
- `ansible.cfg` en la raíz y un enlace simbólico versionado para que el checkout se
  resuelva como la colección sin instalar nada (§3.2).
- `internal_ca_dir` deja de anclarse en el repositorio y se ancla en el inventario
  (§3.4). Es **el único cambio de comportamiento** en un rol entregado.
- Los inventarios locales pierden sus rutas absolutas y sus `playbook_dir/../../`.
- `release.yaml` construye la colección, la instala en limpio y hace un `--syntax-check`
  del playbook con el inventario de ejemplo (§3.6). No publica.
- Una sola versión de `ansible-lint` en el repositorio (§3.7).

**Fuera, y por qué:**

- **Publicar en Ansible Galaxy.** Decidido: la colección se instala desde el repositorio
  por git, fijada por etiqueta, igual que la imagen Docker se publica desde el fork. Galaxy
  exigiría cuenta, namespace y un token en los secretos de CI para algo que un
  `requirements.yml` de tres líneas ya resuelve. Si algún día hace falta, es un paso más en
  `release.yaml`, no un cambio de forma.
- **`meta/argument_specs.yml` por rol.** Es la documentación formal de cada variable, y
  hoy esa documentación vive en los comentarios de `defaults/main.yml`, que es donde
  quien edita la variable la lee. Pasarla a `argument_specs` es trabajo útil —da
  validación en tiempo de ejecución— pero es otra fase: ocho roles, decenas de variables,
  y ninguna de ellas cambia por empaquetar.
- **Mover la colección a la raíz del repositorio o a un repositorio aparte.** §3.1.
- **Cualquier cambio en lo que hacen los roles.** Empaquetar no es ocasión de arreglar.
  Los huecos operativos de la fase 4 siguen en TODO.md.

## 3. Decisiones y su porqué

### 3.1 `ansible/` es la colección, dentro de este repositorio

Tres formas, y se elige la primera:

- **`ansible/` es la raíz de la colección.** `galaxy.yml` vive ahí, y el resto del
  repositorio —`cmd/`, `server/`, `storage/`, `documentacion/`— no forma parte del
  paquete. `ansible-galaxy` sabe instalar una colección que vive en un subdirectorio de un
  repositorio git: `git+https://github.com/rasty94/dex.git#ansible/,fork-v2.2.0`.
- **Mover `roles/`, `playbooks/` y `galaxy.yml` a la raíz** sería la forma canónica de
  una colección, pero este repositorio es un fork de dex: meter `roles/` junto a
  `server/` mezcla dos proyectos y ensucia para siempre el `git diff` con `upstream`, que
  es lo que las sincronizaciones con dexidp/dex necesitan limpio.
- **Un repositorio aparte** sería lo más limpio para Galaxy, y rompe lo que hace valer
  este trabajo: las specs, `DONE.md`, las pruebas en vivo y la imagen Docker viven aquí,
  y cada fase se ha probado contra el binario de este árbol. Dos repositorios son dos
  cosas que versionar a la vez, y la fase 4 acaba de demostrar que hasta un inventario
  de ejemplo se desincroniza del rol si nadie lo renderiza.

Namespace `rasty94`, nombre `dex`. Los roles quedan como `rasty94.dex.internal_ca`,
`rasty94.dex.valkey`, `rasty94.dex.dex`, `rasty94.dex.dex_dashboard`,
`rasty94.dex.haproxy`, `rasty94.dex.keepalived`, `rasty94.dex.mariadb` y
`rasty94.dex.orchestrator`. `rasty94.dex.dex` se lee raro y se queda: el rol despliega
dex, y renombrarlo a otra cosa para evitar la repetición sería mentir sobre lo que hace.

### 3.2 Los dos caminos: instalada y desde el checkout

El checkout tiene que seguir funcionando sin instalar nada: el converge local, la lista
de verificación manual y cada prueba en vivo de las fases anteriores lo usan. Y `ansible`
solo resuelve `rasty94.dex.*` si encuentra un directorio `ansible_collections/rasty94/dex`
en su ruta de colecciones.

Se resuelve con un enlace y una línea:

```
ansible/.collections/ansible_collections/rasty94/dex  →  ../../..     (enlace simbólico, versionado)
ansible.cfg:  collections_path = ansible/.collections:~/.ansible/collections
```

Desde la raíz del repositorio, `ansible-playbook rasty94.dex.site -i ansible/inventories/local/hosts.yml`
resuelve la colección al propio árbol. Instalada en otra máquina, el mismo comando
resuelve a `~/.ansible/collections`. **Un solo nombre para las dos formas**: los
playbooks, la documentación y las pruebas no tienen que saber cuál de las dos está en
uso.

`ansible-galaxy collection build` no puede meter el enlace en el tarball —apuntaría a
un `../../..` que en la instalación no existe—: va en `build_ignore` (§4).

Lo que **no** se hace: dejar `roles: [mariadb]` sin FQCN en los playbooks del repositorio
«porque desde el checkout funciona». Serían dos formas de nombrar lo mismo. Ninguna
herramienta estándar lo impide —comprobado en las tareas 2 y 3: ni `--syntax-check` ni
`ansible-lint` en perfil `production` rechazan la forma corta, porque `ansible/roles/mariadb`
resuelve igual por directorio—; «solo FQCN» es una convención de este repositorio, y la
tarea 5 la hace exigible con un hook local de `pre-commit`.

### 3.3 La versión es la etiqueta

`galaxy.yml` lleva `version: X.Y.Z` y la etiqueta del fork es `fork-vX.Y.Z`: **la misma
cifra**, una sola cadena de versiones para el binario, la imagen Docker y la colección.
Un `requirements.yml` que fije `fork-v2.2.0` instala la colección que se probó con la
imagen `fork-v2.2.0`.

`release.yaml` lo comprueba: si la etiqueta empujada no coincide con `version` en
`galaxy.yml`, el workflow falla antes de construir nada. Es la única forma de que la
cifra no se olvide, porque no hay nada más que la lea.

La primera versión de la colección es la de la **siguiente** etiqueta del fork, no la
`2.1.0` que sigue sin empujar: esa apunta a un árbol sin colección.

### 3.4 `internal_ca_dir` se ancla en el inventario, no en el repositorio

Es el único sitio donde un rol asume el repositorio:

```yaml
internal_ca_dir: "{{ playbook_dir }}/../../.internal-ca"
```

Con la colección instalada, `playbook_dir` es `~/.ansible/collections/…/playbooks`, y la
clave privada de la CA iría a parar a `~/.ansible/.internal-ca`, mezclada con las
colecciones instaladas y borrada con ellas.

Pasa a:

```yaml
internal_ca_dir: "{{ inventory_dir }}/.internal-ca"
```

La CA es del **despliegue** —de este inventario, de estas máquinas—, no del código que lo
despliega. Vive junto al inventario, que es lo que el operador versiona, cifra y guarda.
Dos despliegues con dos inventarios tienen dos CA, que es lo correcto.

Consecuencia para este repositorio: los inventarios de `ansible/inventories/` pasarían a
tener `.internal-ca/` dentro, y ese directorio está dentro de la colección. Va en
`.gitignore` (patrón `.internal-ca/` a cualquier profundidad, no solo en la raíz) y en
`build_ignore`. El comentario actual de `internal_ca/defaults/main.yml` —«fuera de
`ansible/` para que un `git add ansible/` no la arrastre nunca»— se reescribe: ahora lo
que la protege es el `.gitignore`, y hay que decirlo.

Es un cambio de comportamiento en un rol entregado, y se declara como tal en el CHANGELOG:
quien tenga un despliegue hecho desde el checkout con `.internal-ca/` en la raíz tiene que
moverlo junto a su inventario antes de volver a desplegar, o el rol emitirá una CA nueva y
reemitirá todos los certificados.

### 3.5 La colección tiene su propio `CHANGELOG.md`

`ansible-lint` exige un registro de cambios en la raíz de la colección
(`galaxy[no-changelog]`), y el del repositorio está un nivel por encima. Un enlace no
sirve: `collection build` no lo sigue y la instalación no tiene ese `..`.

`ansible/CHANGELOG.md` es un fichero real, en español, con **solo** lo que cambia en la
colección por versión: roles nuevos, variables nuevas o renombradas, cambios de
comportamiento. El de la raíz sigue siendo el del fork entero y enlaza a este para la
parte de Ansible. Son dos documentos con dos lectores: quien instala la colección no
tiene el otro.

### 3.6 CI construye e instala; no publica

`release.yaml` gana un trabajo, en cada etiqueta `fork-v*`:

1. `ansible-galaxy collection build ansible/` — que se pueda construir.
2. Instalar el tarball resultante en un directorio limpio.
3. `ansible-playbook rasty94.dex.site -i ansible/inventories/ejemplo/hosts.yml --syntax-check`
   contra la colección **instalada**, no contra el checkout.

El tercer paso es el que importa, pero no renderiza nada: `--syntax-check` comprueba
estructura, YAML y que cada rol y cada playbook resuelvan por FQCN en la colección
**instalada**, no en el checkout. Con eso se cazaría un rol o un playbook mal nombrados al
empaquetar. Lo que no se cazaría es una plantilla mal escrita: el `bind_address` sin
loopback que la revisión final de la fase 4 encontró solo sale renderizando de verdad, con
`--check --diff` contra el inventario de ejemplo (tarea 4, paso 6) — eso exigiría Docker en
CI y se queda como paso local.

El tarball se adjunta a la release de GitHub como artefacto, sin más ceremonia: es la
tercera forma de instalar (git, checkout, tarball) y sale gratis del paso 1.

### 3.7 Una sola versión de `ansible-lint`

Hoy conviven dos: el hook de `pre-commit` fija `v25.9.2` y el `.venv` documentado lleva
`26.8.0`. Con reglas de colección de por medio (`galaxy[*]`, `meta-runtime[*]`,
`role-name[*]`) dos versiones pueden discrepar sobre el mismo fichero, y un hook que pasa
con lo que CI rechaza es un hook que no sirve. El hook sube a la versión del `.venv` y
`despliegue-ansible.md` lo dice en un solo sitio.

## 4. La forma de la colección

```
ansible/
├── galaxy.yml               namespace rasty94, name dex, version X.Y.Z,
│                            dependencies: community.docker >=5.2.1, community.crypto >=3.3.0,
│                            readme, license (Apache-2.0, la de dex), repository, build_ignore
├── meta/runtime.yml         requires_ansible: ">=2.15.0"
├── README.md                qué es, cómo instalar (git y tarball), requisitos, inventario,
│                            enlace a la documentación completa en GitHub
├── CHANGELOG.md             §3.5
├── requirements.yml         se queda: instala las dependencias sin instalar la colección
│                            (el camino del checkout)
├── .ansible-lint            se queda
├── .collections/            §3.2, solo el enlace; en build_ignore
├── playbooks/site.yml       antes playbooks/dex.yml; roles por FQCN
├── roles/<ocho roles>       sin cambios salvo internal_ca_dir
└── inventories/
    ├── ejemplo/             se empaqueta: es la documentación viva del inventario
    └── local/               NO se empaqueta (build_ignore): es el converge de desarrollo
```

`build_ignore`: `.collections`, `inventories/local`, `inventories/*/.internal-ca`,
`.ansible-lint`, `.converge`.

Sin `meta/main.yml` por rol: dentro de una colección no aporta nada que `galaxy.yml` no
diga ya, y `dependencies` entre roles sería declarar un orden que el playbook ya fija con
sus tandas.

## 5. Los cambios en lo entregado

- **`ansible/playbooks/dex.yml` → `ansible/playbooks/site.yml`**, y cada `roles:` con FQCN.
  Los comentarios del playbook —que explican el orden de las tandas— no cambian.
- **`ansible.cfg`**: `roles_path = ansible/roles` desaparece; entra `collections_path`.
- **`ansible/inventories/local/hosts.yml`**: `{{ playbook_dir }}/../../` pasa a
  `{{ inventory_dir }}/../../../` (el inventario sabe dónde está; el playbook, dentro de
  una colección, ya no), y las rutas absolutas `/Users/antoniorodriguez/…` de los grupos
  `mariadb`, `loadbalancer` y `orchestrator` pasan a `{{ inventory_dir }}/../../../.converge/…`.
  Se colaron en la fase 4 porque los playbooks desechables corrían fuera del árbol y
  `playbook_dir` no servía; `inventory_dir` es lo que hacía falta entonces.
- **`.gitignore`**: `.internal-ca/` a cualquier profundidad; `/.internal-ca/` de la raíz
  se queda una versión más por quien ya lo tenga ahí.
- **`.pre-commit-config.yaml`**: `rev` del hook a la versión del `.venv`.
- **`documentacion/despliegue-ansible.md`**: la sección de requisitos deja de instalar un
  `.venv` con `roles_path` y pasa a las dos formas —`requirements.yml` con `source: git`
  y `type: git`, o el checkout con `ansible.cfg`—; cada `ansible/playbooks/dex.yml` de la
  lista de verificación manual pasa a `rasty94.dex.site`.

## 6. Pruebas

### 6.1 Lo que se puede probar aquí

1. **Se construye.** `ansible-galaxy collection build ansible/` produce
   `rasty94-dex-X.Y.Z.tar.gz`, y `tar tzf` enseña los ocho roles, `playbooks/site.yml`,
   `inventories/ejemplo` y **ni** `.collections`, **ni** `inventories/local`, **ni**
   ningún `.internal-ca`.
2. **Instalada en limpio, despliega el ejemplo.** Con `ANSIBLE_COLLECTIONS_PATH` en un
   directorio temporal vacío, instalar el tarball y ejecutar
   `ansible-playbook rasty94.dex.site -i ansible/inventories/ejemplo/hosts.yml --syntax-check`.
   Es la prueba de §3.6 corrida a mano antes de fiarla a CI. Sabotaje: dejar un
   `roles: [mariadb]` sin FQCN en `site.yml` → tiene que fallar con «role not found».
3. **Instalada por git con subdirectorio.** Desde un clon `--bare` local del repositorio,
   `ansible-galaxy collection install 'git+file:///…/dex.git#ansible/,<rama>'` en un
   directorio temporal. Prueba la sintaxis de la URL con `#ansible/,ref`, que es la que va
   en la documentación y nadie ha usado todavía.
4. **El checkout sigue funcionando.** `ansible-playbook rasty94.dex.site -i ansible/inventories/local/hosts.yml --syntax-check`
   desde la raíz del repositorio, sin nada instalado en `~/.ansible/collections` (o con
   `ANSIBLE_COLLECTIONS_PATH` apuntando solo a `ansible/.collections`). Sabotaje: quitar
   el enlace → «collection rasty94.dex not found».
5. **`internal_ca_dir` va donde el inventario.** Un converge del rol `internal_ca` solo,
   con el inventario local: la CA aparece en `ansible/inventories/local/.internal-ca/`,
   `git status` no la enseña, y `tar tzf` del tarball tampoco.
6. **`ansible-lint` en perfil `production` sobre la colección**, con las reglas de
   colección activas: cero fallos.
7. **`release.yaml` se puede ejecutar en local** con `act`, o al menos su trabajo nuevo
   se reproduce paso a paso a mano con los mismos comandos: la comprobación de versión
   contra la etiqueta tiene que fallar con una etiqueta inventada y pasar con la buena.

### 6.2 Lo que no se puede probar aquí, y se dice

- **Instalar por `git+https://github.com/rasty94/dex.git`** de verdad: exige que el
  repositorio esté empujado con la colección dentro. La prueba 3 cubre la mecánica con un
  clon local; la URL pública se comprueba la primera vez que se empuje una etiqueta con
  colección, y esa comprobación va a la lista de verificación manual.
- **El converge completo desde la colección instalada** (no solo `--syntax-check`): en
  esta máquina el converge exige el `.venv` y Docker Desktop, que son del checkout. La
  prueba 2 demuestra que la colección instalada resuelve todos los roles y renderiza el
  inventario de ejemplo; desplegar de verdad desde ella es lo mismo que desde el checkout
  con otro `collections_path`, y eso es lo que la prueba 4 demuestra.

## 7. Documentación a escribir

- **`ansible/README.md`**: nuevo, en español, autosuficiente para quien instala sin el
  repositorio: qué despliega, las dos formas de instalar, los grupos del inventario en
  una tabla, el orden de las tandas, y el enlace a `despliegue-ansible.md` en GitHub
  para todo lo demás.
- **`ansible/CHANGELOG.md`**: nuevo, con la primera versión de la colección y el cambio
  de `internal_ca_dir` como cambio de comportamiento declarado.
- **`despliegue-ansible.md`**: requisitos e instalación (§5), y cada invocación del
  playbook.
- **La hoja de ruta**: fase 5 a «Entregada»; la tabla queda completa.
- **`CHANGELOG.md`** de la raíz, **`TODO.md`** y **`DONE.md`** al cerrar, con lo que
  destapó probarlo. Y una entrada nueva en TODO.md por lo que queda fuera a propósito:
  `argument_specs.yml` por rol.
