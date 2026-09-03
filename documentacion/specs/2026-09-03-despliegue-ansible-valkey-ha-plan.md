# Plan de implementación — Fases 1 y 2

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Que `pkg/valkey` sepa hablar con sentinel y cluster, y que un playbook de
Ansible despliegue el fork entero —dex replicado, el panel replicado y Valkey en
cualquiera de las tres topologías— sobre Docker.

**Architecture:** El código va primero porque el rol no tiene nada que configurar sin él.
`Config.Address` pasa a `Addresses` más un `mode` explícito, y la validación vive en
`pkg/valkey` para que los dos binarios la hereden. El contador de ventana fija sube de
`server/ratelimit` a `pkg/valkey` para que el panel pueda usarlo sin arrastrar medio
servidor. Los tres roles de Ansible renderizan un `docker-compose.yml` por máquina, y el
de Valkey se apoya en una regla: Ansible escribe `managed.conf` y no es dueño de
`valkey.conf`.

**Tech Stack:** Go 1.27, `valkey-go`, miniredis (tests sin servidor), Ansible con
`community.docker` y `community.crypto`, Docker Compose v2, Valkey 8.

**Spec:** [2026-09-03-despliegue-ansible-valkey-ha.md](2026-09-03-despliegue-ansible-valkey-ha.md)
y la [hoja de ruta](2026-09-03-despliegue-hoja-de-ruta.md).

## Global Constraints

- Código, comentarios, logs y tests **en inglés**. Documentación del fork
  (`documentacion/`, `TODO.md`, `DONE.md`, `Ejemplos/`) **en español**.
- `misspell` en CI exige inglés americano: `canceled`, `honors`, `color`, `recognize`,
  `labeled`, `enroll`, `behavior`.
- Commits **en español sin tildes** (la ñ sí), prefijo convencional
  (`feat(valkey):`, `fix(dashboard):`, `docs:`, `ci:`), asunto en minúsculas y sin punto.
  El cuerpo cuenta el porqué y qué se verificó. **Sin atribución de ninguna herramienta.**
- **Commitear sí; `git push` no.** Ni una sola tarea de este plan hace push.
- Los valores de `mode` son exactamente `standalone`, `sentinel` y `cluster`.
- `cluster` exige `db: 0`. Un cluster de Valkey no tiene más base que esa.
- Ningún secreto en el `docker-compose.yml` ni en variables de entorno del contenedor:
  van a ficheros modo `0600`.
- Los contenedores de Valkey van en `network_mode: host`.
- Todo Valkey desplegado lleva `maxmemory-policy noeviction` y `appendonly yes`.
- La imagen se fija a una etiqueta `fork-vX.Y.Z`, **nunca `latest`**.
- Un rol es correcto solo si converge dos veces y la segunda no cambia nada.
- Nada de `git add -A` a ciegas: `go build ./...` deja binarios y `references/` no se
  versiona.
- Tras cambiar código Go: `graphify update .`.

---

## Estructura de ficheros

**Fase 1 — código**

| Fichero | Responsabilidad |
| --- | --- |
| `pkg/valkey/valkey.go` | Modificado: `Addresses`, `Mode`, `MasterSet`, y `New` construyendo las tres topologías |
| `pkg/valkey/config.go` | **Nuevo**: `Config`, sus constantes de modo y `Validate()`. Sale de `valkey.go` porque la validación crece y la conexión no |
| `pkg/valkey/fixedwindow.go` | **Nuevo**: el contador de ventana fija, subido desde `server/ratelimit/valkey.go` |
| `pkg/valkey/config_test.go` | **Nuevo**: tabla de validación |
| `pkg/valkey/ha_test.go` | **Nuevo**: tests de sentinel y cluster, tras variable de entorno |
| `server/ratelimit/valkey.go` | Modificado: usa `valkey.FixedWindow` en vez de su `sharedCounter` |
| `cmd/dex-dashboard/auth.go` | Modificado: `attemptLimiter` con almacén compartido opcional |
| `docker-compose.valkey-ha.yaml` | **Nuevo**: pila de sentinel y pila de cluster para los tests |

**Fase 2 — Ansible**

| Fichero | Responsabilidad |
| --- | --- |
| `ansible/playbooks/dex.yml` | Orquesta los tres roles en orden |
| `ansible/inventories/ejemplo/hosts.yml` | Inventario de ejemplo con las tres topologías comentadas |
| `ansible/roles/valkey/` | La topología: `managed.conf`, TLS, bootstrap de sentinel y cluster |
| `ansible/roles/dex/` | Configuración de dex, secretos a fichero, compose |
| `ansible/roles/dex_dashboard/` | Lo mismo para el panel |
| `documentacion/despliegue-ansible.md` | Cómo se usa |

---

## Task 1: `pkg/valkey` — la configuración y su validación

**Files:**
- Create: `pkg/valkey/config.go`, `pkg/valkey/config_test.go`
- Modify: `pkg/valkey/valkey.go` (quitar `Config` y `TLSConfig`, que se van a `config.go`)

**Interfaces:**
- Consumes: nada.
- Produces: `valkey.Config{Mode, Addresses, MasterSet, SentinelUsername, SentinelPassword, Username, Password, DB, KeyPrefix, TLS}`, las constantes `ModeStandalone`, `ModeSentinel`, `ModeCluster` (todas `string`), y `func (c Config) Validate() error`.

- [ ] **Step 1: Escribir el test que falla**

En `pkg/valkey/config_test.go`:

```go
package valkey

import "testing"

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string // substring; empty means the config is valid
	}{
		{"no address at all means everything stays in memory", Config{}, ""},
		{"standalone is the default", Config{Addresses: []string{"a:6379"}}, ""},
		{"an unknown mode names the three that exist", Config{Mode: "clustered", Addresses: []string{"a:6379"}}, "standalone"},
		{"standalone refuses a second address", Config{Mode: ModeStandalone, Addresses: []string{"a:6379", "b:6379"}}, "mode"},
		{"sentinel needs a master set", Config{Mode: ModeSentinel, Addresses: []string{"s1:26379"}}, "masterSet"},
		{"sentinel with a master set is valid", Config{Mode: ModeSentinel, Addresses: []string{"s1:26379"}, MasterSet: "dex"}, ""},
		{"cluster is valid on db 0", Config{Mode: ModeCluster, Addresses: []string{"a:6379", "b:6379", "c:6379"}}, ""},
		{"a valkey cluster has no database but zero", Config{Mode: ModeCluster, Addresses: []string{"a:6379"}, DB: 1}, "db"},
		{"a mode without addresses is a mistake, not a way to disable it", Config{Mode: ModeSentinel}, "addresses"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want no error", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate() = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
```

Añadir `"strings"` al import.

- [ ] **Step 2: Comprobar que falla por lo que tiene que fallar**

Run: `go test ./pkg/valkey/ -run TestConfigValidate`
Expected: FAIL, no compila — `undefined: ModeStandalone`.

- [ ] **Step 3: Escribir `pkg/valkey/config.go`**

Mover `Config` y `TLSConfig` desde `valkey.go` (borrarlos de allí) y ampliarlos:

```go
package valkey

import "fmt"

// The topologies a deployment can ask for. The mode is explicit on purpose: it
// could be guessed from the number of addresses, and that guess fails silently.
// Given several addresses that turn out not to form a cluster, valkey-go does
// not fail -- it falls back to standalone against one of them. The operator
// asked for a cluster, got a single node, and nothing said so.
const (
	ModeStandalone = "standalone"
	ModeSentinel   = "sentinel"
	ModeCluster    = "cluster"
)

// Config is the shared store. Empty Addresses keeps every caller on its own
// in-process state, which is the default and needs no server at all.
type Config struct {
	// Mode is standalone, sentinel or cluster. Empty means standalone.
	Mode string `json:"mode"`
	// Addresses are the data nodes, or the sentinels when Mode is sentinel.
	Addresses []string `json:"addresses"`
	// MasterSet is the name sentinel monitors the master under. Required by,
	// and only meaningful to, sentinel.
	MasterSet string `json:"masterSet"`

	// Sentinels can carry credentials of their own, configured separately from
	// the data nodes. Empty falls back to Username and Password.
	SentinelUsername string `json:"sentinelUsername"`
	SentinelPassword string `json:"sentinelPassword"`

	Username  string    `json:"username"`
	Password  string    `json:"password"`
	DB        int       `json:"db"`
	KeyPrefix string    `json:"keyPrefix"`
	TLS       TLSConfig `json:"tls"`
}

type TLSConfig struct {
	CACert             string `json:"caCert"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
}

// mode returns the topology, defaulting to standalone.
func (c Config) mode() string {
	if c.Mode == "" {
		return ModeStandalone
	}
	return c.Mode
}

