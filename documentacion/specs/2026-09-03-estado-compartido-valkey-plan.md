# Estado compartido con Valkey — Plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Que el limitador de login, la caché de tokens de Keystone y las sesiones del panel puedan compartirse entre réplicas a través de Valkey, sin dejar de funcionar en memoria cuando no hay Valkey configurado.

**Architecture:** Un cliente compartido en `pkg/valkey` y, en cada una de las tres piezas, una implementación que cumple la interfaz local que esa pieza ya tiene. No se añade ninguna abstracción común: el limitador usa `INCR` en un script Lua, la caché `SET`/`GET`, y las sesiones `GETEX`. Sin `valkey.address` no cambia nada.

**Tech Stack:** Go 1.25.8, `github.com/valkey-io/valkey-go` (Apache-2.0), `github.com/alicebob/miniredis/v2` (MIT, solo test), `golang.org/x/time/rate` (se conserva para el camino local).

**Spec:** [2026-09-03-estado-compartido-valkey.md](2026-09-03-estado-compartido-valkey.md)

## Global Constraints

- Sin `valkey.address` el comportamiento es idéntico al de hoy: nada de dependencias en ejecución, ni de conexiones.
- Con `valkey.address` puesto, dex y el panel hacen `PING` al arrancar y **se niegan a arrancar si falla**. Los fallos en ejecución degradan según la tabla de la spec; los de arranque no.
- Ninguna clave lleva un secreto ni datos personales en el nombre: tokens, nombres de usuario e identificadores de sesión van con SHA-256 en hexadecimal.
- Código, comentarios y mensajes de log en inglés; ortografía americana (`misspell` lo exige en CI). Los commits, en español y sin tildes.
- Cada test se sabotea una vez: revertir el arreglo y comprobar que falla por lo que importa.
- `miniredis` no implementa el seguimiento de invalidación del servidor: todos los clientes de test se construyen con `DisableCache: true`.
- No hacer `git push`: los commits se quedan locales hasta que el usuario lo autorice.

---

### Task 1: `pkg/valkey` — cliente compartido y configuración

**Files:**
- Create: `pkg/valkey/valkey.go`
- Create: `pkg/valkey/registry.go`
- Test: `pkg/valkey/valkey_test.go`

**Interfaces:**
- Consumes: nada.
- Produces:
  - `valkey.Config{Address, Username, Password, DB int, KeyPrefix string, TLS TLSConfig}`
  - `valkey.TLSConfig{CACert string, InsecureSkipVerify bool}`
  - `func New(ctx context.Context, cfg Config) (*Client, error)` — devuelve `(nil, nil)` si `Address` está vacío.
  - `type Client struct { valkey.Client; prefix string }` — el cliente de valkey-go va embebido, así que `c.Do(ctx, c.B()...)` y `c.Close()` funcionan directamente.
  - `func (c *Client) Key(name string) string` — antepone el prefijo.
  - `func (c *Client) HashKey(kind, secret string) string` — `prefijo + kind + ":" + sha256hex(secret)`.
  - `func SetShared(c *Client)` / `func Shared() *Client` — registro de paquete para el conector.

- [ ] **Step 1: Añadir las dependencias**

```bash
go get github.com/valkey-io/valkey-go@v1.0.77
go get github.com/alicebob/miniredis/v2@v2.39.0
go mod tidy
```

- [ ] **Step 2: Escribir el test que falla**

Crear `pkg/valkey/valkey_test.go`:

```go
package valkey

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// An empty address is how an operator says "keep everything in memory". It has
// to be a nil client and not an error, because every caller treats nil as
// "there is no shared store" and falls back to its local implementation.
func TestNoAddressMeansNoClient(t *testing.T) {
	c, err := New(t.Context(), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c != nil {
		t.Fatal("an empty address must not open a connection")
	}
}

func TestNewPingsAndPrefixes(t *testing.T) {
	m := miniredis.RunT(t)
	c, err := New(t.Context(), Config{Address: m.Addr(), KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	if got := c.Key("rl:abc"); got != "dex:rl:abc" {
		t.Errorf("Key = %q, want dex:rl:abc", got)
	}
}

// The key must not carry the secret it identifies: a Valkey shared with other
// services would otherwise list live tokens and usernames as key names.
func TestHashKeyHidesTheSecret(t *testing.T) {
	m := miniredis.RunT(t)
	c, err := New(t.Context(), Config{Address: m.Addr(), KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	const secret = "gAAAAABlive-keystone-token"
	got := c.HashKey("tok", secret)

	if strings.Contains(got, secret) {
		t.Errorf("the key carries the secret verbatim: %q", got)
	}
	if !strings.HasPrefix(got, "dex:tok:") {
		t.Errorf("HashKey = %q, want the dex:tok: prefix", got)
	}
	if got != c.HashKey("tok", secret) {
		t.Error("HashKey is not stable, so a lookup could never hit")
	}
}

// A configured address that does not answer is a startup error, not a silent
// fallback: an operator who asked for a shared store must not discover during
// an incident that it was never shared.
func TestUnreachableAddressFailsToStart(t *testing.T) {
	m := miniredis.RunT(t)
	addr := m.Addr()
	m.Close()

	if _, err := New(context.Background(), Config{Address: addr}); err == nil {
		t.Fatal("an unreachable address must fail to start")
	}
}
```

- [ ] **Step 3: Ejecutar el test y comprobar que falla**

Run: `go test ./pkg/valkey/ -run TestNoAddress -v`
Expected: FAIL con `undefined: New`.

- [ ] **Step 4: Escribir la implementación mínima**

Crear `pkg/valkey/valkey.go`:

```go
// Package valkey opens the connection dex shares with its replicas. It is not a
// cache abstraction: each caller uses the commands its own problem needs, and
// this package only owns the connection, the key prefix, and the rule that no
// key ever carries a secret in its name.
package valkey

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"os"

	valkeygo "github.com/valkey-io/valkey-go"
)

// Config is the shared store. An empty Address keeps every caller on its own
// in-process state, which is the default and needs no server at all.
type Config struct {
	Address   string    `json:"address"`
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

// Client is the shared connection. The valkey-go client is embedded, so callers
// build commands with c.B() and run them with c.Do() as usual.
//
// A nil *Client means "no shared store": every caller checks for it and uses its
// own in-memory implementation instead. Do not call methods on a nil Client.
type Client struct {
	valkeygo.Client

	prefix string
}

// New opens and verifies the connection. An empty address returns (nil, nil):
// that is the configuration saying everything stays in memory.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Address == "" {
		return nil, nil
	}

	opt := valkeygo.ClientOption{
		InitAddress: []string{cfg.Address},
		Username:    cfg.Username,
		Password:    cfg.Password,
		SelectDB:    cfg.DB,
	}
	if cfg.TLS.CACert != "" || cfg.TLS.InsecureSkipVerify {
		tlsCfg := &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		}
		if cfg.TLS.CACert != "" {
			pem, err := os.ReadFile(cfg.TLS.CACert)
			if err != nil {
				return nil, fmt.Errorf("read valkey caCert: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("valkey caCert %q holds no certificate", cfg.TLS.CACert)
			}
			tlsCfg.RootCAs = pool
		}
		opt.TLSConfig = tlsCfg
	}

	c, err := valkeygo.NewClient(opt)
	if err != nil {
		return nil, fmt.Errorf("connect to valkey: %w", err)
	}
	// Prove it answers now rather than on the first login.
	if err := c.Do(ctx, c.B().Ping().Build()).Error(); err != nil {
		c.Close()
		return nil, fmt.Errorf("ping valkey: %w", err)
	}
	return &Client{Client: c, prefix: cfg.KeyPrefix}, nil
}

// Key namespaces a key that carries nothing secret.
func (c *Client) Key(name string) string {
	return c.prefix + name
}

// HashKey namespaces a key derived from something that must not appear in the
// store: a Keystone token, a username, a session id.
func (c *Client) HashKey(kind, secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return c.prefix + kind + ":" + hex.EncodeToString(sum[:])
}
```

