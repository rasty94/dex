# TODO — Dex Fork (rasty94/dex)

> Solo trabajo pendiente. Lo ya entregado vive en [DONE.md](DONE.md), incluida la
> tabla de estado y los bloques cerrados de la sincronización con upstream.
>
> Última actualización: 2026-09-01
> Imagen Docker: `ghcr.io/rasty94/dex:latest`
> Repositorio: <https://github.com/rasty94/dex>

---

## 🚀 Futuras Mejoras (Propuestas)

### 1. 📊 Telemetría

- [ ] Trazabilidad distribuida (OpenTelemetry) para peticiones hacia OpenStack.

### 2. 🚀 Rendimiento y Alta Disponibilidad (HA)

- [ ] Cache Distribuida: Expandir la actual caché nativa en RAM de Keystone para soportar Redis de backend, permitiendo despliegues de Dex multi-réplica compartir estado de validación de tokens.
    - Los buckets del rate limiter de login tienen el mismo techo y se migran con ella: hoy el límite efectivo es `attempts × réplicas`.

### 3. ☁️ Ecosistema Cloud Native e Integraciones

- [ ] Provider para HashiCorp Vault: Leer el `adminPassword` y los app-credentials nativamente de Vault sin exponerlos en el `config.yaml`.
- [ ] Helm Chart u Operator Kubernetes Mejorado: Adaptar configuraciones del Fork directamente en los values nativos del chart oficial de la comunidad.

### 4. 🔐 Autenticación Avanzada (Beyond TOTP)

- [ ] WebAuthn / Passkeys: Empezar a sentar las bases para la autenticación sin contraseña (Passwordless) en Keystone, como segundo factor apoyándose en WebAuthn o llaves físicas FIDO2 (Yubikey).
- [ ] Políticas Condicionales: Permitir bloquear el login basado en roles o dominios específicos de OpenStack directamente en el Connector antes de emitir claims JWT.

### 5. 🔄 Sincronización con upstream — bloque F

> Bloques A a E y G cerrados, con su detalle en [DONE.md](DONE.md).

- [ ] **Re-port sobre `upstream/master`.** 🚧 **En marcha** en la rama
      `feat/upstream-sync-2026-08` (empujada a `origin`, partiendo de `upstream/master`).
      Worktree en `../dex-upstream-sync`, para no mezclarla con `master`.
      - [x] `connector/keystone/` completo, más el registro de sus colectores en `cmd/dex`.
            Entró **sin una sola edición**: compila contra el layout nuevo tal cual. Confirma la
            estimación de coste cero para esta pieza.
      - [x] Rate limiting de login, ahora en un paquete propio `server/ratelimit` compartido
            por el flujo interactivo y el grant de password, con test de extremo a extremo del
            throttling en el grant. Dos mejoras sobre el original: la IP se lee del resolutor
            de upstream (nuestro `clientIP` se fiaba del primer `X-Forwarded-For`, spoofeable),
            y los valores por defecto viven dentro de `ratelimit.New`. El router adjunta ahora
            siempre una IP al contexto, si no todos los clientes compartirían bucket.
      - [ ] i18n y theming por cliente → `server/templates/`.
      - [ ] Trusted device sobre la maquinaria de cookies de upstream (ya cifradas), no sobre
            la cookie propia `dex_mfa_trust_*`.
      - [ ] Flujo TOTP en el servidor (`ErrTOTPRequired`) → `server/authflow/`.
      - [ ] `.proto` y config dinámica de gRPC: regenerar y reaplicar nuestras extensiones.
      - [ ] `web/`: CSS, temas y plantillas.

  Por qué no es un `git merge`: upstream troceó `server/` en subpaquetes y los seis ficheros donde vive nuestro trabajo (`handlers.go`, `oauth2.go`, `api.go`, `templates.go`, `refreshhandlers.go`, `deviceflowhandlers.go`) ya no existen allí, así que git los ve como modify/delete. No activar de golpe lo nuevo de upstream (sesiones, `sid`, back-channel logout, PKCE configurable, CEL, Kerberos).

### 6. 🎛️ Dashboard de administración

> Estado: **fase 1 entregada** en `cmd/dex-dashboard/` (solo lectura). Las cinco decisiones
> de abajo están tomadas: binario aparte en este repo, BFF en Go con `html/template`, el token
> de administración solo en servidor, login OIDC contra el propio dex con gate por grupo, y
> alcance de usuarios limitado al password DB. Ver `cmd/dex-dashboard/README.md`.

**El problema no es la API, es quién la usa y cómo.** El API gRPC ya cubre casi todo lo que
un dashboard necesita: `ListClients`/`Create`/`Update`/`DeleteClient`, `ListPasswords` y sus
CRUD, `ListConnectors` y sus CRUD (tras el flag `api_connectors_crud`, apagado por defecto),
`ListRefresh`/`RevokeRefresh`, `GetDiscovery`, `GetVersion` y `ReloadConfig`. Lo que falta es
una interfaz, un modelo de identidad para los administradores y una pista de auditoría.