// Validate rejects a configuration that would connect and then misbehave. It
// lives here rather than in cmd/dex so the dashboard, which has no Validate of
// its own, gets the same checks.
func (c Config) Validate() error {
	if len(c.Addresses) == 0 {
		// No shared store at all: the default, and not an error. A mode without
		// addresses is a mistake, though -- somebody meant to configure this.
		if c.Mode != "" {
			return fmt.Errorf("valkey: mode %q needs at least one entry in addresses", c.Mode)
		}
		return nil
	}

	switch c.mode() {
	case ModeStandalone:
		if len(c.Addresses) > 1 {
			return fmt.Errorf("valkey: standalone takes one address, got %d; set mode to %q or %q to use several",
				len(c.Addresses), ModeSentinel, ModeCluster)
		}
	case ModeSentinel:
		if c.MasterSet == "" {
			return fmt.Errorf("valkey: mode %q needs masterSet, the name sentinel monitors the master under", ModeSentinel)
		}
	case ModeCluster:
		if c.DB != 0 {
			// It would connect and then fail on every command.
			return fmt.Errorf("valkey: a cluster has no database but 0, got db %d", c.DB)
		}
	default:
		return fmt.Errorf("valkey: unknown mode %q, want %q, %q or %q",
			c.Mode, ModeStandalone, ModeSentinel, ModeCluster)
	}
	return nil
}
```

- [ ] **Step 4: Comprobar que pasa**

Run: `go test ./pkg/valkey/ -run TestConfigValidate -v`
Expected: PASS, los nueve casos.

- [ ] **Step 5: Sabotear el test una vez**

Quitar la comprobación de `c.DB != 0` en el caso `ModeCluster` y volver a correr.
Expected: FAIL en «a valkey cluster has no database but zero». Restaurar.

- [ ] **Step 6: Commit**

```bash
git add pkg/valkey/config.go pkg/valkey/config_test.go pkg/valkey/valkey.go
git commit -m "feat(valkey): configuracion con modo, varias direcciones y su validacion

El modo es explicito a proposito: deducirlo del numero de direcciones falla en
silencio, porque valkey-go cae a standalone contra uno de los nodos cuando lo
que le das no forma cluster. La validacion vive en pkg/valkey y no en cmd/dex
porque el panel no tiene Validate y asi la heredan los dos binarios."
```

---

## Task 2: `pkg/valkey` — conectar en las tres topologías

**Files:**
- Modify: `pkg/valkey/valkey.go:44-93` (la función `New`)
- Test: `pkg/valkey/valkey_test.go`

**Interfaces:**
- Consumes: `Config`, `ModeSentinel`, `ModeCluster` de la Task 1.
- Produces: `func New(ctx context.Context, cfg Config) (*Client, error)` sin cambio de firma. Sigue devolviendo `(nil, nil)` cuando no hay direcciones.

- [ ] **Step 1: Escribir el test que falla**

Añadir a `pkg/valkey/valkey_test.go`:

```go
// New refuses a configuration that Validate rejects, rather than connecting and
// misbehaving later. miniredis is a real server, so this proves the check runs
// before the dial and not after it.
func TestNewValidatesBeforeConnecting(t *testing.T) {
	m := miniredis.RunT(t)

	_, err := New(t.Context(), Config{Mode: ModeCluster, Addresses: []string{m.Addr()}, DB: 1})
	if err == nil {
		t.Fatal("New accepted a cluster on db 1")
	}
	if !strings.Contains(err.Error(), "database but 0") {
		t.Errorf("New() = %v, want the error to explain the database restriction", err)
	}
}

// A single address with no mode is what every existing deployment has, and it
// has to keep working exactly as before.
func TestStandaloneIsStillTheDefault(t *testing.T) {
	m := miniredis.RunT(t)

	c, err := New(t.Context(), Config{Addresses: []string{m.Addr()}, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	if err := c.Do(t.Context(), c.B().Set().Key(c.Key("k")).Value("v").Build()).Error(); err != nil {
		t.Errorf("set against a standalone server: %v", err)
	}
}
```

- [ ] **Step 2: Comprobar que falla**

Run: `go test ./pkg/valkey/ -run 'TestNewValidates|TestStandaloneIsStill'`
Expected: FAIL — `unknown field Addresses in struct literal`.

- [ ] **Step 3: Reescribir `New`**

En `pkg/valkey/valkey.go`, sustituir el cuerpo de `New` hasta la llamada a
`valkeygo.NewClient`:

```go
// New opens and verifies the connection. Empty addresses return (nil, nil):
// that is the configuration saying everything stays in memory.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if len(cfg.Addresses) == 0 {
		return nil, nil
	}

	opt := valkeygo.ClientOption{
		InitAddress: cfg.Addresses,
		Username:    cfg.Username,
		Password:    cfg.Password,
		SelectDB:    cfg.DB,
		// Every caller here reads and writes its own keys directly, so
		// client-side caching buys nothing. Disabling it also keeps this
		// package usable against miniredis in tests, which does not
		// implement the server-assisted invalidation tracking it needs.
		DisableCache: true,
	}

	switch cfg.mode() {
	case ModeSentinel:
		// The addresses are the sentinels, not the data nodes: valkey-go asks
		// them for the master and follows the +switch-master events, so a
		// failover needs nothing from us.
		opt.Sentinel = valkeygo.SentinelOption{
			MasterSet: cfg.MasterSet,
			Username:  firstNonEmpty(cfg.SentinelUsername, cfg.Username),
			Password:  firstNonEmpty(cfg.SentinelPassword, cfg.Password),
		}
	case ModeCluster:
		// Cluster mode is detected from the nodes themselves. Shuffling keeps
		// every replica from hammering the same node while it starts up.
		opt.ShuffleInit = true
	}

	if cfg.TLS.CACert != "" || cfg.TLS.InsecureSkipVerify {
		// ... el bloque TLS existente, sin cambios ...
	}
	// ... NewClient, Ping y el return existentes, sin cambios ...
}

// firstNonEmpty returns a, or b when a is empty. Sentinels can carry their own
// credentials; when they are not given, they are the data nodes' credentials.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
```

El `opt.Sentinel` necesita el mismo `TLSConfig` que el resto cuando hay TLS; añadir,
dentro del bloque TLS ya existente y después de `opt.TLSConfig = tlsCfg`:

```go
		if opt.Sentinel.MasterSet != "" {
			opt.Sentinel.TLSConfig = tlsCfg
		}
```

- [ ] **Step 4: Comprobar que pasa**

Run: `go test ./pkg/valkey/`
Expected: PASS.

- [ ] **Step 5: Actualizar todos los usos de `Address`**

Run: `grep -rn 'dexvalkey.Config{Address:\|valkey.Config{Address:\|Config{Address:' --include='*.go' .`

Cambiar cada `Config{Address: x}` por `Config{Addresses: []string{x}}`. Están en
`server/ratelimit/valkey_test.go`, `connector/keystone/cache_valkey_test.go`,
`cmd/dex-dashboard/sessions_valkey_test.go` y `pkg/valkey/valkey_test.go`.

En `cmd/dex/serve.go:421` y `cmd/dex-dashboard/main.go:57`, la línea de log
`"address", c.Valkey.Address` pasa a `"addresses", c.Valkey.Addresses, "mode", c.Valkey.Mode`.

- [ ] **Step 6: Comprobar que todo sigue verde**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/valkey cmd/dex/serve.go cmd/dex-dashboard/main.go server/ratelimit/valkey_test.go connector/keystone/cache_valkey_test.go cmd/dex-dashboard/sessions_valkey_test.go
git commit -m "feat(valkey): conectar contra sentinel y contra cluster

valkey-go ya sabe seguir un failover de sentinel --se suscribe a
+switch-master-- y reencaminar los MOVED del cluster, asi que esto es dejarle
expresar la topologia y poco mas. Los sentinels pueden llevar credenciales
propias; sin ellas se usan las de los nodos de datos."
```

---

## Task 3: El contador de ventana fija sube a `pkg/valkey`

**Files:**
- Create: `pkg/valkey/fixedwindow.go`, `pkg/valkey/fixedwindow_test.go`
- Modify: `server/ratelimit/valkey.go` (queda casi vacío), `server/ratelimit/ratelimit.go:51` y `:113`

**Interfaces:**
- Consumes: `*valkey.Client` de las tareas anteriores.
- Produces: `func NewFixedWindow(c *Client, kind string) *FixedWindow`,
  `func (w *FixedWindow) Incr(ctx context.Context, key string, window time.Duration) (int64, error)`,
  `func (w *FixedWindow) Reset(ctx context.Context, key string) error`.
  `kind` es el prefijo de clave: `"rl"` para el limitador de dex, `"dl"` para el del panel.

**Por qué:** el panel necesita el mismo contador en la Task 8 y no puede importar
`server/ratelimit` sin arrastrar `reqctx` y prometheus para usar una función. El
primitivo pertenece a `pkg/valkey`, que es donde viven las piezas compartidas.

- [ ] **Step 1: Escribir el test que falla**

`pkg/valkey/fixedwindow_test.go`:

```go
package valkey

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestFixedWindowCountsAcrossClients(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	newWindow := func() *FixedWindow {
		c, err := New(ctx, Config{Addresses: []string{m.Addr()}, KeyPrefix: "dex:"})
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		t.Cleanup(c.Close)
		return NewFixedWindow(c, "rl")
	}

	a, b := newWindow(), newWindow()

	// Two processes, one budget: that is the whole point.
	if n, err := a.Incr(ctx, "k", time.Minute); err != nil || n != 1 {
		t.Fatalf("first attempt = %d, %v; want 1, nil", n, err)
	}
	if n, err := b.Incr(ctx, "k", time.Minute); err != nil || n != 2 {
		t.Fatalf("second attempt from another client = %d, %v; want 2, nil", n, err)
	}

	// Reset clears it for everyone.
	if err := a.Reset(ctx, "k"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if n, err := b.Incr(ctx, "k", time.Minute); err != nil || n != 1 {
		t.Fatalf("after reset = %d, %v; want 1, nil", n, err)
	}
}

// The window has to expire on its own. Without the PEXPIRE, a counter that
// reached the limit would lock the key out until somebody noticed.
func TestFixedWindowExpires(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	c, err := New(ctx, Config{Addresses: []string{m.Addr()}, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(c.Close)
	w := NewFixedWindow(c, "rl")

	if _, err := w.Incr(ctx, "k", time.Minute); err != nil {
		t.Fatalf("incr: %v", err)
	}
	m.FastForward(2 * time.Minute)
	if n, _ := w.Incr(ctx, "k", time.Minute); n != 1 {
		t.Errorf("after the window passed = %d, want the count to start over at 1", n)
	}
}

// Two kinds must not share a budget: the dashboard's own login throttle is not
// dex's.
func TestFixedWindowKindsAreSeparate(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	c, err := New(ctx, Config{Addresses: []string{m.Addr()}, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(c.Close)

	if _, err := NewFixedWindow(c, "rl").Incr(ctx, "k", time.Minute); err != nil {
		t.Fatalf("incr: %v", err)
	}
	if n, _ := NewFixedWindow(c, "dl").Incr(ctx, "k", time.Minute); n != 1 {
		t.Errorf("a different kind counted %d, want its own budget starting at 1", n)
	}
}
```

- [ ] **Step 2: Comprobar que falla**

Run: `go test ./pkg/valkey/ -run TestFixedWindow`
Expected: FAIL — `undefined: NewFixedWindow`.

- [ ] **Step 3: Escribir `pkg/valkey/fixedwindow.go`**

Mover el script y el contador desde `server/ratelimit/valkey.go`:

```go
package valkey

import (
	"context"
	"strconv"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"
)

// fixedWindow counts one attempt and, when it is the first of a window, gives
// the key its lifetime. Both in one script: doing it in two commands leaves a
// counter with no expiry if the process dies in between, and that key would
// lock the user out until someone noticed.
var fixedWindow = valkeygo.NewLuaScript(`
local n = redis.call("INCR", KEYS[1])
if n == 1 then redis.call("PEXPIRE", KEYS[1], ARGV[1]) end
return n
`)

// FixedWindow is a counter shared by every replica, used to throttle attempts.
//
// It is not a token bucket: x/time/rate keeps its state in process and cannot
// be shared. A fixed window allows up to 2 x limit across a window boundary,
// which is the accepted trade for a login throttle and is correct across
// replicas -- the point of having it at all.
//
// One key per attempt, so it is safe in cluster mode: every command it issues
// touches a single key and therefore a single slot.
type FixedWindow struct {
	c    *Client
	kind string
}

// NewFixedWindow returns a counter whose keys are namespaced under kind, so two
// throttles sharing a server do not share a budget.
func NewFixedWindow(c *Client, kind string) *FixedWindow {
	return &FixedWindow{c: c, kind: kind}
}

// Incr counts one attempt against key and returns the running total for the
// current window.
func (w *FixedWindow) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	k := w.c.HashKey(w.kind, key)
	ms := strconv.FormatInt(window.Milliseconds(), 10)
	return fixedWindow.Exec(ctx, w.c.Client, []string{k}, []string{ms}).AsInt64()
}

// Reset forgets the attempts recorded for key.
func (w *FixedWindow) Reset(ctx context.Context, key string) error {
	return w.c.Do(ctx, w.c.B().Del().Key(w.c.HashKey(w.kind, key)).Build()).Error()
}
```

- [ ] **Step 4: Adaptar `server/ratelimit`**

Borrar `server/ratelimit/valkey.go` entero. En `server/ratelimit/ratelimit.go`, el campo
`shared *sharedCounter` pasa a `shared *dexvalkey.FixedWindow`, `SetSharedStore` pasa a
`l.shared = dexvalkey.NewFixedWindow(c, "rl")`, y las llamadas pasan de
`l.shared.incr(...)` / `l.shared.reset(...)` a `l.shared.Incr(...)` / `l.shared.Reset(...)`.

El comentario del campo se mantiene tal cual: sigue siendo cierto.

- [ ] **Step 5: Comprobar que pasa todo**

Run: `go test ./pkg/valkey/ ./server/ratelimit/`
Expected: PASS. Los tests de `server/ratelimit/valkey_test.go` siguen valiendo sin
tocarlos: prueban el `Limiter`, no el contador.

- [ ] **Step 6: Sabotear**

En `Incr`, cambiar `w.c.HashKey(w.kind, key)` por `w.c.HashKey("rl", key)`.
Expected: FAIL en `TestFixedWindowKindsAreSeparate`. Restaurar.

- [ ] **Step 7: Commit**

```bash
git add pkg/valkey/fixedwindow.go pkg/valkey/fixedwindow_test.go server/ratelimit/
git commit -m "refactor(valkey): subir el contador de ventana fija a pkg/valkey

El panel va a necesitar el mismo contador para su limitador de login, y no
puede importar server/ratelimit sin arrastrar reqctx y prometheus para usar una
funcion. El primitivo va donde estan las piezas compartidas. El prefijo por tipo
evita que dos limitadores distintos compartan presupuesto."
```

---

## Task 4: La pila de pruebas de sentinel y cluster

**Files:**
- Create: `docker-compose.valkey-ha.yaml`
- Create: `pkg/valkey/ha_test.go`
- Modify: `.github/workflows/ci.yaml`

**Interfaces:**
- Consumes: `New`, `Config`, `ModeSentinel`, `ModeCluster`, `NewFixedWindow`.
- Produces: las variables `DEX_VALKEY_SENTINEL_ADDRS` y `DEX_VALKEY_CLUSTER_ADDRS`
  (listas separadas por comas) que activan estos tests. Sin ellas, se saltan.

**Por qué una pila propia y no `services:` de GitHub Actions:** los servicios de Actions
se publican en puertos aleatorios del runner, y sentinel devuelve al cliente la dirección
*interna* del master, que desde el job no se alcanza. Con `network_mode: host` en un
runner Linux, las direcciones que anuncia el gossip son las que el test puede usar.

- [ ] **Step 1: Escribir `docker-compose.valkey-ha.yaml`**

```yaml
# Pilas de Valkey en alta disponibilidad para los tests de integración.
# Se levantan a mano o desde CI; no forman parte de ningún despliegue.
#
#   docker compose -f docker-compose.valkey-ha.yaml --profile sentinel up -d
#   DEX_VALKEY_SENTINEL_ADDRS=127.0.0.1:26379,127.0.0.1:26380,127.0.0.1:26381 go test ./pkg/valkey/
#
# network_mode: host porque sentinel y cluster se anuncian por IP real: dentro de
# una red bridge anunciarían direcciones de contenedor que el test no alcanza.
services:
    valkey-master:
        profiles: ["sentinel"]
        image: valkey/valkey:8-alpine
        network_mode: host
        command: ["valkey-server", "--port", "6390", "--appendonly", "no", "--maxmemory-policy", "noeviction"]

    valkey-replica:
        profiles: ["sentinel"]
        image: valkey/valkey:8-alpine
        network_mode: host
        command: ["valkey-server", "--port", "6391", "--replicaof", "127.0.0.1", "6390", "--appendonly", "no"]
        depends_on: [valkey-master]

    sentinel-1: &sentinel
        profiles: ["sentinel"]
        image: valkey/valkey:8-alpine
        network_mode: host
        entrypoint: ["sh", "-c"]
        command:
            - |
              cat >/tmp/s.conf <<EOF
              port 26379
              sentinel monitor dex 127.0.0.1 6390 2
              sentinel down-after-milliseconds dex 1000
              sentinel failover-timeout dex 5000
              EOF
              exec valkey-sentinel /tmp/s.conf
        depends_on: [valkey-master]

    sentinel-2:
        <<: *sentinel
        command:
            - |
              cat >/tmp/s.conf <<EOF
              port 26380
              sentinel monitor dex 127.0.0.1 6390 2
              sentinel down-after-milliseconds dex 1000
              sentinel failover-timeout dex 5000
              EOF
              exec valkey-sentinel /tmp/s.conf

    sentinel-3:
        <<: *sentinel
        command:
            - |
              cat >/tmp/s.conf <<EOF
              port 26381
              sentinel monitor dex 127.0.0.1 6390 2
              sentinel down-after-milliseconds dex 1000
              sentinel failover-timeout dex 5000
              EOF
              exec valkey-sentinel /tmp/s.conf

    cluster-1: &clusternode
        profiles: ["cluster"]
        image: valkey/valkey:8-alpine
        network_mode: host
        command:
            ["valkey-server", "--port", "7001", "--cluster-enabled", "yes",
             "--cluster-config-file", "/tmp/nodes-7001.conf", "--appendonly", "no"]

    cluster-2:
        <<: *clusternode
        command:
            ["valkey-server", "--port", "7002", "--cluster-enabled", "yes",
             "--cluster-config-file", "/tmp/nodes-7002.conf", "--appendonly", "no"]

    cluster-3:
        <<: *clusternode
        command:
            ["valkey-server", "--port", "7003", "--cluster-enabled", "yes",
             "--cluster-config-file", "/tmp/nodes-7003.conf", "--appendonly", "no"]

    # Forma el cluster una vez y sale. --cluster-yes evita la confirmación
    # interactiva; sin réplicas, porque estos tests prueban el enrutado por
    # slots, no el failover.
    cluster-init:
        profiles: ["cluster"]
        image: valkey/valkey:8-alpine
        network_mode: host
        restart: "no"
        entrypoint:
            ["sh", "-c",
             "sleep 3 && valkey-cli --cluster create 127.0.0.1:7001 127.0.0.1:7002 127.0.0.1:7003 --cluster-yes"]
        depends_on: [cluster-1, cluster-2, cluster-3]
```

