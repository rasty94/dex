# dex-dashboard

A read-only administration panel for a dex server.

> **Cómo funciona por dentro** — arquitectura, flujo de autenticación, sesiones,
> operación y diagnóstico: [documentacion/dashboard-administracion.md](../../documentacion/dashboard-administracion.md).

It is a **separate binary** from dex on purpose: dex is the identity provider, and
a bug in a management UI should not be a bug in the IdP. The dashboard talks to
dex over the gRPC API, keeps the admin token server-side, and authenticates its
own users against dex itself.

## What it shows

| View | Contents |
| ---- | -------- |
| Overview | dex version, counts of clients, local users and connectors |
| Clients | OAuth2 clients, their type and redirect URIs |
| Connectors | configured connectors (id, type, name) |
| Local users | dex's password DB |
| Sessions | a user's refresh tokens, looked up by `sub` |

**Nothing here can change dex's state.** Writes are phase two of the plan in
[TODO.md](../../TODO.md).

## What it is not

The **Local users** view lists dex's password DB and nothing else. Users who sign
in through Keystone, LDAP, GitHub or any other connector live in that provider;
dex neither creates nor deletes them, so they cannot be managed from here.

## Running it

```
go build ./cmd/dex-dashboard
./dex-dashboard --config dashboard.yaml
```

Start from [config.example.yaml](config.example.yaml). Three things have to line
up before it will start:

1. **dex's gRPC API is enabled** and reachable at `dex.grpcAddress`.
2. **The dashboard is registered as a client in dex**, with
   `<baseURL>/callback` among its `redirectURIs`.
3. **`admin.groups` or `admin.emails` is set.** The dashboard refuses to start
   without one: an admin panel that admits every authenticated user has no gate
   at all.

The connector view additionally needs dex's `api_connectors_crud` feature flag;
without it that view reports the flag rather than failing the whole page.

## Security notes

- The gRPC admin token stays in this process. Browsers only ever hold an opaque
  session id.
- Sessions live in memory: a restart asks for a fresh login, and the panel does
  not survive being replicated. That is a deliberate trade — nothing to encrypt,
  nothing to rotate.
- Session cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` whenever
  `baseURL` is HTTPS.
- Logout is a POST behind a CSRF token; the check is in place ahead of the
  writes that arrive in phase two.
- A refused login is logged with the identity that was refused.

## Known gap

dex's gRPC API is protected by a **single shared token with no identity**. The
dashboard logs which administrator performed an action, but dex only ever sees
"the token". Before the panel is allowed to write, the API needs to distinguish
named tokens so the audit trail means something.