Crear `pkg/valkey/registry.go`:

```go
package valkey

import "sync/atomic"

// shared is the process-wide client, set once at startup.
//
// ponytail: package-level state, which is the price of connector.Open(id,
// logger) having nowhere to inject a dependency. The alternative was giving the
// Keystone connector its own valkey block, and connector configuration is
// stored in the database — the password would end up in the connector store and
// in the dashboard's edit form.
var shared atomic.Pointer[Client]

// SetShared publishes the client for components that cannot be handed one.
func SetShared(c *Client) { shared.Store(c) }

// Shared returns the client set at startup, or nil when none was configured.
func Shared() *Client { return shared.Load() }
```

- [ ] **Step 5: Ejecutar los tests y comprobar que pasan**

Run: `go test ./pkg/valkey/ -v`
Expected: PASS, los cuatro.

- [ ] **Step 6: Sabotear una vez**

Cambiar `HashKey` para que devuelva `c.prefix + kind + ":" + secret`. `TestHashKeyHidesTheSecret` debe fallar con «the key carries the secret verbatim». Deshacer.

- [ ] **Step 7: Commit**

```bash
git add pkg/valkey go.mod go.sum
git commit -m "feat(valkey): cliente compartido para el estado entre replicas"
```

---

### Task 2: El limitador de login cuenta en Valkey

**Files:**
- Modify: `server/ratelimit/ratelimit.go`
- Create: `server/ratelimit/valkey.go`
- Modify: `server/authflow/password.go:170`, `server/grants/password.go:65`
- Test: `server/ratelimit/valkey_test.go`, y actualizar `server/ratelimit/ratelimit_test.go`

**Interfaces:**
- Consumes: `pkg/valkey` `*Client`, `HashKey`.
- Produces:
  - `func (l *Limiter) Allow(ctx context.Context, key string) bool` — **cambia la firma**, antes era `Allow(key string) bool`.
  - `func (l *Limiter) Reset(ctx context.Context, key string)` — igual.
  - `func (l *Limiter) SetSharedStore(c *dexvalkey.Client)` — al estilo del `SetRejectedCounter` que ya existe.

- [ ] **Step 1: Escribir el test que falla**

Crear `server/ratelimit/valkey_test.go`:

```go
package ratelimit

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

func sharedLimiter(t *testing.T, addr string, attempts int) *Limiter {
	t.Helper()
	c, err := dexvalkey.New(t.Context(), dexvalkey.Config{Address: addr, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("valkey client: %v", err)
	}
	t.Cleanup(c.Close)

	l := New(Config{Enabled: true, Attempts: attempts, Window: time.Minute}, nil)
	l.SetSharedStore(c)
	return l
}

// The whole point of the change: two replicas must share one budget. With local
// buckets each instance would start from zero and the effective limit would be
// attempts x replicas, which is the security hole this closes.
func TestTwoLimitersShareOneBudget(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	a := sharedLimiter(t, m.Addr(), 3)
	b := sharedLimiter(t, m.Addr(), 3)

	for i := range 3 {
		if !a.Allow(ctx, "1.2.3.4\x00jane") {
			t.Fatalf("attempt %d refused by the first replica", i+1)
		}
	}
	if b.Allow(ctx, "1.2.3.4\x00jane") {
		t.Error("the second replica granted a fourth attempt: the budget is not shared")
	}
}

// A successful login clears the counter, on every replica.
func TestResetClearsTheSharedCounter(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	a := sharedLimiter(t, m.Addr(), 1)
	b := sharedLimiter(t, m.Addr(), 1)

	a.Allow(ctx, "k")
	if b.Allow(ctx, "k") {
		t.Fatal("the budget was not shared to begin with")
	}
	a.Reset(ctx, "k")
	if !b.Allow(ctx, "k") {
		t.Error("Reset did not clear the shared counter")
	}
}

// The window has to expire, or one burst locks a user out forever.
func TestTheSharedWindowExpires(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	l := sharedLimiter(t, m.Addr(), 1)
	l.Allow(ctx, "k")
	if l.Allow(ctx, "k") {
		t.Fatal("the second attempt should have been refused")
	}

	m.FastForward(time.Minute + time.Second)
	if !l.Allow(ctx, "k") {
		t.Error("the window never expired")
	}
}

// Valkey being down must degrade to today's behavior -- local buckets -- and not
// to "no limit at all".
func TestValkeyDownFallsBackToLocalBuckets(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	l := sharedLimiter(t, m.Addr(), 2)
	m.Close()

	if !l.Allow(ctx, "k") || !l.Allow(ctx, "k") {
		t.Fatal("the local fallback refused attempts inside the budget")
	}
	if l.Allow(ctx, "k") {
		t.Error("with Valkey down the limiter stopped limiting")
	}
}
```

- [ ] **Step 2: Ejecutar y comprobar que falla**

Run: `go test ./server/ratelimit/ -run TestTwoLimitersShare -v`
Expected: FAIL con `l.SetSharedStore undefined` y `too many arguments in call to a.Allow`.

- [ ] **Step 3: Escribir el backend**

Crear `server/ratelimit/valkey.go`:

```go
package ratelimit

import (
	"context"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
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

// sharedCounter is the fixed window kept in Valkey.
//
// This is not the token bucket used locally: x/time/rate keeps its state in
// process and cannot be shared. A fixed window allows up to 2 x attempts across
// a window boundary, which is the accepted trade for a login throttle and is
// correct across replicas -- the point of having it at all.
type sharedCounter struct {
	c *dexvalkey.Client
}

func (s *sharedCounter) incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	k := s.c.HashKey("rl", key)
	ms := strconv.FormatInt(window.Milliseconds(), 10)
	return fixedWindow.Exec(ctx, s.c.Client, []string{k}, []string{ms}).AsInt64()
}

func (s *sharedCounter) reset(ctx context.Context, key string) error {
	k := s.c.HashKey("rl", key)
	return s.c.Do(ctx, s.c.B().Del().Key(k).Build()).Error()
}
```

Añadir `"strconv"` a los imports de ese fichero.

- [ ] **Step 4: Enganchar el backend al limitador**