- [ ] **Step 2: Escribir los tests que se saltan sin la pila**

`pkg/valkey/ha_test.go`:

```go
package valkey

import (
	"os"
	"strings"
	"testing"
	"time"
)

// addrsFrom reads a comma-separated list from the environment, skipping the test
// when it is not set. This follows the pattern storage/sql already uses with
// DEX_MYSQL_HOST: the suite runs everywhere, and grows teeth where the stack is up.
func addrsFrom(t *testing.T, env string) []string {
	t.Helper()
	raw := os.Getenv(env)
	if raw == "" {
		t.Skipf("%s not set; start docker-compose.valkey-ha.yaml to run this", env)
	}
	return strings.Split(raw, ",")
}

func TestSentinelResolvesTheMaster(t *testing.T) {
	c, err := New(t.Context(), Config{
		Mode:      ModeSentinel,
		Addresses: addrsFrom(t, "DEX_VALKEY_SENTINEL_ADDRS"),
		MasterSet: "dex",
		KeyPrefix: "dextest:",
	})
	if err != nil {
		t.Fatalf("connect through sentinel: %v", err)
	}
	defer c.Close()

	// Writing proves it found the master and not a replica: a replica refuses
	// writes with READONLY.
	if err := c.Do(t.Context(), c.B().Set().Key(c.Key("probe")).Value("v").Ex(time.Minute).Build()).Error(); err != nil {
		t.Errorf("write through sentinel: %v", err)
	}
}

func TestClusterRoutesByKey(t *testing.T) {
	c, err := New(t.Context(), Config{
		Mode:      ModeCluster,
		Addresses: addrsFrom(t, "DEX_VALKEY_CLUSTER_ADDRS"),
		KeyPrefix: "dextest:",
	})
	if err != nil {
		t.Fatalf("connect to the cluster: %v", err)
	}
	defer c.Close()

	// Enough keys to land in different slots on different nodes. Getting them
	// all back proves the client follows the MOVED redirections.
	w := NewFixedWindow(c, "rl")
	for i := 0; i < 32; i++ {
		key := "probe-" + strconv.Itoa(i)
		if n, err := w.Incr(t.Context(), key, time.Minute); err != nil || n != 1 {
			t.Fatalf("key %q across the cluster = %d, %v; want 1, nil", key, n, err)
		}
	}
}
```

Añadir `"strconv"` al import.

- [ ] **Step 3: Comprobar los dos caminos**

Run: `go test ./pkg/valkey/ -run 'TestSentinel|TestCluster' -v`
Expected: SKIP los dos, con el mensaje que dice qué levantar.

Run:
```bash
docker compose -f docker-compose.valkey-ha.yaml --profile sentinel up -d
sleep 5
DEX_VALKEY_SENTINEL_ADDRS=127.0.0.1:26379,127.0.0.1:26380,127.0.0.1:26381 \
  go test ./pkg/valkey/ -run TestSentinel -v
docker compose -f docker-compose.valkey-ha.yaml --profile sentinel down
```
Expected: PASS.

Run:
```bash
docker compose -f docker-compose.valkey-ha.yaml --profile cluster up -d
sleep 8
DEX_VALKEY_CLUSTER_ADDRS=127.0.0.1:7001,127.0.0.1:7002,127.0.0.1:7003 \
  go test ./pkg/valkey/ -run TestCluster -v
docker compose -f docker-compose.valkey-ha.yaml --profile cluster down
```
Expected: PASS.

- [ ] **Step 4: Añadir el paso a CI**

En `.github/workflows/ci.yaml`, después del paso de tests existente, un paso nuevo
—no un `service:`, por lo explicado arriba:

```yaml
      - name: Test Valkey HA topologies
        run: |
          docker compose -f docker-compose.valkey-ha.yaml --profile sentinel up -d
          docker compose -f docker-compose.valkey-ha.yaml --profile cluster up -d
          sleep 10
          go test ./pkg/valkey/ -run 'TestSentinel|TestCluster' -v
        env:
          DEX_VALKEY_SENTINEL_ADDRS: 127.0.0.1:26379,127.0.0.1:26380,127.0.0.1:26381
          DEX_VALKEY_CLUSTER_ADDRS: 127.0.0.1:7001,127.0.0.1:7002,127.0.0.1:7003
```

- [ ] **Step 5: Commit**

```bash
git add docker-compose.valkey-ha.yaml pkg/valkey/ha_test.go .github/workflows/ci.yaml
git commit -m "test(valkey): pila de sentinel y de cluster para los tests de integracion

Mismo patron que storage/sql con DEX_MYSQL_HOST: sin las variables el test se
salta y la suite corre en cualquier sitio contra miniredis. No van como
services: de Actions porque se publican en puertos aleatorios y sentinel
devuelve la direccion interna del master, que desde el job no se alcanza; con
network_mode host lo que anuncia el gossip es lo que el test puede usar."
```

---

## Task 5: El test que justifica el trabajo — sobrevivir a un failover

**Files:**
- Modify: `pkg/valkey/ha_test.go`

**Interfaces:**
- Consumes: todo lo anterior.
- Produces: nada nuevo.

**Por qué su propia tarea:** es la única prueba de que la alta disponibilidad sirve para
algo. Todo lo demás demuestra que se conecta.

- [ ] **Step 1: Escribir el test**

```go
// The point of sentinel: kill the master and the shared budget survives. Without
// this the rest only proves that the client can connect.
//
// Not run by default even with the stack up: it stops a container and takes
// about fifteen seconds.
func TestTheBudgetSurvivesAFailover(t *testing.T) {
	if os.Getenv("DEX_VALKEY_FAILOVER_TEST") == "" {
		t.Skip("DEX_VALKEY_FAILOVER_TEST not set; this one stops a container")
	}
	ctx := t.Context()

	c, err := New(ctx, Config{
		Mode:      ModeSentinel,
		Addresses: addrsFrom(t, "DEX_VALKEY_SENTINEL_ADDRS"),
		MasterSet: "dex",
		KeyPrefix: "dextest:",
	})
	if err != nil {
		t.Fatalf("connect through sentinel: %v", err)
	}
	defer c.Close()

	w := NewFixedWindow(c, "rl")
	key := "failover-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	for i := int64(1); i <= 3; i++ {
		if n, err := w.Incr(ctx, key, 10*time.Minute); err != nil || n != i {
			t.Fatalf("attempt %d = %d, %v; want %d, nil", i, n, err, i)
		}
	}

	// Stop the master. Sentinel promotes the replica within
	// down-after-milliseconds plus failover-timeout.
	out, err := exec.Command("docker", "compose", "-f", "../../docker-compose.valkey-ha.yaml",
		"--profile", "sentinel", "stop", "valkey-master").CombinedOutput()
	if err != nil {
		t.Fatalf("stop the master: %v: %s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-f", "../../docker-compose.valkey-ha.yaml",
			"--profile", "sentinel", "start", "valkey-master").Run()
	})

	// The count has to carry on from where it was, against the new master. It
	// may take a few tries while the promotion happens.
	deadline := time.Now().Add(30 * time.Second)
	for {
		n, err := w.Incr(ctx, key, 10*time.Minute)
		if err == nil {
			if n != 4 {
				t.Fatalf("after the failover the count was %d, want 4: the budget was reset, which hands an attacker a fresh one", n)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no promotion within 30s: %v", err)
		}
		time.Sleep(time.Second)
	}
}
```

Añadir `"os/exec"` al import.

- [ ] **Step 2: Correrlo de verdad**

```bash
docker compose -f docker-compose.valkey-ha.yaml --profile sentinel up -d
sleep 5
DEX_VALKEY_FAILOVER_TEST=1 \
DEX_VALKEY_SENTINEL_ADDRS=127.0.0.1:26379,127.0.0.1:26380,127.0.0.1:26381 \
  go test ./pkg/valkey/ -run TestTheBudgetSurvives -v -timeout 2m
docker compose -f docker-compose.valkey-ha.yaml --profile sentinel down
```
Expected: PASS. **Guardar la salida completa**: va al cuerpo del commit.

- [ ] **Step 3: Comprobar que el test detecta lo que dice detectar**

Cambiar la aserción `n != 4` por `n != 1` y volver a correr.
Expected: FAIL diciendo que el presupuesto se reinició. Restaurar.