#### Lo que hay que decidir antes de teclear

1. **Dónde vive.** Recomendación: **servicio aparte**, no dentro del binario de dex. Dex es el
   IdP; meterle un panel de administración en el mismo proceso amplía su superficie de ataque
   y convierte cualquier fallo del panel en un fallo del IdP. Un binario separado que habla
   gRPC con dex se despliega, se actualiza y se expone por separado.
2. **Cómo llega el navegador al API.** gRPC no se llama desde un navegador. Hacen falta
   grpc-web con proxy, o un **BFF** (backend for frontend) que exponga REST/JSON al navegador
   y hable gRPC con dex. Recomendación: BFF, porque además resuelve el punto siguiente.
3. **Dónde vive el token de administración.** Hoy el API se protege con **un único token
   estático compartido** (`newAuthInterceptor` en `cmd/dex/serve.go`): quien lo tenga es
   administrador total y no queda rastro de quién hizo qué. Ese token **no puede llegar nunca
   al navegador**. El BFF lo guarda en servidor y nunca lo emite al cliente.
4. **Quién entra al dashboard.** Los administradores se autentican contra **el propio dex** por
   OIDC, y se autorizan por pertenencia a un grupo (`dex-admins` o el que se configure). Ojo al
   problema del huevo y la gallina: si dex no arranca o el conector cae, nadie entra. Hace falta
   una vía de rescate — un usuario local del password DB, o un flag de arranque.
5. **Qué significa "añadir usuarios".** Este es el punto que más confusión va a generar y hay
   que dejarlo escrito en la propia UI: el password DB de dex son **solo usuarios locales**. Los
   usuarios de Keystone (o LDAP, o GitHub) viven en su proveedor y dex no los crea ni los borra.
   El dashboard puede listar y gestionar los locales, y para el resto solo puede **consultar** y
   revocar sesiones. Prometer "gestión de usuarios" a secas es prometer algo que no se puede
   cumplir.

#### Fases

- [x] **Fase 1 — Solo lectura.** Entregada. Login OIDC contra dex con gate por grupo o email
  de rescate, y vistas de clientes, conectores, usuarios locales, sesiones por `sub`, versión
  y discovery. Sin escritura. Probada de extremo a extremo contra un dex real: un usuario
  autenticado pero sin el grupo admin recibe 403 y queda en el log.
  - Sin htmx todavía: fase 1 no tiene ni un formulario que lo justifique, así que no hay
    JavaScript en el panel. Entrará con la primera escritura, junto con `script-src` en la CSP.
- **Fase 2 — Escritura de bajo riesgo.** Alta/baja/edición de clientes OAuth2 y de usuarios
  locales. Revocación de refresh tokens (útil de verdad en incidentes). Cada acción, a un log de
  auditoría con la identidad OIDC del administrador, no con el token compartido.
- **Fase 3 — Conectores.** Es la más delicada: la config de un conector es un blob JSON con
  esquema distinto por tipo. **No** construir un generador de formularios genérico. Empezar por
  un editor de JSON con validación contra el esquema del tipo y un botón de `ReloadConfig`, y
  solo hacer formulario a medida para los dos o tres tipos que de verdad usemos.
- **Fase 4 — Operación.** Métricas ya expuestas por Prometheus (incluidas las de keystone y las
  del rate limiter de login), estado de salud, y visor de intentos de login fallidos.

#### Requisitos que no son negociables

- Toda escritura queda auditada con **quién** (identidad OIDC), **qué** y **cuándo**.
- El dashboard no guarda credenciales de usuarios finales ni las muestra.
- CSRF en todas las mutaciones y `SameSite` en la cookie de sesión del panel.
- El panel se puede desplegar sin exponerlo a internet (bind separado), y así por defecto.

#### Deuda previa que conviene cerrar antes de la fase 2

- [ ] Un solo token sin identidad ni roles. Si el dashboard va a escribir, el API necesita al
      menos distinguir varios tokens con nombre para que la auditoría signifique algo.

### 7. 🧹 Deuda técnica conocida

- [ ] **Sesiones Keystone antiguas con `userIDKey: email`/`username`**: `Refresh()` usa ahora el id real de Keystone guardado en `identity.ConnectorData`. Las sesiones creadas antes de ese cambio no lo tienen y caen al fallback (`identity.UserID`, que es el UUID sintético), así que seguirán fallando el refresh hasta que el usuario vuelva a autenticarse. Anotarlo en el `CHANGELOG` de la próxima release.
- [ ] **`Ejemplos/` en solo lectura**: el directorio está como `dr-xr-xr-x` en algunos entornos de desarrollo, lo que hace fallar `git checkout` sobre `Ejemplos/config.yaml` con "Permission denied". Se arregla con `chmod u+w Ejemplos`.