En `server/ratelimit/ratelimit.go`:

1. Añadir a la estructura `Limiter`, junto a `rejected`:

```go
	// shared counts in Valkey instead of the local buckets. When it is nil, or
	// when it fails, the buckets below are used: that degrades to the behavior
	// of a single replica rather than to no limit at all.
	shared *sharedCounter
	window time.Duration

	// Counts falls back to the local buckets. Without it a Valkey that started
	// refusing connections looks exactly like one that is working: the limiter
	// keeps limiting, just per replica again.
	backendErrors prometheus.Counter
```

2. En `New`, tras normalizar la configuración, guardar `window: cfg.Window` en el `Limiter` que se devuelve.

3. Reemplazar el comentario `ponytail:` de la estructura por:

```go
// Limiter throttles login attempts keyed by client IP and username. Only failed
// attempts count: a successful login clears the counter for that key, so a
// legitimate user, or a service using the password grant, is never throttled.
//
// With SetSharedStore the counting happens in Valkey, so several replicas share
// one budget. Without it the buckets are in process and the effective limit is
// Attempts x replicas.
```

4. Añadir el emparejador de `SetRejectedCounter`:

```go
// SetSharedStore makes the limiter count in Valkey, so replicas share a budget.
func (l *Limiter) SetSharedStore(c *dexvalkey.Client) {
	if l == nil || c == nil {
		return
	}
	l.shared = &sharedCounter{c: c}
}

// SetBackendErrorCounter counts the times the shared store could not be reached
// and the local buckets took over.
func (l *Limiter) SetBackendErrorCounter(c prometheus.Counter) {
	if l == nil {
		return
	}
	l.backendErrors = c
}
```

5. Cambiar `Allow` y `Reset`:

```go
// Allow reports whether another login attempt may be made for key.
func (l *Limiter) Allow(ctx context.Context, key string) bool {
	if l == nil {
		return true
	}

	if l.shared != nil {
		n, err := l.shared.incr(ctx, key, l.window)
		if err == nil {
			if n <= int64(l.burst) {
				return true
			}
			if l.rejected != nil {
				l.rejected.Inc()
			}
			return false
		}
		// Fall through to the local buckets: a Valkey outage must not turn the
		// limiter off. It is counted, because otherwise a store that stopped
		// answering is indistinguishable from one that works.
		if l.backendErrors != nil {
			l.backendErrors.Inc()
		}
	}

	return l.allowLocal(key)
}

// allowLocal is the in-process token bucket, used when there is no shared store
// and when the shared store cannot be reached.
func (l *Limiter) allowLocal(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		l.sweep(now)
		b = &bucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now

	if b.limiter.AllowN(now, 1) {
		return true
	}
	if l.rejected != nil {
		l.rejected.Inc()
	}
	return false
}

// Reset forgets the failed attempts recorded for key.
func (l *Limiter) Reset(ctx context.Context, key string) {
	if l == nil {
		return
	}
	if l.shared != nil {
		_ = l.shared.reset(ctx, key)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}
```

`burst` ya vale `cfg.Attempts`; comprobarlo en `New` antes de usarlo como techo de la ventana.

- [ ] **Step 5: Actualizar los dos puntos de llamada**

`server/authflow/password.go:170`:

```go
		limitKey := ratelimit.Key(ctx, username)
		if !h.LoginLimiter.Allow(ctx, limitKey) {
```

`server/grants/password.go:65`:

```go
	limitKey := ratelimit.Key(ctx, req.Username)
	if !g.limiter.Allow(ctx, limitKey) {
```

Buscar también las llamadas a `Reset` en esos dos ficheros y pasarles `ctx`:

```bash
grep -rn "\.Reset(\|\.Allow(" server/authflow/password.go server/grants/password.go
```

- [ ] **Step 6: Actualizar los tests que ya existían**

En `server/ratelimit/ratelimit_test.go`, `TestLimiter`, `TestLimiterDisabled`, `TestLimiterDefaults` y `TestLimiterEvictsIdleBuckets` llaman a `Allow(key)` y `Reset(key)`. Pasarles `t.Context()` como primer argumento. No cambia lo que comprueban.

- [ ] **Step 7: Ejecutar todo y comprobar que pasa**

Run: `go test ./server/ratelimit/ ./server/authflow/ ./server/grants/ -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 8: Sabotear una vez**

Quitar el `if l.shared != nil` de `Allow` para que siempre use los cubos locales. `TestTwoLimitersShareOneBudget` debe fallar con «the second replica granted a fourth attempt». Deshacer.

- [ ] **Step 9: Commit**

```bash
git add server/ratelimit server/authflow/password.go server/grants/password.go
git commit -m "feat(ratelimit): contar los intentos en valkey para compartirlos entre replicas"
```

---

### Task 3: Cablear Valkey en dex

**Files:**
- Modify: `cmd/dex/config.go` (estructura `Config`, sobre `LoginRateLimit`)
- Modify: `cmd/dex/serve.go` (crear el cliente, registrarlo, pasarlo al limitador)
- Modify: `server/config.go` (nuevo campo)
- Modify: `server/server.go:239` (pasarlo al limitador)
- Test: `cmd/dex/config_test.go`

**Interfaces:**
- Consumes: `valkey.New`, `valkey.SetShared`, `Limiter.SetSharedStore`.
- Produces: campo `Valkey dexvalkey.Config` en la configuración de dex y en `server.Config`.

- [ ] **Step 1: Escribir el test que falla**

Añadir a `cmd/dex/config_test.go`:

```go
// The shared store is configuration, not a feature flag: an operator turns it on
// by naming an address.
func TestUnmarshalValkeyConfig(t *testing.T) {
	rawConfig := []byte(`
issuer: http://127.0.0.1:5556/dex
storage:
  type: sqlite3
  config:
    file: /var/dex/dex.db
valkey:
  address: valkey:6379
  username: dex
  password: secret
  keyPrefix: "dex:"
  tls:
    caCert: /etc/ssl/valkey-ca.pem
`)

	var c Config
	if err := yaml.Unmarshal(rawConfig, &c); err != nil {
		t.Fatalf("failed to decode config: %v", err)
	}
	if c.Valkey.Address != "valkey:6379" {
		t.Errorf("address = %q", c.Valkey.Address)
	}
	if c.Valkey.KeyPrefix != "dex:" {
		t.Errorf("keyPrefix = %q", c.Valkey.KeyPrefix)
	}
	if c.Valkey.TLS.CACert != "/etc/ssl/valkey-ca.pem" {
		t.Errorf("caCert = %q", c.Valkey.TLS.CACert)
	}
}
```

- [ ] **Step 2: Ejecutar y comprobar que falla**

Run: `go test ./cmd/dex/ -run TestUnmarshalValkeyConfig -v`
Expected: FAIL con `c.Valkey undefined`.

- [ ] **Step 3: Añadir el campo a la configuración de dex**

En `cmd/dex/config.go`, dentro de `Config`, justo antes de `LoginRateLimit`:

```go
	// Valkey is the store dex shares with its replicas: the login rate limiter's
	// counters and, when a connector asks for it, its caches. Leaving the
	// address empty keeps all of that in process, which is the default.
	Valkey dexvalkey.Config `json:"valkey"`
