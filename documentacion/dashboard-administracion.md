# Panel de Administración — Funcionamiento

> Binario: `cmd/dex-dashboard` · Configuración de ejemplo: `cmd/dex-dashboard/config.example.yaml`

Este documento explica **cómo funciona por dentro** el panel de administración: qué
piezas tiene, por dónde pasa una petición, dónde están las fronteras de confianza y
qué hacer cuando algo falla. Para la guía rápida de arranque, ver
`cmd/dex-dashboard/README.md`.

---

## 1. Qué es, y por qué es un binario aparte

El panel es un **cliente de Dex**, no una parte de Dex.

Dex es el proveedor de identidad: si se cae o se compromete, se cae o se compromete
el login de todo lo que hay detrás. Una interfaz de gestión es, por naturaleza, la
parte del sistema con más superficie (formularios, plantillas, sesiones de
navegador, dependencias de frontend). Meterla dentro del mismo proceso significaría
que cualquier fallo del panel es un fallo del IdP.

Por eso son dos binarios: se compilan, se despliegan, se actualizan y se exponen por
separado. El panel puede estar apagado sin que nadie lo note; Dex no.

**En la fase 1 el panel es de solo lectura.** Ninguna vista puede cambiar el estado de
Dex. La escritura llega en la fase 2, y hay un motivo para el orden — ver §9.

---

## 2. Arquitectura y frontera de confianza

```
   navegador                    dex-dashboard                      dex
  ┌──────────┐                 ┌──────────────┐               ┌──────────┐
  │          │  HTTPS          │              │  gRPC         │          │
  │  admin   │ ──────────────► │  BFF en Go   │ ────────────► │ API      │  :5557
  │          │ ◄────────────── │  html/       │ ◄──────────── │ gRPC     │
  └──────────┘   HTML ya       │  template    │  token de     └──────────┘
       │         renderizado   │              │  administración     │
       │                       └──────────────┘                     │
       │                                                            │
       └──────────── login OIDC (authorization code) ───────────────┘
                                                                 :5556
```

La línea que importa es la de la izquierda. **Al navegador solo le llega HTML y un
identificador de sesión opaco.** Nunca le llega el token de administración de la API
gRPC, y esa es exactamente la razón de que exista un backend en vez de una aplicación
de página única que hablara con Dex directamente: en una SPA, la credencial de
administración tendría que estar en el cliente.

El token vive en el proceso del panel (`dexClient.token`, en `cmd/dex-dashboard/dex.go`)
y se adjunta a cada llamada gRPC en `authed()`. Ningún handler de HTTP lo toca.

Segunda consecuencia del diseño: el navegador **no habla gRPC**, no puede. El panel
traduce. Es lo que se llama un BFF, *backend for frontend*.

---

## 3. El flujo de autenticación, paso a paso

Los administradores se autentican **contra el propio Dex** por OIDC. El panel no tiene
usuarios ni contraseñas propias.

1. El navegador pide una página protegida, por ejemplo `/clients`.
2. `requireAdmin` (`auth.go`) busca la cookie `dex_dashboard_session`. No la hay.
3. Al ser un `GET`, arranca el login: genera un `state` aleatorio, lo guarda en la
   cookie `dex_dashboard_state` (10 minutos de vida) y redirige a `/auth` de Dex.
4. El administrador se autentica en Dex con el conector que sea.
5. Dex redirige de vuelta a `<baseURL>/callback` con un `code` y el `state`.
6. El panel compara el `state` recibido con el de la cookie **en tiempo constante**.
   Esto es lo que impide que una respuesta de login que el navegador no pidió sea
   aceptada (CSRF sobre el callback).
7. Canjea el `code` por tokens, **verifica la firma del `id_token`** contra el JWKS de
   Dex y extrae los claims `email`, `name` y `groups`.
8. Decide si esa identidad es administradora (§4). Si no lo es, responde `403` y lo
   registra con la identidad rechazada.
9. Si lo es, crea la sesión y planta la cookie.

Un detalle de la fase 3: **el scope `groups` se añade siempre**, aunque no esté en la
configuración (`newAuthenticator` en `auth.go`). La autorización depende de ese claim;
sin él, el panel autenticaría correctamente y luego dejaría fuera a todo el mundo, que
es el fallo más desconcertante posible.

### Peticiones que no son GET

`requireAdmin` trata distinto a un `POST` sin sesión: en vez de mandarlo al login,
responde `401`. Redirigir un formulario caducado a un login y ejecutarlo al volver
sería reproducir una acción que el usuario envió en otro contexto.

---

## 4. Quién entra: la puerta de acceso

Se configura en el bloque `admin`:

```yaml
admin:
    groups:
        - dex-admins          # basta con pertenecer a uno
    emails:
        - break.glass@example.com
```

`authorized()` (`auth.go`) admite si el email está en `admin.emails` (comparación
insensible a mayúsculas) **o** si alguno de los grupos del usuario está en
`admin.groups` (comparación exacta, no por subcadena: `dex-admins-readonly` no entra
por `dex-admins`).