Esto confirma que el test distingue «el contador sobrevivió» de «el contador volvió a
empezar», que es exactamente la diferencia entre tener alta disponibilidad y creer que
se tiene.

- [ ] **Step 4: Commit**

```bash
git add pkg/valkey/ha_test.go
git commit -m "test(valkey): el presupuesto del limitador sobrevive a un failover

Es la unica prueba de que la alta disponibilidad sirve de algo; lo demas
demuestra que el cliente conecta. Se para el master, sentinel promueve la
replica y el contador tiene que seguir en 4, no volver a 1: volver a 1 es
regalarle a un atacante un presupuesto entero.

Fuera de la ejecucion normal tras DEX_VALKEY_FAILOVER_TEST porque para un
contenedor y tarda unos quince segundos."
```

---

## Task 6: Configuración, imagen y documentación del cambio

**Files:**
- Modify: `config.docker.yaml:43-68`, `Ejemplos/dashboard/dex.yaml`,
  `Ejemplos/dashboard/docker-compose.yml`, `documentacion/valkey.md`, `CHANGELOG.md`
- Test: manual contra `Ejemplos/dashboard`

**Interfaces:**
- Consumes: `Config` con `Mode`, `Addresses`, `MasterSet`.
- Produces: `DEX_VALKEY_ADDRESSES`, `DEX_VALKEY_MODE`, `DEX_VALKEY_MASTER_SET`.

- [ ] **Step 1: Actualizar `config.docker.yaml`**

Sustituir el bloque `valkey:` por:

```
{{- /* Shared store between replicas: the login rate limiter's counters and,
       when a connector asks for it, its caches. No address keeps everything
       in process, which is the default -- nothing below is rendered then.
       DEX_VALKEY_ADDRESS stays for the single-node case; DEX_VALKEY_ADDRESSES
       takes a comma-separated list for sentinel and cluster. */}}
{{- $valkeyAddrs := getenv "DEX_VALKEY_ADDRESSES" (getenv "DEX_VALKEY_ADDRESS" "") }}
{{- if $valkeyAddrs }}
valkey:
  mode: {{ getenv "DEX_VALKEY_MODE" "standalone" }}
  addresses:
{{- range (strings.Split "," $valkeyAddrs) }}
  - {{ . | strings.TrimSpace }}
{{- end }}
{{- if getenv "DEX_VALKEY_MASTER_SET" "" }}
  masterSet: {{ .Env.DEX_VALKEY_MASTER_SET | quote }}
{{- end }}
  keyPrefix: {{ getenv "DEX_VALKEY_KEY_PREFIX" "dex:" | quote }}
```

El resto del bloque (username, password, db, tls) se mantiene igual.

- [ ] **Step 2: Comprobar que el ejemplo sigue arrancando**

```bash
docker compose -f Ejemplos/dashboard/docker-compose.yml up -d --build
sleep 8
docker logs dex 2>&1 | grep -i valkey
docker restart dex-dashboard   # comparte netns con dex; ver CLAUDE.md
```
Expected: `config valkey addresses=[valkey:6379] mode=standalone`, sin errores.

- [ ] **Step 3: Corregir la entrada del CHANGELOG, no añadir una**

`valkey.address` está en `[Unreleased]` y no ha salido en ninguna etiqueta. En la línea
de **Añadido**, cambiar `` `valkey.address` `` por
`` `valkey.addresses` y `valkey.mode` `` y añadir a continuación:

```markdown
- Valkey en alta disponibilidad: `mode: sentinel` con `masterSet`, o `mode: cluster`
  con varias direcciones. `valkey-go` sigue el failover de sentinel y las
  redirecciones del cluster sin que dex tenga que hacer nada. El modo es explícito
  porque deducirlo del número de direcciones falla en silencio.
```

- [ ] **Step 4: Ampliar `documentacion/valkey.md`**

Sustituir el apartado «Alta disponibilidad», que hoy dice que solo se admite una
dirección, por las tres topologías, con el ejemplo de configuración de cada una y la
regla de los tres votantes de sentinel. Añadir a la sección de configuración el `mode`
y `masterSet`.

- [ ] **Step 5: Commit**

```bash
git add config.docker.yaml Ejemplos/ documentacion/valkey.md CHANGELOG.md
git commit -m "docs: modo y varias direcciones en la imagen, el ejemplo y las guias

DEX_VALKEY_ADDRESS se queda para el caso de un nodo. La entrada del CHANGELOG se
corrige en vez de anadir otra: valkey.address esta en Unreleased y no ha salido
en ninguna etiqueta, asi que no hay nada que mantener."
```

---

## Task 7: MariaDB, verificada de una vez

**Files:**
- Modify: `docker-compose.yaml` (descomentar y añadir un servicio `mariadb`)
- Modify: `documentacion/valkey.md` no; `documentacion/despliegue-ansible.md` todavía no
  existe — el resultado va al cuerpo del commit y a `DONE.md` en la Task 13.

**Interfaces:** ninguna. Es una verificación.

**Por qué:** todo apunta a que funciona —el dialecto MySQL es conservador y no usa nada
exclusivo de MySQL 8— pero **es una inferencia**, y el despliegue entero se apoya en
ella.

- [ ] **Step 1: Añadir el servicio**

En `docker-compose.yaml`, junto a `mysql` y `mysql8`:

```yaml
    mariadb:
        image: mariadb:11.4
        environment:
            MARIADB_DATABASE: dex
            MARIADB_USER: mysql
            MARIADB_PASSWORD: mysql
            MARIADB_ROOT_PASSWORD: root
        ports:
            - 3307:3306
```

- [ ] **Step 2: Correr la suite de almacenamiento contra ella**

```bash
docker compose -f docker-compose.yaml up -d mariadb
sleep 15
DEX_MYSQL_HOST=127.0.0.1 DEX_MYSQL_PORT=3307 DEX_MYSQL_DATABASE=dex \
DEX_MYSQL_USER=root DEX_MYSQL_PASSWORD=root \
  go test ./storage/sql/ -v
```
Expected: PASS. **Si falla, parar y anotar exactamente qué falla**: eso cambia la fase 4
del despliegue y es más valioso que el arreglo.

- [ ] **Step 3: Probar también una versión anterior a la 11.1**

Repetir con `image: mariadb:10.11`, que es donde entra el camino de compatibilidad
`tx_isolation` de `storage/sql/config.go:289`.
Expected: PASS, y en el log `reconnecting with MySQL pre-5.7.20 compatibility mode`.

- [ ] **Step 4: Commit con el resultado**

```bash
git add docker-compose.yaml
git commit -m "test(storage): verificar el fork contra mariadb

Estaba dado por bueno por inferencia --el dialecto mysql de storage/sql es
conservador y no usa nada exclusivo de mysql 8-- y el despliegue entero se
apoyaba en esa inferencia. Verificado contra mariadb 11.4 y 10.11; en la 10.11
entra el camino de compatibilidad de tx_isolation, que es justo lo que estaba
sin probar.

<pegar aqui el resumen real de las dos ejecuciones>"
```

---

## Task 8: El limitador del panel, compartido entre réplicas

**Files:**
- Modify: `cmd/dex-dashboard/auth.go:194,260,494-545`
- Test: `cmd/dex-dashboard/auth_test.go`

**Interfaces:**
- Consumes: `valkey.NewFixedWindow(c, "dl")` de la Task 3.
- Produces: `newAttemptLimiter(limit int, window time.Duration, shared *dexvalkey.Client) *attemptLimiter`.

**Por qué:** el TODO justificaba que fuera local diciendo que con una sola réplica del
panel el límite por proceso ya es el límite real. Era cierto. Replicar el panel lo deja
de ser, y su límite de login pasa a valer `intentos × réplicas`.

- [ ] **Step 1: Escribir el test que falla**

```go
// Two dashboard replicas must share one login budget. Without this, replicating
// the panel multiplies its throttle by the number of instances -- the same hole
// the shared state closed in dex.
func TestTwoDashboardsShareOneLoginBudget(t *testing.T) {
	m := miniredis.RunT(t)

	newLimiter := func() *attemptLimiter {
		c, err := dexvalkey.New(t.Context(), dexvalkey.Config{
			Addresses: []string{m.Addr()}, KeyPrefix: "dex-dashboard:",
		})
		if err != nil {
			t.Fatalf("valkey client: %v", err)
		}
		t.Cleanup(c.Close)
		return newAttemptLimiter(2, time.Minute, c)
	}

	a, b := newLimiter(), newLimiter()

	if !a.allow(t.Context(), "10.0.0.1") || !b.allow(t.Context(), "10.0.0.1") {
		t.Fatal("the shared budget refused attempts inside the limit")
	}
	if a.allow(t.Context(), "10.0.0.1") {
		t.Error("the third attempt got through: each replica is counting on its own")
	}
}

// Without a shared store nothing changes: one process, its own map.
func TestTheLocalLimiterStillWorks(t *testing.T) {
	l := newAttemptLimiter(2, time.Minute, nil)
	ctx := t.Context()

	if !l.allow(ctx, "10.0.0.1") || !l.allow(ctx, "10.0.0.1") {
		t.Fatal("the local limiter refused attempts inside the limit")
	}
	if l.allow(ctx, "10.0.0.1") {
		t.Error("the local limiter stopped limiting")
	}
}

// A Valkey that stopped answering must not turn the throttle off. It degrades to
// one replica's worth of limit, never to none.
func TestTheDashboardLimiterFallsBackWhenValkeyIsDown(t *testing.T) {
	m := miniredis.RunT(t)
	c, err := dexvalkey.New(t.Context(), dexvalkey.Config{
		Addresses: []string{m.Addr()}, KeyPrefix: "dex-dashboard:",
	})
	if err != nil {
		t.Fatalf("valkey client: %v", err)
	}
	t.Cleanup(c.Close)

	l := newAttemptLimiter(2, time.Minute, c)
	m.Close()

	ctx := t.Context()
	if !l.allow(ctx, "10.0.0.1") || !l.allow(ctx, "10.0.0.1") {
		t.Fatal("the fallback refused attempts inside the limit")
	}
	if l.allow(ctx, "10.0.0.1") {
		t.Error("with Valkey down the dashboard stopped limiting logins")
	}
}
```