```

Importar `dexvalkey "github.com/dexidp/dex/pkg/valkey"`.

- [ ] **Step 4: Añadir el campo a `server.Config`**

En `server/config.go`, junto a `LoginRateLimit`:

```go
	// Valkey is the shared store, or nil when everything stays in process.
	Valkey *dexvalkey.Client
```

- [ ] **Step 5: Usarlo al construir el limitador**

En `server/server.go`, tras `loginLimiter := ratelimit.New(c.LoginRateLimit, rc.now)`:

```go
	loginLimiter.SetSharedStore(c.Valkey)
```

`SetSharedStore` ya tolera receptor nil y cliente nil, así que no hace falta guardia.

Y junto al registro de `dex_login_rate_limited_total`, que ya existe unas líneas más abajo, registrar el contador de caídas al camino local:

```go
		backendErrors := prometheus.NewCounter(prometheus.CounterOpts{
			Name: "dex_login_rate_limit_backend_errors_total",
			Help: "Number of times the shared rate limit store could not be reached and the local buckets were used.",
		})
		if err := c.PrometheusRegistry.Register(backendErrors); err == nil {
			loginLimiter.SetBackendErrorCounter(backendErrors)
		}
```

- [ ] **Step 6: Abrir la conexión al arrancar**

En `cmd/dex/serve.go`, antes de construir `serverConfig` (junto al resto de la configuración, cerca de la línea 439):

```go
	valkeyClient, err := dexvalkey.New(ctx, c.Valkey)
	if err != nil {
		return fmt.Errorf("valkey: %v", err)
	}
	if valkeyClient != nil {
		defer valkeyClient.Close()
		// Published for components that cannot be handed a dependency: see
		// pkg/valkey/registry.go.
		dexvalkey.SetShared(valkeyClient)
		logger.Info("config valkey", "address", c.Valkey.Address, "key_prefix", c.Valkey.KeyPrefix)
	}
	serverConfig.Valkey = valkeyClient
```

Comprobar que la variable `ctx` existe en ese ámbito; si no, usar la que `runServe` ya tenga o `context.Background()`.

- [ ] **Step 7: Ejecutar los tests**

Run: `go test ./cmd/dex/ ./server/ 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 8: Comprobar que arranca en los dos modos**

```bash
go build -o /tmp/dex-valkey ./cmd/dex
/tmp/dex-valkey serve config.dev.yaml 2>&1 | head -5   # sin valkey: arranca igual
```

Luego añadir temporalmente `valkey: {address: "127.0.0.1:1"}` a `config.dev.yaml` y comprobar que **no arranca**, con un error que nombre valkey. Deshacer el cambio en el fichero.

- [ ] **Step 9: Commit**

```bash
git add cmd/dex server/config.go server/server.go
git commit -m "feat(dex): abrir la conexion de valkey al arrancar y darsela al limitador"
```

---

### Task 4: Arreglar la fuga de la caché de Keystone

Esta tarea no tiene que ver con las réplicas: arregla un fallo que existe hoy con una sola. Va antes de compartir la caché porque estrecha el tipo, que es lo que permite serializarla.

**Files:**
- Modify: `connector/keystone/cache.go`
- Modify: `connector/keystone/keystone.go:33,180-196,297-302,377-379`
- Test: `connector/keystone/cache_test.go`

**Interfaces:**
- Produces:
  - `type identityCache interface { get(ctx context.Context, token string) (connector.Identity, bool); set(ctx context.Context, token string, id connector.Identity) }`
  - `func newTimeCache(ttl time.Duration) *timeCache` — sin cambios de firma.
  - El campo `conn.tokenCache` pasa de `*timeCache` a `identityCache`.

- [ ] **Step 1: Escribir el test que falla**

Crear `connector/keystone/cache_test.go`:

```go
package keystone

import (
	"strings"
	"testing"
	"time"

	"github.com/dexidp/dex/connector"
)

// The cache checked expiry on read but never deleted, so an entry for a token
// that is never seen again stayed forever. Keystone tokens rotate, so that is
// unbounded growth in a long-running process -- with a single replica.
func TestExpiredEntriesAreDropped(t *testing.T) {
	c := newTimeCache(time.Minute)
	ctx := t.Context()

	for i := range 100 {
		c.set(ctx, "token-"+string(rune('a'+i%26))+string(rune('a'+i/26)), connector.Identity{UserID: "u"})
	}
	if n := c.len(); n != 100 {
		t.Fatalf("stored %d entries, want 100", n)
	}

	c.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	c.set(ctx, "fresh", connector.Identity{UserID: "u"})

	if n := c.len(); n != 1 {
		t.Errorf("%d entries survived their TTL; only the fresh one should remain", n)
	}
}

func TestCacheRoundTripsAnIdentity(t *testing.T) {
	c := newTimeCache(time.Minute)
	ctx := t.Context()

	want := connector.Identity{UserID: "u-1", Email: "jane@example.com", Groups: []string{"admins"}}
	c.set(ctx, "tok", want)

	got, ok := c.get(ctx, "tok")
	if !ok {
		t.Fatal("the entry just written was not found")
	}
	if got.UserID != want.UserID || got.Email != want.Email || len(got.Groups) != 1 {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A misspelled cacheTTL used to disable the cache and say nothing, so an
// operator who asked for caching got none and had no way to notice.
func TestAMisspelledCacheTTLIsRefused(t *testing.T) {
	_, err := (&Config{Host: "http://keystone", CacheTTL: "5min"}).Open("kc", testLogger())
	if err == nil {
		t.Fatal("a cacheTTL that does not parse was accepted")
	}
	if !strings.Contains(err.Error(), "cacheTTL") {
		t.Errorf("the error does not name the field: %v", err)
	}
}

func TestExpiredEntryIsAMiss(t *testing.T) {
	c := newTimeCache(time.Minute)
	ctx := t.Context()

	c.set(ctx, "tok", connector.Identity{UserID: "u"})
	c.now = func() time.Time { return time.Now().Add(2 * time.Minute) }

	if _, ok := c.get(ctx, "tok"); ok {
		t.Error("an expired entry was served")
	}
}
```

- [ ] **Step 2: Ejecutar y comprobar que falla**

Run: `go test ./connector/keystone/ -run TestExpiredEntriesAreDropped -v`
Expected: FAIL con `c.len undefined` y `too many arguments in call to c.set`.

- [ ] **Step 3: Reescribir la caché**

Reemplazar el contenido de `connector/keystone/cache.go`:

```go
package keystone

import (
	"context"
	"sync"
	"time"

	"github.com/dexidp/dex/connector"
)

// identityCache is what the connector needs from a cache: an identity for a
// Keystone token. Both the in-process cache and the shared one satisfy it.
type identityCache interface {
	get(ctx context.Context, token string) (connector.Identity, bool)
	set(ctx context.Context, token string, id connector.Identity)
}

type cacheEntry struct {
	value     connector.Identity
	expiresAt time.Time
}

// timeCache holds identities in this process. Entries are dropped on write
// rather than only ignored on read: Keystone tokens rotate, so a cache that
// never deletes grows for as long as the process runs.
type timeCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
	now     func() time.Time
}

func newTimeCache(ttl time.Duration) *timeCache {
	return &timeCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
		now:     time.Now,
	}
}

func (c *timeCache) set(_ context.Context, key string, value connector.Identity) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.sweep(now)
	c.entries[key] = cacheEntry{
		value:     value,
		expiresAt: now.Add(c.ttl),
	}
}

func (c *timeCache) get(_ context.Context, key string) (connector.Identity, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok || c.now().After(entry.expiresAt) {
		return connector.Identity{}, false
	}
	return entry.value, true
}

// sweep drops expired entries. Callers must hold c.mu.
func (c *timeCache) sweep(now time.Time) {
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
		}
	}
}

// len reports how many entries are held, for the tests that check the sweep.
func (c *timeCache) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
```

- [ ] **Step 4: Adaptar el conector**

En `connector/keystone/keystone.go`:

1. Línea 33, en la estructura `conn`: `tokenCache identityCache`.
2. Línea ~180, en `Open`, el bloque queda así. **Ojo al cambio de comportamiento**: hoy dice `if err == nil && importTime > 0`, de modo que un `cacheTTL` mal escrito desactiva la caché **sin decir nada**. Pasa a ser un error de arranque, y se declara en el CHANGELOG de la Task 7:

```go
	var tokenCache identityCache
	if c.CacheTTL != "" {
		importTime, err := time.ParseDuration(c.CacheTTL)
		if err != nil {
			return nil, fmt.Errorf("invalid cacheTTL %q: %v", c.CacheTTL, err)
		}
		if importTime <= 0 {
			return nil, fmt.Errorf("cacheTTL must be positive, got %q", c.CacheTTL)
		}
		tokenCache = newTimeCache(importTime)
	}
```

Comprobar que `fmt` está entre los imports del fichero.
3. Línea ~298: `if cached, ok := p.tokenCache.get(ctx, subjectToken); ok {` y devolver `cached` directamente, sin la aserción `cached.(connector.Identity)`.
4. Línea ~378: `p.tokenCache.set(ctx, subjectToken, identity)`.

- [ ] **Step 5: Ejecutar y comprobar que pasa**

Run: `go test ./connector/keystone/ -v 2>&1 | tail -10`
Expected: PASS, incluidos los tests que ya existían.

- [ ] **Step 6: Sabotear una vez**

Quitar la llamada a `c.sweep(now)` de `set`. `TestExpiredEntriesAreDropped` debe fallar con «100 entries survived their TTL». Deshacer.

- [ ] **Step 7: Commit**

```bash
git add connector/keystone
git commit -m "fix(keystone): la cache de tokens no borraba nunca lo caducado"
```

---

### Task 5: La caché de Keystone, compartida

**Files:**
- Create: `connector/keystone/cache_valkey.go`
- Modify: `connector/keystone/keystone.go` (campo `CacheShared` en `Config`, y `Open`)
- Test: `connector/keystone/cache_valkey_test.go`

**Interfaces:**
- Consumes: `identityCache` (Task 4), `valkey.Shared()`, `valkey.HashKey`.
- Produces: `cacheShared bool` en `keystone.Config` (`json:"cacheShared"`).

- [ ] **Step 1: Escribir el test que falla**

Crear `connector/keystone/cache_valkey_test.go`:

```go
package keystone

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/dexidp/dex/connector"
	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

func sharedCache(t *testing.T, addr string, ttl time.Duration) *valkeyCache {
	t.Helper()
	c, err := dexvalkey.New(t.Context(), dexvalkey.Config{Address: addr, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("valkey client: %v", err)
	}
	t.Cleanup(c.Close)
	return newValkeyCache(c, ttl)
}

// One replica validates a token against Keystone; the others must not have to.
func TestASecondReplicaHitsWhatTheFirstCached(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	a := sharedCache(t, m.Addr(), time.Minute)
	b := sharedCache(t, m.Addr(), time.Minute)

	want := connector.Identity{UserID: "u-1", Email: "jane@example.com", Groups: []string{"admins"}}
	a.set(ctx, "keystone-token", want)

	got, ok := b.get(ctx, "keystone-token")
	if !ok {
		t.Fatal("the second replica missed what the first stored")
	}
	if got.UserID != want.UserID || got.Email != want.Email || len(got.Groups) != 1 {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The raw token must never be a key name: a Valkey shared with other services
// would be listing live credentials.
func TestTheTokenIsNotAKeyName(t *testing.T) {
	m := miniredis.RunT(t)
	c := sharedCache(t, m.Addr(), time.Minute)

	c.set(t.Context(), "gAAAAAB-live-token", connector.Identity{UserID: "u"})

	for _, k := range m.Keys() {
		if k == "gAAAAAB-live-token" || k == "dex:gAAAAAB-live-token" {
			t.Fatalf("the raw token is a key name: %q", k)
		}
	}
}

func TestTheSharedEntryExpires(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	c := sharedCache(t, m.Addr(), time.Minute)
	c.set(ctx, "tok", connector.Identity{UserID: "u"})

	m.FastForward(time.Minute + time.Second)
	if _, ok := c.get(ctx, "tok"); ok {
		t.Error("an expired entry was served")
	}
}

// A cache is an optimization. If Valkey is gone the login still has to work, so
// every failure is a miss and never an error.
func TestValkeyDownIsAMissAndNotAnError(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	c := sharedCache(t, m.Addr(), time.Minute)
	c.set(ctx, "tok", connector.Identity{UserID: "u"})
	m.Close()

	if _, ok := c.get(ctx, "tok"); ok {
		t.Error("a dead Valkey reported a hit")
	}
	c.set(ctx, "tok", connector.Identity{UserID: "u"}) // must not panic
}
```

- [ ] **Step 2: Ejecutar y comprobar que falla**

Run: `go test ./connector/keystone/ -run TestASecondReplica -v`
Expected: FAIL con `undefined: valkeyCache`.

- [ ] **Step 3: Escribir la caché compartida**

Crear `connector/keystone/cache_valkey.go`:

```go
package keystone

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dexidp/dex/connector"
	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

// valkeyCache holds identities where every replica can see them.
//
// The key is a hash of the Keystone token, never the token itself: this store
// may be shared with other services, and key names are the first thing anyone
// with access sees. The value is the identity as JSON -- personal data, which is
// why the deployment documentation says what lives in here.
type valkeyCache struct {
	c   *dexvalkey.Client
	ttl time.Duration
}

func newValkeyCache(c *dexvalkey.Client, ttl time.Duration) *valkeyCache {
	return &valkeyCache{c: c, ttl: ttl}
}

func (v *valkeyCache) get(ctx context.Context, token string) (connector.Identity, bool) {
	raw, err := v.c.Do(ctx, v.c.B().Get().Key(v.c.HashKey("tok", token)).Build()).AsBytes()
	if err != nil {
		// Missing key, or Valkey unreachable. Both are a cache miss: a login is
		// never failed because the optimization is unavailable.
		return connector.Identity{}, false
	}
	var id connector.Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		return connector.Identity{}, false
	}
	return id, true
}

func (v *valkeyCache) set(ctx context.Context, token string, id connector.Identity) {
	raw, err := json.Marshal(id)
	if err != nil {
		return
	}
	_ = v.c.Do(ctx, v.c.B().Set().
		Key(v.c.HashKey("tok", token)).
		Value(string(raw)).
		Ex(v.ttl).
		Build()).Error()
}
```