**Los emails son la vía de rescate.** El problema del huevo y la gallina es real: si el
grupo de administración lo aporta un conector y ese conector es justo el que está
roto, nadie puede entrar a arreglarlo. Una lista corta de emails conocidos da una
segunda puerta.

**La configuración se niega a arrancar si no hay ni grupos ni emails** (`validate()` en
`config.go`). No es una comodidad: un panel de administración que admite a cualquier
usuario que Dex sepa autenticar no tiene puerta en absoluto, y arrancar así sería
peor que no arrancar.

---

## 5. Sesiones y cookies

Las sesiones viven **en memoria del proceso** (`sessionStore`, en `auth.go`). La cookie
del navegador solo lleva un identificador aleatorio de 32 bytes; todo lo demás —
email, grupos, token CSRF, caducidad — se queda en el servidor.

La consecuencia buena: no hay nada que cifrar en la cookie, nada que firmar, ninguna
clave que rotar.

La consecuencia a asumir, anotada como `ponytail:` en el código: **un reinicio del
panel pide login de nuevo, y el panel no sobrevive a estar replicado**. Para una
consola de administración de una sola instancia es el intercambio correcto. Si algún
día se replica, hay que mover el almacén a un sitio compartido.

La caducidad se comprueba **al leer**, no solo por el `max-age` de la cookie: un
cliente que siga presentando el identificador pasado el TTL pierde el acceso igual.
Por defecto son 8 horas, ajustable con `admin.sessionTTL`.

Banderas de la cookie: `HttpOnly` siempre, `SameSite=Lax` siempre, y `Secure` cuando
`baseURL` empieza por `https://`.

### CSRF

Cada sesión lleva un token CSRF que se renderiza en los formularios. `requireCSRF` lo
compara en tiempo constante. Hoy solo protege el logout, porque no hay más
mutaciones, pero **la comprobación ya está puesta antes de que lleguen las escrituras**
de la fase 2, en vez de después.

---

## 6. Cómo llega a los datos

Todas las vistas salen de la API gRPC de Dex. No hay acceso directo a la base de
datos: si el panel pudiera leer el storage por su cuenta, sería una segunda ruta hacia
los datos de Dex que habría que asegurar y mantener aparte.

| Vista | Llamada gRPC | Notas |
| ----- | ------------ | ----- |
| Overview | `GetVersion`, `ListClients`, `ListPasswords`, `ListConnectors` | Los conectores se cuentan «si se puede» |
| Clients | `ListClients` | Tipo público/confidencial, redirect URIs, trusted peers |
| Connectors | `ListConnectors` | Detrás del flag `api_connectors_crud` |
| Local users | `ListPasswords` | Solo el password DB, ver §7 |
| Sessions | `ListRefresh` | Requiere un `sub`, ver §8 |

### Degradación en vez de caída

El listado de conectores está detrás del feature flag `api_connectors_crud` de Dex,
que viene apagado. En el Overview eso **no tumba la página**: el contador muestra «—» y
un aviso explica que el flag está desactivado. Un panel que devuelve un 500 entero
porque una de cuatro llamadas está capada es un panel inútil.

`friendlyGRPCError` (`handlers.go`) traduce los fallos habituales a algo accionable:
token rechazado, API inalcanzable, flag apagado, `sub` mal formado. Lo que no reconoce
lo muestra tal cual, sin tragárselo.

---

## 7. Qué significa «usuarios» aquí

Este es el punto que más confusión genera, y por eso el aviso está **en la propia
página**, no solo en la documentación:

> La vista *Local users* lista el password DB de Dex y nada más.

Los usuarios que entran por Keystone, LDAP, GitHub o cualquier otro conector **viven en
ese proveedor**. Dex no los crea, no los borra y no guarda sus contraseñas: solo delega
la autenticación y traduce el resultado a claims. Ni este panel ni ningún otro sobre
la API de Dex puede gestionarlos.

Lo que sí se puede hacer con ellos, y llegará en la fase 2, es **revocar sus sesiones**.

---

## 8. La vista de sesiones y el `sub`

`ListRefresh` de Dex se indexa por el claim `sub`, que **no es un email ni un nombre de
usuario**: es un protobuf en base64 que codifica el par `(userID, connectorID)`.

El panel **no lo deriva**. El codificador vive en `server/internal`, un paquete que
`cmd/` no puede importar por las reglas de Go, y duplicar aquí un formato de wire
interno crearía un acoplamiento que se rompería en silencio el día que cambie.

Así que el formulario pide el `sub` tal cual, que es lo que un operador tiene delante
cuando investiga un incidente: el token del usuario. Si algún día buscar por nombre se
vuelve el caso común, la solución correcta es exponer un codificador desde Dex, no
copiar el formato.

Un `sub` mal formado devuelve un aviso claro, no el error de protobuf en crudo.