- [ ] **Step 2: Comprobar que falla**

Run: `go test ./cmd/dex-dashboard/ -run 'TestTwoDashboards|TestTheLocal|TestTheDashboardLimiter'`
Expected: FAIL — `too many arguments in call to newAttemptLimiter`.

- [ ] **Step 3: Implementar**

En `cmd/dex-dashboard/auth.go`:

```go
type attemptLimiter struct {
	limit  int
	window time.Duration

	// shared counts in Valkey so replicas of this panel share one budget. When
	// it is nil, or when it fails, the map below is used: that degrades to the
	// behavior of a single replica rather than to no limit at all.
	shared *dexvalkey.FixedWindow

	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newAttemptLimiter(limit int, window time.Duration, vk *dexvalkey.Client) *attemptLimiter {
	l := &attemptLimiter{limit: limit, window: window, attempts: map[string][]time.Time{}}
	if vk != nil {
		l.shared = dexvalkey.NewFixedWindow(vk, "dl")
	}
	return l
}

// allow records an attempt from key and reports whether it may proceed.
func (l *attemptLimiter) allow(ctx context.Context, key string) bool {
	if l == nil {
		return true
	}
	if l.shared != nil {
		if n, err := l.shared.Incr(ctx, key, l.window); err == nil {
			return n <= int64(l.limit)
		}
		// Fall through: a Valkey outage must not turn the throttle off.
	}
	return l.allowLocal(key)
}

// allowLocal is the in-process window, used with no shared store and when the
// shared store cannot be reached.
func (l *attemptLimiter) allowLocal(key string) bool {
	// ... el cuerpo actual de allow, sin el "if l == nil" ...
}
```

En `newAuthenticator`, `loginLimiter: newAttemptLimiter(10, time.Minute, vk)` —`vk` ya es
un parámetro de esa función—, y en la línea 260, `a.loginLimiter.allow(r.Context(), clientAddr(r))`.

- [ ] **Step 4: Comprobar que pasa**

Run: `go test ./cmd/dex-dashboard/`
Expected: PASS. Los tests existentes de `TestAttemptLimiter` necesitan el tercer
argumento `nil` y el `ctx`.

- [ ] **Step 5: Sabotear**

Cambiar `return n <= int64(l.limit)` por `return true`.
Expected: FAIL en `TestTwoDashboardsShareOneLoginBudget`. Restaurar.

- [ ] **Step 6: Commit**

```bash
git add cmd/dex-dashboard/auth.go cmd/dex-dashboard/auth_test.go
git commit -m "fix(dashboard): contar los intentos de login en valkey

El TODO justificaba que el limitador fuera local diciendo que con una sola
replica del panel el limite por proceso ya es el limite real. Era cierto.
Replicar el panel lo deja de ser: su limite pasaria a valer intentos por
replicas, que es el agujero que el estado compartido cerro en dex.

Ante una caida de valkey cae al contador local: degrada a una replica, nunca a
sin limite."
```

---

## Task 9: El esqueleto de Ansible y los roles `dex` y `dex_dashboard`

**Files:**
- Create: `ansible/playbooks/dex.yml`, `ansible/inventories/ejemplo/hosts.yml`,
  `ansible/inventories/ejemplo/group_vars/all.yml`,
  `ansible/roles/dex/{defaults/main.yml,tasks/main.yml,templates/{config.yaml.j2,docker-compose.yml.j2}}`,
  `ansible/roles/dex_dashboard/{defaults/main.yml,tasks/main.yml,templates/{config.yaml.j2,docker-compose.yml.j2}}`,
  `ansible/.ansible-lint`
- Modify: `.pre-commit-config.yaml`

**Interfaces:**
- Consumes: las variables del inventario.
- Produces: las variables `dex_image`, `dex_issuer`, `dex_storage`, `dex_valkey`,
  `dex_config_dir` (por defecto `/etc/dex`), que el rol `valkey` de la Task 10 también lee.

**Los dos roles van juntos** porque son la misma forma —renderiza configuración, escribe
secretos a fichero, levanta un compose— y un revisor que aceptara uno aceptaría el otro.

- [ ] **Step 1: El inventario de ejemplo**

`ansible/inventories/ejemplo/hosts.yml`:

```yaml
# Inventario de ejemplo. Las tres topologías de Valkey están comentadas abajo.
all:
  children:
    valkey:
      hosts:
        valkey-1: {}
        valkey-2: {}
      vars:
        # standalone: una sola máquina en este grupo.
        # sentinel: varias, más el grupo valkey_sentinel con 3 votantes impares.
        # cluster: tres máquinas, seis procesos.
        valkey_topology: sentinel
        valkey_master_set: dex
    valkey_sentinel:
      # Tres votantes. Con solo dos máquinas de datos, el tercero va en un nodo
      # de dex: con dos, perder una deja un sentinel solo y no hay promoción.
      hosts:
        valkey-1: {}
        valkey-2: {}
        dex-1: {}
    dex:
      hosts:
        dex-1: {}
        dex-2: {}
      vars:
        dex_issuer: https://sso.interno/dex   # la URL del balanceador, no la del nodo
        dex_storage:
          type: mysql
          host: mariadb.interno
          port: 3306
          database: dex
          user: dex
    dex_dashboard:
      hosts:
        dex-1: {}
        dex-2: {}
```

`ansible/inventories/ejemplo/group_vars/all.yml`:

```yaml
# Cifrar con: ansible-vault encrypt ansible/inventories/ejemplo/group_vars/all.yml
#
# Ojo con el carácter '$': el feature flag expand_env de dex viene encendido y
# aplica os.ExpandEnv a todas las cadenas de su configuración. Una contraseña con
# '$' se expande y deja de ser la contraseña.
dex_image: ghcr.io/rasty94/dex:fork-v2.0.0
dex_storage_password: cambiame-sin-simbolo-dolar
dex_grpc_token: cambiame-tambien
dex_valkey_password: cambiame-igual
dex_session_cookie_key: treinta-y-dos-bytes-exactos-aqui
```

- [ ] **Step 2: El rol `dex`**

`ansible/roles/dex/tasks/main.yml`:

```yaml
- name: La base de datos tiene que responder antes de arrancar dex
  # Un mensaje de Ansible es mejor que un contenedor en bucle de reinicio.
  ansible.builtin.wait_for:
    host: "{{ dex_storage.host }}"
    port: "{{ dex_storage.port | default(3306) }}"
    timeout: 10
  register: db_probe
  failed_when: db_probe.failed
  changed_when: false

- name: Directorio de configuración
  ansible.builtin.file:
    path: "{{ dex_config_dir }}"
    state: directory
    owner: "1001"
    group: "1001"
    mode: "0750"

- name: Configuración de dex
  # El fichero ES el secreto: dex no sabe leer la contraseña de la base, la de
  # Valkey ni cookieEncryptionKey desde fichero aparte, y por entorno las enseña
  # docker inspect.
  ansible.builtin.template:
    src: config.yaml.j2
    dest: "{{ dex_config_dir }}/config.yaml"
    owner: "1001"
    group: "1001"
    mode: "0600"
  notify: reiniciar dex

- name: Certificado de la CA interna
  ansible.builtin.copy:
    content: "{{ internal_ca_cert }}"
    dest: "{{ dex_config_dir }}/internal-ca.pem"
    owner: "1001"
    group: "1001"
    mode: "0644"
  notify: reiniciar dex

- name: Compose
  ansible.builtin.template:
    src: docker-compose.yml.j2
    dest: "{{ dex_config_dir }}/docker-compose.yml"
    mode: "0644"
  notify: reiniciar dex

- name: Levantar dex
  community.docker.docker_compose_v2:
    project_src: "{{ dex_config_dir }}"
    state: present
```

`ansible/roles/dex/templates/docker-compose.yml.j2`:

```yaml
# Renderizado por Ansible. No editar aquí: los cambios se pierden en el próximo
# despliegue. La plantilla está en ansible/roles/dex/templates/.
services:
    dex:
        image: {{ dex_image }}
        container_name: dex
        restart: unless-stopped
        command: ["dex", "serve", "/etc/dex/config.yaml"]
        # Sin variables de entorno con secretos: van en config.yaml, modo 0600.
        volumes:
            - {{ dex_config_dir }}/config.yaml:/etc/dex/config.yaml:ro
            - {{ dex_config_dir }}/internal-ca.pem:/etc/dex/internal-ca.pem:ro
        ports:
            - "{{ dex_http_port | default(5556) }}:5556"
            - "{{ dex_grpc_port | default(5557) }}:5557"
            - "127.0.0.1:{{ dex_telemetry_port | default(5558) }}:5558"
        healthcheck:
            test: ["CMD", "wget", "-qO-", "http://127.0.0.1:5558/healthz"]
            interval: 10s
            timeout: 3s
            retries: 5
```

`ansible/roles/dex/templates/config.yaml.j2` renderiza `issuer`, `storage`, `valkey`
—con `mode`, `addresses` y `masterSet` sacados del grupo `valkey`—, `grpc` con su
`token` y su TLS, `telemetry`, `sessions` con `cookieEncryptionKey`, y `connectors`.

- [ ] **Step 3: El rol `dex_dashboard`**

La misma forma. La diferencia que importa: el token gRPC va a su propio fichero, porque
el panel **sí** sabe leerlo de ahí:

```yaml
- name: Token gRPC del panel
  ansible.builtin.copy:
    content: "{{ dex_grpc_token }}"
    dest: "{{ dashboard_config_dir }}/grpc-token"
    owner: "1001"
    group: "1001"
    mode: "0600"
  notify: reiniciar el panel
```

y en su `config.yaml.j2`, `dex: {tokenFile: /etc/dex-dashboard/grpc-token}`.

- [ ] **Step 4: El playbook**

`ansible/playbooks/dex.yml`:

```yaml
# El orden importa: dex se niega a arrancar sin Valkey, y con razón.
- name: Valkey
  hosts: valkey:valkey_sentinel
  roles: [valkey]

- name: Dex
  hosts: dex
  serial: 1        # de una en una: una actualización no deja el servicio abajo
  roles: [dex]

- name: Panel de administración
  hosts: dex_dashboard
  serial: 1
  roles: [dex_dashboard]
```

- [ ] **Step 5: Lint y sintaxis**

```bash
ansible-lint ansible/
ansible-playbook --syntax-check -i ansible/inventories/ejemplo/hosts.yml ansible/playbooks/dex.yml
```
Expected: sin avisos.

Añadir a `.pre-commit-config.yaml`:

```yaml
  - repo: https://github.com/ansible/ansible-lint
    rev: v25.9.2
    hooks:
      - id: ansible-lint
        files: ^ansible/
```

- [ ] **Step 6: Commit**

```bash
git add ansible/ .pre-commit-config.yaml
git commit -m "feat(ansible): esqueleto, inventario de ejemplo y los roles de dex y del panel

El compose lo renderiza el rol, asi que el compose de produccion que pedia el
TODO y la plantilla del rol son el mismo artefacto y no se pueden desincronizar.
En la maquina queda un fichero que un operador puede leer sin ansible delante.

Los secretos no viajan por entorno: docker inspect los ensena. El fichero de
configuracion de dex es el secreto, modo 0600; el panel usa su tokenFile."
```

---

## Task 10: El rol `valkey` — standalone, y la regla de quién manda en el fichero

**Files:**
- Create: `ansible/roles/valkey/{defaults/main.yml,tasks/{main.yml,tls.yml,standalone.yml},templates/{managed.conf.j2,docker-compose.yml.j2},handlers/main.yml}`

**Interfaces:**
- Consumes: `valkey_topology`, `valkey_master_set`, `dex_valkey_password`.
- Produces: `internal_ca_cert` (contenido del certificado de la CA), que los roles de la
  Task 9 montan.

- [ ] **Step 1: La CA interna**

`ansible/roles/valkey/tasks/tls.yml`, con `community.crypto`. La clave de la CA se genera
y se queda en la máquina de control (`delegate_to: localhost`, `run_once: true`); a cada
nodo van solo su certificado, su clave y el certificado de la CA.

- [ ] **Step 2: `managed.conf.j2`, que es lo único que Ansible posee**

```
# Renderizado por Ansible en cada despliegue. Lo escribe el rol; lo incluye
# valkey.conf, que es de Valkey.
port 0
tls-port {{ valkey_port | default(6379) }}
tls-cert-file /etc/valkey/tls/node.crt
tls-key-file /etc/valkey/tls/node.key
tls-ca-cert-file /etc/valkey/tls/ca.crt
tls-replication yes
{% if valkey_topology == 'cluster' %}
tls-cluster yes
cluster-enabled yes
cluster-config-file /data/nodes.conf
{% endif %}
requirepass {{ dex_valkey_password }}
masterauth {{ dex_valkey_password }}

# Todo lo que dex guarda aquí lleva caducidad, así que volatile-* se llevaría
# justo sus claves: los contadores del limitador de login y las sesiones del
# panel. noeviction rechaza escrituras en vez de tirar claves.
maxmemory-policy noeviction
maxmemory {{ valkey_maxmemory | default('512mb') }}

# Un master que reinicia con el conjunto vacío y vuelve a ser master replica ese
# vacío a los demás.
appendonly yes
```

- [ ] **Step 3: `valkey.conf`, que Ansible crea una vez y no vuelve a tocar**

```yaml
- name: valkey.conf, creado una sola vez
  # Valkey reescribe su propia configuración: sentinel hace CONFIG REWRITE en
  # cada failover. Un rol que sobrescriba este fichero le devuelve al nodo un
  # replicaof caducado y deshace la promoción. El include va en la primera línea
  # a propósito: las directivas posteriores ganan, así que lo que escriba Valkey
  # después manda.
  ansible.builtin.copy:
    content: "include /etc/valkey/managed.conf\n"
    dest: "{{ valkey_config_dir }}/valkey.conf"
    force: false
    mode: "0640"
```

- [ ] **Step 4: El compose, con red de host**

```yaml
services:
    valkey:
        image: valkey/valkey:8-alpine
        container_name: valkey
        restart: unless-stopped
        # Cluster y sentinel se anuncian por IP real. Dentro de una red bridge
        # anunciarían la IP del contenedor, que desde otra máquina no existe.
        network_mode: host
        command: ["valkey-server", "/etc/valkey/valkey.conf"]
        volumes:
            - {{ valkey_config_dir }}:/etc/valkey:ro
            - valkey-data:/data
volumes:
    valkey-data:
```

- [ ] **Step 5: Probar la idempotencia, que es el criterio de verdad**

```bash
ansible-playbook -i <inventario-de-una-maquina> ansible/playbooks/dex.yml
ansible-playbook -i <inventario-de-una-maquina> ansible/playbooks/dex.yml
```
Expected: la segunda pasada, `changed=0`. Si `valkey.conf` sale como `changed`, el
`force: false` no está y la Task 11 va a romper failovers.

- [ ] **Step 6: Commit**

```bash
git add ansible/roles/valkey/
git commit -m "feat(ansible): rol de valkey en standalone, con su CA y sus dos ficheros

La regla que hace idempotente el rol: ansible escribe managed.conf y no es
dueno de valkey.conf. Valkey reescribe su propia configuracion --sentinel hace
CONFIG REWRITE en cada failover-- y un rol que sobrescriba ese fichero devuelve
un replicaof caducado y deshace la promocion.

Red de host porque el gossip anuncia IPs reales: dentro de una bridge anunciaria
direcciones de contenedor que desde otra maquina no existen."
```

---

## Task 11: El rol `valkey` — sentinel

**Files:**
- Create: `ansible/roles/valkey/tasks/sentinel.yml`,
  `ansible/roles/valkey/templates/sentinel.conf.j2`

**Interfaces:**
- Consumes: el grupo `valkey_sentinel`, `valkey_master_set`.
- Produces: nada nuevo.

- [ ] **Step 1: La comprobación del quórum, antes que nada**

```yaml
- name: Sentinel necesita un número impar de votantes, y al menos tres
  # Con dos, perder una máquina deja un sentinel solo: no alcanza quórum y no
  # hay promoción. Es un failover que no funciona el día que hace falta, y
  # fallar aquí es mejor que descubrirlo entonces.
  ansible.builtin.assert:
    that:
      - groups['valkey_sentinel'] | length >= 3
      - groups['valkey_sentinel'] | length is odd
    fail_msg: >-
      El grupo valkey_sentinel tiene {{ groups['valkey_sentinel'] | length }}
      votantes. Hacen falta al menos 3 y un número impar. Con dos máquinas de
      datos, pon el tercer sentinel en un nodo de dex.
  run_once: true
```

- [ ] **Step 2: Quién es el master, y solo la primera vez**

```yaml
- name: ¿Sabe algún sentinel quién es el master?
  ansible.builtin.command: >-
    valkey-cli -p {{ valkey_sentinel_port | default(26379) }}
    sentinel get-master-addr-by-name {{ valkey_master_set }}
  register: known_master
  changed_when: false
  failed_when: false
  delegate_to: "{{ groups['valkey_sentinel'] | first }}"
  run_once: true

- name: El primer host del grupo es el master, solo en el arranque inicial
  # Después de esto manda sentinel. El rol no vuelve a asignar el papel de
  # nadie: reasignar en cada pasada deshace las promociones.
  ansible.builtin.set_fact:
    valkey_master_host: >-
      {{ (known_master.stdout_lines | first)
         if known_master.stdout_lines | length > 0
         else groups['valkey'] | first }}
  run_once: true
```