- [ ] **Step 4: Añadir la opción al conector**

En `connector/keystone/keystone.go`, en `Config`, tras `CacheTTL`:

```go
	// CacheShared puts the token cache in the shared store instead of this
	// process, so replicas do not each revalidate the same token. It decides
	// where the cache lives, not whether there is one: the lifetime is still
	// CacheTTL, and without CacheTTL there is no cache either way.
	CacheShared bool `json:"cacheShared"`
```

Y en `Open`, donde hoy se construye la caché:

El bloque queda como lo dejó la Task 4, más la rama compartida:

```go
	var tokenCache identityCache
	if c.CacheTTL != "" {
		importTime, err := time.ParseDuration(c.CacheTTL)
		if err != nil {
			return nil, fmt.Errorf("invalid cacheTTL %q: %v", c.CacheTTL, err)
		}
		if importTime <= 0 {
			return nil, fmt.Errorf("cacheTTL must be positive, got %q", c.CacheTTL)
		}
		if c.CacheShared {
			shared := dexvalkey.Shared()
			if shared == nil {
				// Asking for a shared cache and silently getting a local one is
				// the kind of quiet difference that is only discovered during an
				// incident.
				return nil, errors.New("cacheShared is set but no valkey address is configured")
			}
			tokenCache = newValkeyCache(shared, importTime)
		} else {
			tokenCache = newTimeCache(importTime)
		}
	}
```

`errors` tiene que estar entre los imports.

- [ ] **Step 5: Ejecutar y comprobar que pasa**

Run: `go test ./connector/keystone/ -v 2>&1 | tail -12`
Expected: PASS.

- [ ] **Step 6: Sabotear una vez**

En `valkeyCache.get`/`set`, cambiar `v.c.HashKey("tok", token)` por `v.c.Key(token)`. `TestTheTokenIsNotAKeyName` debe fallar nombrando la clave. Deshacer.

- [ ] **Step 7: Commit**

```bash
git add connector/keystone
git commit -m "feat(keystone): cache de tokens compartida entre replicas"
```

---

### Task 6: Las sesiones del panel

**Files:**
- Create: `cmd/dex-dashboard/sessions_valkey.go`
- Modify: `cmd/dex-dashboard/auth.go:46-110,167` (interfaz y construcción)
- Modify: `cmd/dex-dashboard/config.go` (bloque `valkey`)
- Test: `cmd/dex-dashboard/sessions_valkey_test.go`

**Interfaces:**
- Consumes: `pkg/valkey`.
- Produces:
  - `type sessions interface { create(ctx context.Context, s *session) (string, error); get(ctx context.Context, id string) (*session, bool); delete(ctx context.Context, id string) }`
  - `func newValkeySessions(c *dexvalkey.Client, ttl, idleTTL time.Duration) *valkeySessions`
  - Campo `Valkey dexvalkey.Config` en la `Config` del panel.

- [ ] **Step 1: Escribir el test que falla**

Crear `cmd/dex-dashboard/sessions_valkey_test.go`:

```go
package main

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

func valkeySessionsFor(t *testing.T, addr string, ttl, idle time.Duration) *valkeySessions {
	t.Helper()
	c, err := dexvalkey.New(t.Context(), dexvalkey.Config{Address: addr, KeyPrefix: "dex-dashboard:"})
	if err != nil {
		t.Fatalf("valkey client: %v", err)
	}
	t.Cleanup(c.Close)
	return newValkeySessions(c, ttl, idle)
}

// A replicated panel must not sign an administrator out just because the load
// balancer moved them to another instance.
func TestASessionCreatedOnOneReplicaWorksOnAnother(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	a := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	b := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)

	id, err := a.create(ctx, &session{Email: "admin@example.com", CanWrite: true, Groups: []string{"dex-admins"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, ok := b.get(ctx, id)
	if !ok {
		t.Fatal("the second replica did not know the session")
	}
	if got.Email != "admin@example.com" || !got.CanWrite {
		t.Errorf("session came back as %+v", got)
	}
}

// The write permission travels through the store, so it has to survive the round
// trip exactly: a session that loses CanWrite locks an administrator out, and one
// that gains it is a privilege escalation.
func TestWritePermissionSurvivesTheRoundTrip(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	id, err := s.create(ctx, &session{Email: "viewer@example.com", CanWrite: false})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, ok := s.get(ctx, id)
	if !ok {
		t.Fatal("session not found")
	}
	if got.CanWrite {
		t.Error("a read-only session came back with write permission")
	}
}

// The idle timeout is what makes an abandoned console stop working. Valkey keeps
// it, so a session left alone past the idle window must be gone.
func TestIdleSessionsExpire(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	id, err := s.create(ctx, &session{Email: "admin@example.com"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	m.FastForward(31 * time.Minute)
	if _, ok := s.get(ctx, id); ok {
		t.Error("an idle session survived its window")
	}
}

// Reading refreshes the idle window, or an administrator working steadily would
// be logged out mid-task.
func TestUsingASessionKeepsItAlive(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	id, _ := s.create(ctx, &session{Email: "admin@example.com"})

	for range 3 {
		m.FastForward(20 * time.Minute)
		if _, ok := s.get(ctx, id); !ok {
			t.Fatal("a session in continuous use was dropped")
		}
	}
}

func TestDeleteEndsTheSessionEverywhere(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	a := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	b := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)

	id, _ := a.create(ctx, &session{Email: "admin@example.com"})
	a.delete(ctx, id)

	if _, ok := b.get(ctx, id); ok {
		t.Error("logging out on one replica left the session valid on another")
	}
}

// With the store gone the panel cannot tell a real session from an invented one,
// so it must refuse. Failing closed here costs a login; failing open costs the
// panel.
func TestValkeyDownRefusesTheSession(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	id, _ := s.create(ctx, &session{Email: "admin@example.com"})
	m.Close()

	if _, ok := s.get(ctx, id); ok {
		t.Error("a session was accepted with the store unreachable")
	}
}
```

- [ ] **Step 2: Ejecutar y comprobar que falla**

Run: `go test ./cmd/dex-dashboard/ -run TestASessionCreatedOnOneReplica -v`
Expected: FAIL con `undefined: valkeySessions`.

- [ ] **Step 3: Escribir el almacén compartido**