---

## 9. Lo que bloquea la escritura

El panel registra qué administrador hace cada cosa. **Dex no.**

La API gRPC se protege con un **único token estático compartido**
(`newAuthInterceptor`, en `cmd/dex/serve.go`). Quien lo tenga es administrador total, y
desde el lado de Dex todas las acciones son del mismo actor: «el token». Una auditoría
que solo puede decir «lo hizo el token» no sirve de nada cuando hay que averiguar
quién borró un cliente.

Por eso la fase 1 es de solo lectura y la fase 2 no empieza hasta que la API
distinga tokens con nombre. El orden no es prudencia excesiva: es que la pista de
auditoría tiene que existir **antes** de que haya algo que auditar.

---

## 10. Puesta en marcha

### En Dex

```yaml
grpc:
    addr: 127.0.0.1:5557

staticClients:
    - id: dex-dashboard
      name: 'Dex Dashboard'
      secret: <una-cadena-larga-y-aleatoria>
      redirectURIs:
          - 'https://panel.example.com/callback'
```

Si la API gRPC lleva token, el mismo valor va en `dex.token` o `dex.tokenFile` del
panel.

### En el panel

Partir de `cmd/dex-dashboard/config.example.yaml`. Tres cosas tienen que cuadrar o no
arranca:

1. `dex.grpcAddress` apunta a una API gRPC habilitada y alcanzable.
2. `<baseURL>/callback` está entre los `redirectURIs` del cliente en Dex.
3. `admin.groups` o `admin.emails` tiene algo.

```
go build ./cmd/dex-dashboard
./dex-dashboard --config dashboard.yaml
```

Al arrancar, el panel espera hasta un minuto (12 intentos cada 5 segundos) a que el
endpoint de discovery de Dex responda, para que no haya que ordenar el arranque de
los dos servicios en el despliegue.

### Exposición

`listen` apunta por defecto a `127.0.0.1:5556`. **Es deliberado**: esto administra el
proveedor de identidad y no debería estar en internet. Detrás de un proxy con TLS y
restringido por red, o accesible solo por VPN.

El panel manda sus propias cabeceras de seguridad (`securityHeaders`, en `main.go`):
`X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: same-origin` y una CSP
`default-src 'none'` — no carga ni un byte de JavaScript, así que puede permitirse la
política más estricta.

---

## 11. Rutas

| Ruta | Protegida | Qué hace |
| ---- | :-------: | -------- |
| `/` | sí | Overview |
| `/clients` | sí | Clientes OAuth2 |
| `/connectors` | sí | Conectores configurados |
| `/users` | sí | Usuarios del password DB |
| `/sessions` | sí | Refresh tokens por `sub` |
| `/logout` | sí + CSRF | Cierra la sesión (`POST`) |
| `/callback` | no | Retorno del login OIDC |
| `/healthz` | no | Sonda de vida |

---

## 12. Diagnóstico

| Síntoma | Causa probable |
| ------- | -------------- |
| `admin.groups or admin.emails is required` al arrancar | Falta la puerta de acceso; ver §4 |
| Bucle de redirecciones al entrar | `baseURL` no coincide con el `redirectURI` registrado en Dex |
| «dex refused the API token» | `dex.token` no coincide con el de la API gRPC |
| «Cannot reach dex's gRPC API» | `grpc.addr` no habilitado en Dex, o `dex.grpcAddress` mal |
| Entra pero da 403 | La identidad no está en `admin.groups` ni en `admin.emails` |
| 403 y en los grupos no aparece nada | El conector no está devolviendo el claim `groups` |
| Conectores en «—» | El flag `api_connectors_crud` de Dex está apagado |
| Sesión perdida sin motivo | El panel se reinició; las sesiones son en memoria (§5) |

Eventos que deja en el log, y merece la pena vigilar:

- `dashboard login` — entrada correcta, con el email.
- `refused dashboard access` — autenticado pero **no** autorizado, con email y grupos.
  Es el evento que alguien irá a buscar después de un incidente.
- `rejected request with bad CSRF token` — token CSRF inválido.
- `dex API call failed` — la llamada gRPC que falló y por qué.

---

## 13. Estado y siguiente paso

Fase 1 entregada y probada de extremo a extremo contra un Dex real: el login OIDC
completo, un usuario autenticado sin el grupo de administración recibiendo `403`, y
las cinco vistas renderizando datos de la API.

Lo que sigue, en orden, está en `TODO.md`:

- **Fase 2** — escritura de bajo riesgo: clientes OAuth2, usuarios locales y revocación
  de refresh tokens. Bloqueada por la identidad de los tokens de la API (§9). Es donde
  entra htmx y donde la CSP necesitará `script-src`.
- **Fase 3** — conectores. La configuración es un blob JSON con esquema distinto por
  tipo: editor con validación, **no** un generador de formularios genérico.
- **Fase 4** — operación: métricas, salud, visor de intentos de login fallidos.