- [ ] **Step 3: `sentinel.conf`, también creado una sola vez**

Con `force: false`, y con `down-after-milliseconds` y `failover-timeout` como variables.
Los cambios posteriores van por `SENTINEL SET`, no reescribiendo el fichero.

- [ ] **Step 4: Probarlo sobre dos máquinas y un tercer votante**

Levantar tres máquinas de usar y tirar, desplegar, y comprobar:

```bash
valkey-cli -p 26379 sentinel master dex          # quién es el master
docker stop valkey                                # en la máquina del master
sleep 15
valkey-cli -p 26379 sentinel master dex          # tiene que ser otra
ansible-playbook -i <inventario> ansible/playbooks/dex.yml
valkey-cli -p 26379 sentinel master dex          # y seguir siendo la nueva
```
Expected: la última comprobación devuelve la máquina promovida, **no** la primera del
inventario. Ese es el test de que el rol no deshace failovers. Guardar la transcripción.

- [ ] **Step 5: Commit**

```bash
git add ansible/roles/valkey/tasks/sentinel.yml ansible/roles/valkey/templates/sentinel.conf.j2
git commit -m "feat(ansible): topologia sentinel, con quorum comprobado y sin deshacer failovers

El rol falla si los votantes son pares o menos de tres, en vez de dejar montado
un failover que no funciona el dia que haga falta. El primer host del inventario
es el master solo en el arranque inicial: despues se pregunta a los sentinels
quien lo es, porque reasignar el papel en cada pasada deshace las promociones.

Verificado parando el master y volviendo a pasar el playbook: el master sigue
siendo el promovido.

<pegar aqui la transcripcion>"
```

---

## Task 12: El rol `valkey` — cluster

**Files:**
- Create: `ansible/roles/valkey/tasks/cluster.yml`

**Interfaces:**
- Consumes: el grupo `valkey`, `valkey_port`, `valkey_replica_port`.
- Produces: nada nuevo.

- [ ] **Step 1: Detectar antes de tocar**

```yaml
- name: ¿Está el cluster formado?
  ansible.builtin.command: valkey-cli -p {{ valkey_port }} cluster info
  register: cluster_info
  changed_when: false
  failed_when: false

- name: Un cluster a medias se para y se avisa, no se repara
  # Reparar automáticamente un cluster medio formado es como se pierden datos.
  ansible.builtin.assert:
    that: >-
      ('cluster_state:ok' in cluster_info.stdout)
      or ('cluster_known_nodes:1' in cluster_info.stdout)
    fail_msg: >-
      {{ inventory_hostname }} conoce a otros nodos pero el cluster no está en
      estado ok. No se toca nada: revisa 'valkey-cli --cluster check' a mano.
```

- [ ] **Step 2: Crearlo una sola vez**

```yaml
- name: Formar el cluster
  # Solo cuando ningún nodo conoce a nadie. Repetir --cluster create sobre un
  # cluster vivo destruye la asignación de slots.
  ansible.builtin.command: >-
    valkey-cli --cluster create
    {% for h in groups['valkey'] %}{{ hostvars[h].ansible_host }}:{{ valkey_port }} {% endfor %}
    {% for h in groups['valkey'] %}{{ hostvars[h].ansible_host }}:{{ valkey_replica_port }} {% endfor %}
    --cluster-replicas 1 --cluster-yes
  when: "'cluster_known_nodes:1' in cluster_info.stdout"
  run_once: true
  delegate_to: "{{ groups['valkey'] | first }}"
```

- [ ] **Step 3: Comprobar el reparto**

```yaml
- name: Ninguna réplica puede caer en la máquina de su master
  ansible.builtin.command: valkey-cli -p {{ valkey_port }} --cluster check 127.0.0.1:{{ valkey_port }}
  register: cluster_check
  changed_when: false
  run_once: true
```

Y revisar a mano la salida la primera vez: `--cluster create` reparte réplicas evitando
la colocalización cuando puede, pero con tres máquinas y seis nodos conviene verlo.

- [ ] **Step 4: Probarlo sobre tres máquinas**

```bash
ansible-playbook -i <inventario-cluster> ansible/playbooks/dex.yml
ansible-playbook -i <inventario-cluster> ansible/playbooks/dex.yml   # changed=0
valkey-cli -p 7001 cluster info | grep cluster_state                  # ok
docker stop valkey   # en una máquina
sleep 20
valkey-cli -p 7001 cluster info | grep cluster_state                  # sigue ok
```
Expected: la segunda pasada no cambia nada y el cluster sobrevive a perder una máquina.
Guardar la transcripción.

- [ ] **Step 5: Commit**

```bash
git add ansible/roles/valkey/tasks/cluster.yml
git commit -m "feat(ansible): topologia cluster, con bootstrap idempotente

Se detecta antes de tocar: si el estado es ok no se hace nada, si nadie conoce a
nadie se crea, y si aparece medio formado el rol para y avisa. Repetir
--cluster create sobre un cluster vivo destruye la asignacion de slots, y
reparar uno a medias automaticamente es como se pierden datos.

<pegar aqui la transcripcion>"
```

---

## Task 13: Documentación y cierre

**Files:**
- Create: `documentacion/despliegue-ansible.md`
- Modify: `TODO.md`, `DONE.md`, `CHANGELOG.md`, `documentacion/valkey.md`

- [ ] **Step 1: `documentacion/despliegue-ansible.md`**

Cubre: requisitos (Docker en los nodos, una base MariaDB/MySQL existente), el inventario
y sus grupos, las tres topologías con cuándo usar cada una, cómo se cifran los secretos,
la actualización con `serial: 1`, **qué tiene que hacer el balanceador** —repartir entre
los nodos de dex y del panel, comprobar `/healthz`, y **no** hacer afinidad de sesión— y
la advertencia del `$` en las contraseñas por `expand_env`.

Incluye también lo que **no** cubre: la sonda de disponibilidad del panel sigue siendo
una entrada abierta del TODO, y `/healthz` responde 200 sin mirar si dex o Valkey
contestan.

- [ ] **Step 2: Mover a `DONE.md` lo cerrado**

Se cierran: «Rol de Ansible», «Un `docker compose` de producción» y «Valkey es hoy un
punto único de fallo». Cada una con **lo que destapó probarla**, que es la parte que
vale.

Se **corrige**, no se cierra, la entrada del `attemptLimiter` del panel: su razonamiento
era correcto y dejó de serlo por un cambio nuestro.

- [ ] **Step 3: Anotar lo que sigue abierto**

En `TODO.md`: las fases 3, 4 y 5 con enlace a la hoja de ruta; la sonda de
disponibilidad del panel; y `ent` con MariaDB como no soportado.

- [ ] **Step 4: `graphify update .`**

- [ ] **Step 5: Commit**

```bash
git add documentacion/ TODO.md DONE.md CHANGELOG.md
git commit -m "docs: guia del despliegue con ansible y cierre de las tres entradas del todo"
```

---

## Autorrevisión

**Cobertura de la spec.** §4.1-4.5 → Tasks 1, 2 y 6. §4.3 validación → Task 1.
§5.1 topologías → Tasks 10, 11 y 12. §5.2 propiedad del fichero → Task 10 paso 3.
§5.3 bootstrap → Task 12. §5.4 red → Task 10 paso 4. §5.5 TLS → Task 10 paso 1.
§5.6 ajustes → Task 10 paso 2. §6.1 secretos → Task 9. §6.2 comprobaciones → Task 9.
§6.3 orden → Task 9 paso 4. §6.4 detalles → Task 9. §6.5 panel replicado → Task 8.
§7 inventario → Task 9 paso 1. §8.1 tests de código → Tasks 4 y 5. §8.2 lo renderizado →
Task 9 paso 5 y Task 10 paso 5. §8.3 gossip a mano → Tasks 11 y 12. §8.4 MariaDB →
Task 7. §9 documentación → Tasks 6 y 13.

**Sin hueco**, salvo uno consciente: la spec menciona el TLS del gRPC entre el panel y
dex y el plan lo deja dentro de las plantillas de la Task 9 sin desarrollarlo paso a
paso. Es el mismo material de certificados que la Task 10 ya genera.

**Consistencia de nombres.** `Config.Addresses`, `Config.Mode`, `Config.MasterSet`,
`ModeStandalone/Sentinel/Cluster`, `NewFixedWindow(c, kind)`, `Incr`, `Reset`,
`newAttemptLimiter(limit, window, vk)`, `allow(ctx, key)`. Coinciden entre tareas.

**Orden de dependencias.** 1 → 2 → 3 → 4 → 5 son código y van en cadena. 6 depende de
1 y 2. **7 no depende de nada**: es una verificación de MariaDB y puede adelantarse si
conviene saber pronto su resultado, porque un fallo ahí cambia la fase 4. 8 depende de 3.
9 depende de 6. 10 → 11 → 12 en cadena. 13 al final.