Crear `cmd/dex-dashboard/sessions_valkey.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"time"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

// valkeySessions keeps administrator sessions where every replica sees them.
//
// The idle timeout is the key's TTL, refreshed on each read, so there is no
// LastSeen to write back on every request. The absolute lifetime caps that
// refresh: a session in constant use still ends when its Expiry passes.
//
// What is stored decides who may change things -- CanWrite and Groups travel in
// here -- so write access to this store is write access to the panel. The
// deployment documentation says so next to the address.
type valkeySessions struct {
	c       *dexvalkey.Client
	ttl     time.Duration
	idleTTL time.Duration
	now     func() time.Time
}

func newValkeySessions(c *dexvalkey.Client, ttl, idleTTL time.Duration) *valkeySessions {
	return &valkeySessions{c: c, ttl: ttl, idleTTL: idleTTL, now: time.Now}
}

// window is how long the key should live from now: the idle timeout, or what is
// left of the absolute lifetime when that is shorter.
func (v *valkeySessions) window(sess *session) time.Duration {
	remaining := sess.Expiry.Sub(v.now())
	if remaining <= 0 {
		return 0
	}
	if v.idleTTL > 0 && v.idleTTL < remaining {
		return v.idleTTL
	}
	return remaining
}

func (v *valkeySessions) create(ctx context.Context, sess *session) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	now := v.now()
	sess.Expiry = now.Add(v.ttl)
	sess.AuthAt = now
	sess.LastSeen = now

	raw, err := json.Marshal(sess)
	if err != nil {
		return "", err
	}
	if err := v.c.Do(ctx, v.c.B().Set().
		Key(v.c.HashKey("sess", id)).
		Value(string(raw)).
		Ex(v.window(sess)).
		Build()).Error(); err != nil {
		return "", err
	}
	return id, nil
}

func (v *valkeySessions) get(ctx context.Context, id string) (*session, bool) {
	key := v.c.HashKey("sess", id)

	raw, err := v.c.Do(ctx, v.c.B().Get().Key(key).Build()).AsBytes()
	if err != nil {
		// Unknown id, or the store is unreachable. Either way the panel cannot
		// vouch for this session, so it asks for a login.
		return nil, false
	}
	var sess session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, false
	}

	w := v.window(&sess)
	if w <= 0 {
		v.delete(ctx, id)
		return nil, false
	}
	// Push the idle window out, capped by the absolute expiry.
	_ = v.c.Do(ctx, v.c.B().Pexpire().Key(key).Milliseconds(w.Milliseconds()).Build()).Error()

	sess.LastSeen = v.now()
	return &sess, true
}

func (v *valkeySessions) delete(ctx context.Context, id string) {
	_ = v.c.Do(ctx, v.c.B().Del().Key(v.c.HashKey("sess", id)).Build()).Error()
}
```

- [ ] **Step 4: Poner la interfaz delante de las dos implementaciones**

En `cmd/dex-dashboard/auth.go`:

1. Declarar la interfaz sobre `sessionStore`:

```go
// sessions is what the panel needs from a session store: the in-process one and
// the shared one both satisfy it.
type sessions interface {
	create(ctx context.Context, sess *session) (string, error)
	get(ctx context.Context, id string) (*session, bool)
	delete(ctx context.Context, id string)
}
```

2. Añadir `ctx context.Context` como primer parámetro a `sessionStore.create`, `get` y `delete`, ignorándolo con `_`.
3. Cambiar el campo del autenticador (línea ~132) de `sessions *sessionStore` a `sessions sessions`.
4. `newAuthenticator` tiene hoy la firma `newAuthenticator(ctx context.Context, c *Config, logger *slog.Logger)` y su único llamador está en `cmd/dex-dashboard/main.go:187`. Añadirle el cliente:

```go
func newAuthenticator(ctx context.Context, c *Config, vk *dexvalkey.Client, logger *slog.Logger) (*authenticator, error) {
```

y en la construcción (línea ~167) elegir implementación:

```go
		sessions: sessionsFor(c, vk),
```

con:

```go
// sessionsFor picks where administrator sessions live. Without a valkey address
// they stay in this process, which is what a single panel wants: nothing to
// encrypt, nothing to rotate, and a restart just asks for a fresh login.
func sessionsFor(c *Config, vk *dexvalkey.Client) sessions {
	if vk != nil {
		return newValkeySessions(vk, c.Admin.SessionTTL, c.Admin.IdleTTL)
	}
	return newSessionStore(c.Admin.SessionTTL, c.Admin.IdleTTL)
}
```

5. Actualizar las llamadas: `grep -rn "\.sessions\.\(create\|get\|delete\)(" cmd/dex-dashboard/` y pasar `r.Context()` donde haya petición, o `context.Background()` donde no.

- [ ] **Step 5: Añadir el bloque de configuración y abrir la conexión**

En `cmd/dex-dashboard/config.go`, dentro de `Config`:

```go
	// Valkey shares administrator sessions between replicas of this panel.
	// Leaving the address empty keeps them in this process.
	//
	// Whatever can write here can grant itself write permission on the panel:
	// the session carries CanWrite. Authenticate the connection.
	Valkey dexvalkey.Config `json:"valkey"`
```

Con `keyPrefix` por defecto `dex-dashboard:` cuando venga vacío, para que un Valkey compartido con dex no mezcle claves. Ponerlo donde el panel valida y completa su configuración.

En `cmd/dex-dashboard/main.go`, antes de la llamada de la línea 187, abrir el cliente y cerrarlo al salir, con el mismo trato que en dex —si falla el `PING`, no arranca— y pasarlo al constructor:

```go
	vk, err := dexvalkey.New(ctx, c.Valkey)
	if err != nil {
		return fmt.Errorf("valkey: %w", err)
	}
	if vk != nil {
		defer vk.Close()
		logger.Info("config valkey", "address", c.Valkey.Address, "key_prefix", c.Valkey.KeyPrefix)
	}

	auth, err := newAuthenticator(ctx, c, vk, logger)
```

Comprobar la forma exacta en la que ese ámbito devuelve errores y ajustar el `return`.

- [ ] **Step 6: Ejecutar y comprobar que pasa**

Run: `go test ./cmd/dex-dashboard/ -v 2>&1 | tail -15`
Expected: PASS, incluidos los tests del panel que ya existían.

- [ ] **Step 7: Sabotear una vez**

En `valkeySessions.get`, quitar la línea del `Pexpire`. `TestUsingASessionKeepsItAlive` debe fallar con «a session in continuous use was dropped». Deshacer.

- [ ] **Step 8: Commit**

```bash
git add cmd/dex-dashboard
git commit -m "feat(dashboard): sesiones de administrador compartidas entre replicas"
```

---

### Task 7: Ejemplo, documentación y comprobación en vivo

**Files:**
- Modify: `Ejemplos/dashboard/docker-compose.yml`, `Ejemplos/dashboard/dex.yaml`, `Ejemplos/dashboard/README.md`
- Modify: `config.dev.yaml`, `config.docker.yaml`, `config.dashboard.docker.yaml`
- Modify: `documentacion/dashboard-administracion.md`, `documentacion/keystone_connector.md`, `CHANGELOG.md`
- Modify: `TODO.md`, `DONE.md`

**Interfaces:**
- Consumes: todo lo anterior.

- [ ] **Step 1: Añadir Valkey y una segunda réplica al ejemplo**

En `Ejemplos/dashboard/docker-compose.yml`, un servicio nuevo:

```yaml
    valkey:
        image: valkey/valkey:8-alpine
        container_name: valkey
        # Sin contraseña porque no sale de la red del ejemplo. En un despliegue
        # de verdad esto lleva credenciales y TLS: quien escriba aquí puede
        # darse permiso de escritura en el panel.
        command: ["valkey-server", "--save", ""]
        restart: unless-stopped
```

Y una segunda réplica de dex que comparte Valkey pero tiene su propia base de datos, porque lo que se quiere demostrar es el limitador y ése no usa el almacenamiento:

```yaml
    dex-replica:
        image: dex-con-panel:local
        container_name: dex-replica
        command: ["dex", "serve", "/etc/dex/config.yaml"]
        depends_on:
            dex:
                condition: service_healthy
        environment:
            - DEX_API_CONNECTORS_CRUD=true
            - DEX_SESSIONS_ENABLED=true
            - DEX_API_SESSIONS_IDENTITIES_CRUD=true
        volumes:
            - ./dex.yaml:/etc/dex/config.yaml:ro
            - dex-replica-data:/var/dex
        ports:
            - "127.0.0.1:5566:5556"
```

Añadir `dex-replica-data:` a la lista de volúmenes, y a `Ejemplos/dashboard/dex.yaml`:

```yaml
valkey:
    address: valkey:6379
    keyPrefix: "dex:"
```

Nota: el servicio `dashboard` usa `network_mode: "service:dex"`, así que `valkey` es alcanzable como `valkey` desde dex y como `127.0.0.1` no. Comprobar los nombres de red al levantar.

- [ ] **Step 2: Comprobar en vivo que el límite se comparte**

```bash
cd Ejemplos/dashboard && docker compose up -d --build
```

Con `loginRateLimit` habilitado en `dex.yaml` (`attempts: 3`, `window: 1m`), agotar el límite contra la primera réplica y comprobar que la segunda ya lo rechaza:

```bash
for i in 1 2 3; do
  curl -s -o /dev/null -w "%{http_code} " -X POST \
    -d "login=pepe@example.com&password=mal" \
    "http://127.0.0.1:5556/dex/auth/local/login?back=&state=x"
done
echo "--- ahora la replica ---"
curl -s -o /dev/null -w "%{http_code}\n" -X POST \
  -d "login=pepe@example.com&password=mal" \
  "http://127.0.0.1:5566/dex/auth/local/login?back=&state=x"
```

El estado del formulario hay que sacarlo de una petición de auth real; usar el mismo enfoque de `urllib` con `CookieJar` que en las sesiones anteriores, no `requests`, que no está en el host. La réplica debe negarse **sin haber recibido ningún intento propio**.

- [ ] **Step 3: Documentar la configuración**

En `config.dev.yaml`, junto a los demás bloques comentados:

```yaml
# Estado compartido entre replicas. Sin address, cada proceso guarda lo suyo en
# memoria y no hay dependencia ninguna: es el valor por defecto.
#
# Lo que pasa a Valkey: los contadores del limitador de login --que sin esto
# valen attempts x replicas, o sea el limite multiplicado-- y, si un conector lo
# pide, sus caches. Las sesiones de navegador de dex NO viven aqui: estan en el
# almacenamiento desde siempre.
#
# Quien pueda escribir aqui influye en decisiones de seguridad. Autentica la
# conexion y ponle TLS.
#
# valkey:
#   address: valkey:6379
#   username: dex
#   password: a-long-random-string
#   db: 0
#   keyPrefix: "dex:"
#   tls:
#     caCert: /etc/ssl/valkey-ca.pem
```

En `config.docker.yaml`, un bloque renderizado con gomplate siguiendo el patrón de `sessions:`, gobernado por `DEX_VALKEY_ADDRESS` y omitido cuando está vacío:

```
{{- if getenv "DEX_VALKEY_ADDRESS" "" }}
valkey:
  address: {{ .Env.DEX_VALKEY_ADDRESS }}
  keyPrefix: {{ getenv "DEX_VALKEY_KEY_PREFIX" "dex:" | quote }}
{{- if getenv "DEX_VALKEY_USERNAME" "" }}
  username: {{ .Env.DEX_VALKEY_USERNAME | quote }}
{{- end }}
{{- if getenv "DEX_VALKEY_PASSWORD" "" }}
  password: {{ .Env.DEX_VALKEY_PASSWORD | quote }}
{{- end }}
{{- end }}
```

Y el mismo bloque en `config.dashboard.docker.yaml`, con las variables `DEX_DASHBOARD_VALKEY_*` y `dex-dashboard:` como prefijo por defecto, para que un Valkey compartido no mezcle las claves de los dos procesos.

- [ ] **Step 4: Documentar en las guías**

- `documentacion/dashboard-administracion.md`, en la sección de sesiones y cookies: que las sesiones pueden vivir en Valkey, que la caducidad por inactividad la lleva el propio almacén, y **la frase que importa**: quien pueda escribir en ese Valkey se hace administrador con permiso de escritura.
- `documentacion/keystone_connector.md`: `cacheShared`, qué guarda (correo y grupos, datos personales), que la clave va con hash, y que sin `cacheTTL` no hay caché de ninguna clase.

- [ ] **Step 5: CHANGELOG**

El cambio de semántica del limitador es observable y va anotado:

```markdown
### Cambiado

- El limitador de login puede contar en Valkey (`valkey.address`), de modo que
  varias replicas comparten un solo presupuesto. Sin configurarlo, nada cambia.
  Con Valkey, el algoritmo pasa de cubo de fichas a **ventana fija**, que permite
  hasta `2 x attempts` a caballo entre dos ventanas; a cambio el limite es
  correcto entre replicas, que sin esto valia `attempts x replicas`.

### Arreglado

- La cache de tokens del conector Keystone comprobaba la caducidad al leer pero
  nunca borraba, asi que crecia sin limite mientras el proceso viviera. Afecta
  tambien a despliegues de una sola replica.
- Un `cacheTTL` mal escrito en el conector Keystone desactivaba la cache en
  silencio. Ahora dex no arranca y dice cual es el valor que no entiende.
```

- [ ] **Step 6: Mover lo cerrado a DONE.md**

Quitar de `TODO.md` la sección 2 («Caché Distribuida») y «Sesiones compartidas entre réplicas» del panel, y llevarlas a `DONE.md` con lo que se aprendió. Actualizar la tabla de estado y la fecha. Dejar en `TODO.md` lo que no entró: la caché en cliente de `valkey-go`, y el `attemptLimiter` del panel, que sigue siendo local a propósito.

- [ ] **Step 7: Comprobación final y commit**

```bash
go build ./... && go test ./... 2>&1 | grep -v "^ok\|no test files" | head
pre-commit run --files $(git diff --name-only HEAD)
git add -A
git commit -m "docs: valkey en el ejemplo, en las guias y en el changelog"
```
