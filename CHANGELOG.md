# CHANGELOG — Dex Fork (rasty94/dex)

Versiones de este fork sobre la base de [dexidp/dex](https://github.com/dexidp/dex).

El formato sigue [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Las etiquetas del fork llevan el prefijo `fork-v` (`fork-v2.0.0`). El esquema `v2.x` es
de dexidp/dex, que va por la `v2.45.1`, y este repositorio tiene todas sus etiquetas por
el remote `upstream`: numerar igual colisionaría en cuanto publiquen la siguiente.

---

## [Unreleased]

### Añadido

- Roles de Ansible `haproxy` y `keepalived`: la entrada del despliegue. HAProxy
  termina el TLS publico y reparte entre las replicas de dex y del panel;
  keepalived mantiene una IP flotante entre dos balanceadores con VRRP, con una
  sonda que hace una peticion de verdad a HAProxy en vez de mirar si el proceso
  existe. El gRPC pasa en `mode tcp` sin terminarse: el 5557 de dex ya sirve TLS
  y terminarlo aqui cambiaria una credencial que hoy no sale de la maquina por
  una que viaja sin cifrar.
- `internal_ca_extra_sans`: SAN adicionales en el certificado de cada host, que
  es lo que hace posible el passthrough del gRPC. Aplicarla reemite el
  certificado de todos los nodos de dex y los reinicia, de uno en uno.
- `dex_http_bind`, `dex_grpc_bind` y `dex_dashboard_bind`: la interfaz por la que
  se publican los puertos. Con el TLS terminado en el balanceador dejan de ser un
  ajuste y pasan a ser un requisito, porque el trato de `trustForwardedFor` es
  que el balanceador sea el unico camino.
- Roles de Ansible `mariadb` y `orchestrator`: MariaDB en alta disponibilidad,
  fase 4 del despliegue. El primero levanta un par primario/replica con
  replicacion semisincrona y el agente de sonda que dice cual de los dos es el
  primario escribible; el segundo, orchestrator en modo raft a tres, detecta la
  caida y promueve. Motivo: quitar el ultimo punto unico de fallo del
  despliegue, la base de datos donde viven las claves de firma, los refresh
  tokens y las sesiones.
- Frontend y backend `fe_mysql`/`be_mysql` nuevos en el rol `haproxy`, en
  `mode tcp` con `balance first` y comprobacion por el puerto del agente de
  sonda. Motivo: HAProxy no habla SQL, asi que necesita una forma de saber cual
  de los dos nodos de MariaDB es el primario sin tocar el protocolo.

### Cambiado

- **El panel se publica en un nombre propio, no en una ruta bajo el del issuer.**
  El inventario de ejemplo pasa de `https://sso.interno/dex-dashboard` a
  `https://panel.interno/`, y el balanceador reparte por cabecera `Host`.
  - **Motivo:** el panel registra todas sus rutas en la raiz y su `baseURL` solo
    construye el `redirect_uri` de OIDC, asi que bajo un prefijo recibe
    `/dex-dashboard/callback`, cae en su catch-all, arranca un login, dex
    devuelve al callback y vuelta: bucle infinito de redirecciones del que solo
    lo saca el limitador de intentos. Quitarle el prefijo en el balanceador
    arregla la entrada y no la salida.
  - **Impacto:** hacen falta dos nombres DNS y un certificado que cubra los dos.
    Quien tuviera el panel bajo una ruta no podia entrar en el, asi que no hay
    despliegue que romper.
- `dex_storage.host` del inventario de ejemplo pasa a apuntar al balanceador, no
  a una maquina de MariaDB. Motivo: tras un failover el primario cambia de
  nodo; apuntar a una maquina fija mantendria el punto unico de fallo que esta
  fase existe para quitar.
- `ssl_mode` del inventario de ejemplo pasa de `"false"` a `"true"`. Motivo:
  desde esta fase la base de datos es un componente propio, servido con la CA
  interna, y deja de ser la base ajena de la que el rol `dex` solo avisaba.

---

## [2.1.0] — 2026-09-04

Estado compartido en Valkey y despliegue en alta disponibilidad con Ansible. Un
proceso suelto sigue funcionando exactamente igual que antes: todo lo de aquí se
enciende configurando `valkey.addresses`, y sin esa clave cada réplica guarda lo
suyo en memoria. Es un menor y no un mayor porque nada de lo publicado en la
`fork-v2.0.0` cambia de forma; lo que cambia es lo que se puede compartir entre
réplicas cuando hay más de una.

### Añadido

- `valkey.addresses` y `valkey.mode` en `dex.yaml` y en el config del panel: un
  almacén compartido opcional entre réplicas. Sin configurarlo, cada proceso sigue
  guardando lo suyo en memoria, que es el comportamiento de siempre.
- Valkey en alta disponibilidad: `mode: sentinel` con `masterSet`, o `mode: cluster`
  con varias direcciones. `valkey-go` sigue el failover de sentinel y las
  redirecciones del cluster sin que dex tenga que hacer nada. El modo es explícito
  porque deducirlo del número de direcciones falla en silencio.
- Métrica `dex_login_rate_limit_backend_errors_total`: cuenta las veces que el
  almacén compartido no respondió y el limitador cayó de vuelta a sus cubos
  locales.
- Aviso al arrancar cuando el servidor de Valkey puede desalojar claves: todo lo
  que dex guarda ahí lleva caducidad, así que un `maxmemory` con cualquier
  política que no sea `noeviction` puede llevarse los contadores del limitador de
  login —el presupuesto de intentos se reiniciaría solo— y las sesiones del panel.
  Es solo un aviso; si el servidor no responde a `CONFIG`, no se dice nada.
- `documentacion/valkey.md`: lo que el servidor compartido tiene que cumplir,
  qué se pierde en un reinicio, qué hace cada componente cuando el almacén se cae
  y las métricas que lo delatan.
- El `attemptLimiter` del panel (`cmd/dex-dashboard/auth.go`) puede contar en
  Valkey cuando el panel tiene `valkey.addresses` configurado, reutilizando el
  mismo `sharedCounter` que el limitador de dex. Sin Valkey configurado, nada
  cambia: sigue siendo el contador local de siempre.
- Colección de Ansible (`ansible/`) que despliega dex, el panel y Valkey en
  Docker sobre las tres topologías (`standalone`, `sentinel`, `cluster`), con
  una CA interna propia para el TLS entre los tres y los secretos cifrados con
  `ansible-vault`. Documentado en `documentacion/despliegue-ansible.md`.
- `valkey.tls.cert` y `valkey.tls.key` en el bloque `valkey` de dex y del panel:
  el certificado que el proceso **presenta** al servidor, no el que verifica. Hacen
  falta cuando el servidor corre con `tls-auth-clients yes`, que es el valor de
  fabrica. El par se lee al arrancar y no en cada handshake, asi que una ruta
  equivocada para dex diciendo cual es el fichero. Uno sin el otro es un error de
  arranque.

  En la imagen Docker son `DEX_VALKEY_TLS_CERT` y `DEX_VALKEY_TLS_KEY`, y
  `DEX_DASHBOARD_VALKEY_TLS_CERT` / `_KEY` para el panel. Sin ellas, el camino de
  configuracion por entorno no podia hablar con un servidor que exigiera
  certificado: solo el rol de Ansible, que escribe el fichero directamente.

### Cambiado

- El limitador de login puede contar en Valkey (`valkey.addresses`), de modo que
  varias replicas comparten un solo presupuesto. Sin configurarlo, nada cambia.
  Con Valkey, el algoritmo pasa de cubo de fichas a **ventana fija**, que permite
  hasta `2 x attempts` a caballo entre dos ventanas; a cambio el limite es
  correcto entre replicas, que sin esto valia `attempts x replicas`.
- Las sesiones de administrador del panel (`cmd/dex-dashboard`) pueden vivir en
  Valkey en vez de en memoria del proceso, con `valkey.addresses` en el config del
  panel. Quien pueda escribir ahi se hace administrador con permiso de
  escritura sobre el panel: esa conexion necesita autenticacion y TLS igual que
  cualquier otro credencial de administracion.
- El panel recalcula el permiso de administrador **en cada petición**, con la
  configuración cargada y el correo y los grupos que guarda la sesión, en vez de
  confiar en el permiso que se calculó al entrar. Con las sesiones en Valkey
  sobreviven a un reinicio del panel, así que sin esto quitar a alguien de
  `admin.writeGroups` no surtía efecto hasta que su sesión caducara. Perder el
  acceso de lectura destruye la sesión. Los grupos siguen siendo los del ID token
  de cuando esa persona entró: una baja hecha en el proveedor de identidad no se
  ve hasta que vuelva a autenticarse.
- El conector Keystone puede compartir su cache de tokens entre replicas con
  `cacheShared: true`, cuando `dex.yaml` tiene `valkey.addresses` configurado.
  `cacheShared` decide donde vive la cache, no si existe: eso lo sigue decidiendo
  `cacheTTL` como antes.

### Arreglado

- Un Valkey inalcanzable se contaba como fallo de caché normal en
  `keystone_token_cache_lookups_total`, así que una caída se veía igual que una
  racha de tokens nuevos mientras cada login pagaba el viaje entero a Keystone.
  Ahora tiene su propia etiqueta, `result="error"`. Sigue fallando abierto:
  ningún login se rompe porque la caché no esté.
- Un cliente que abandonaba su petición cancelaba el contexto y eso se contaba en
  `dex_login_rate_limit_backend_errors_total` igual que un Valkey inalcanzable,
  lo que dejaba la alarma sin valor. Un plazo agotado sí sigue contando.
- La cache de tokens del conector Keystone comprobaba la caducidad al leer pero
  nunca borraba, asi que crecia sin limite mientras el proceso viviera. Afecta
  tambien a despliegues de una sola replica.
- Un `cacheTTL` mal escrito en el conector Keystone desactivaba la cache en
  silencio. Ahora dex no arranca y dice cual es el valor que no entiende. Lo
  mismo si `cacheTTL` no es positivo.
- `cacheShared: true` sin `valkey.addresses` configurado tambien es ahora un error
  de arranque, en vez de una cache local silenciosa donde se esperaba una
  compartida.

### Seguridad

- **El bus del cluster de Valkey ya va autenticado.** El rol de Ansible ponia
  `tls-auth-clients no` en los nodos y en el sentinel porque el cliente no sabia
  presentar certificado, y esa directiva no es solo de clientes: es global, asi que
  tambien gobernaba las conexiones que los nodos se hacen entre si. El bus iba
  cifrado pero sin autenticar --no tiene un `requirepass` equivalente-- y bastaba
  tener la CA interna para unirse al cluster. Con el par en el cliente, las dos
  plantillas vuelven al `yes` de fabrica.
  - **Impacto en una migracion:** los nodos de datos reciben el cambio solos, pero
    **un sentinel ya desplegado no**, porque su fichero no se reescribe nunca; sigue
    aceptando clientes sin certificado hasta que se migre a mano. Y como la tanda de
    Valkey va antes que la de dex, hay una ventana en la que los nodos exigen
    certificado y dex todavia corre sin el: el proceso que ya esta arriba aguanta y
    cae a sus contadores locales, pero uno que reinicie dentro de esa ventana no
    levanta. Las dos cosas, con su receta, en
    `documentacion/despliegue-ansible.md`.

---

## [2.0.0] — 2026-09-02

Re-port completo del fork sobre el layout nuevo de dexidp/dex. Es un mayor por dos
cambios incompatibles y porque la base cambia de arriba abajo: upstream troceó el
paquete `server` en subpaquetes, así que **no fue una fusión sino una reescritura
rebanada a rebanada** sobre su árbol.

El estado anterior queda recuperable en la rama `master-pre-upstream-sync` y en la
etiqueta `pre-upstream-sync-2026-09-02`.

### ⚠️ Cambios incompatibles

- **El claim `sub` vuelve al formato de upstream.** Este fork emitía un `sub` plano
  (el user id tal cual); ahora vuelve a ser el par `(userID, connectorID)` codificado
  en protobuf-base64, igual que dexidp/dex. Motivo: el `sub` plano no lleva el
  connector, y todo lo que resuelve un subject contra una sesión offline lo necesita
  — la API gRPC de refresh, y la revocación con alcance de sesión que upstream ha
  construido encima. Mantenerlo nos dejaba fuera de esas features para siempre.
  - **Impacto:** cualquier consumidor que guarde o compare el `sub` de un ID token
    verá un valor distinto para el mismo usuario. `ListRefresh`/`RevokeRefresh` ahora
    exigen el subject codificado y rechazan un user id plano con error, en lugar de
    buscar al usuario recorriendo los conectores.
  - **Migración:** los clientes que indexen por `sub` deben re-mapear a los usuarios
    en su siguiente autenticación. No hay cambio de esquema en storage.

- **Las sesiones Keystone anteriores a esta versión no refrescan.** `Refresh()` resuelve
  ahora el usuario por el id real de Keystone, que se guarda en `identity.ConnectorData`
  al autenticarse. Las sesiones creadas antes de ese cambio no lo llevan y caen al
  fallback (`identity.UserID`), que con `userIDKey: email` o `username` es un UUID
  sintético derivado del correo o del nombre, no un id que Keystone reconozca.
  - **Impacto:** el refresh de esas sesiones falla contra Keystone. Afecta solo a los
    despliegues que hubieran configurado `userIDKey`; con el valor por defecto el
    `UserID` ya era el id real y no hay diferencia.
  - **Migración:** ninguna automática. El usuario vuelve a autenticarse una vez y la
    sesión nueva ya guarda el `ConnectorData` correcto.

- **`mfaTrust` exige ahora una clave de cifrado.** La cookie de dispositivo de confianza
  guardaba el token de Keystone en claro. Ese token no es un identificador de sesión de
  dex: es una credencial viva para todo el despliegue de OpenStack. Ahora va sellada con
  AES-GCM, con las mismas primitivas que la cookie de sesión de upstream.
  - **Impacto:** con `mfaTrust.enabled: true` y sin `mfaTrust.encryptionKey`, dex se
    niega a arrancar. Es deliberado: la alternativa era seguir escribiendo la credencial
    en claro. Las cookies emitidas por versiones anteriores no se pueden abrir, así que
    los dispositivos ya confiados vuelven a pedir el segundo factor una vez.
  - **Migración:** añadir `mfaTrust.encryptionKey` con 16, 24 o 32 bytes.

### Seguridad

- Binding del auth code en el callback del device flow (canje entre clientes)
- `client_secret` ya no viaja en el redirect del navegador
- Comparaciones de secreto a tiempo constante en refresh y device flow
- Saneado del parámetro `back` (open redirect en la pantalla de login)
- La contraseña ya no se re-inyecta en un campo oculto durante el paso TOTP
- Dependencias de criptografía, JOSE y SAML al día con upstream

### ✨ Heredado de upstream

Al reasentarse sobre su árbol, el fork incorpora todo lo que upstream construyó desde
la divergencia. Buena parte llega **apagada tras feature flags**, a propósito: activarlo
de golpe habría hecho el re-port imposible de revisar.

- Sesiones de navegador con SSO entre clientes, página de sesión, pantalla de logout,
  *remember me*, claim `sid`, back-channel logout y revocación con alcance de sesión.
  Tras `sessions_enabled`, apagado.
- MFA nativo de dex: TOTP con protección contra reutilización del código, y WebAuthn con
  llaves de seguridad. Es **independiente** del segundo factor que impone Keystone: aquél
  ocurre dentro del intercambio de credenciales y dex nunca ve el secreto; éste es
  posterior a la identidad y lo guarda dex.
- API gRPC de sesiones e identidades (`ListAuthSessions`, `TerminateSessionsByUser`,
  `ResetMFA`, `ListMFADevices`, `RevokeConsent`, purga RGPD de identidad). Tras
  `api_sessions_identities_crud`, apagado.
- Kerberos/SPNEGO, PKCE configurable y políticas CEL.

### 🔧 Cambiado

- **i18n reconstruida sobre el marcado de upstream**, en vez de sustituirlo por el del
  fork. Se conservan así su tema, el *remember me* y las páginas que el fork no tenía. 66
  claves en cinco idiomas, y una mejora sobre la versión anterior: una clave sin traducir
  cae al inglés en vez de renderizar una cadena vacía.
- **Theming por cliente**: el `client_id` viaja por el contexto de la petición en lugar de
  por la firma de cada plantilla. `frontend.clientThemes` no cambia.
- **API gRPC con tokens con nombre**: `grpc.tokens` acepta pares nombre/token y la línea
  de auditoría añade `caller`. El `grpc.token` de siempre sigue valiendo y se registra
  como `default`.
- El pie de página interpola el año. Antes mostraba un `%d` literal.

### 🐛 Corregido

- El `primaryColor` por cliente no pintaba nada: la variable CSS se definía y ningún
  selector la leía.
- La etiqueta del formulario de credenciales salía en inglés con el resto de la página
  traducida.
- `docker-entrypoint` ya no falla con un `dex` sin subcomando, y plantilla también la
  configuración del panel.
- Las llamadas `TerminateSessionsByUser` y `ResetMFA` no dejaban línea de auditoría.

---

## [1.0.0] — 2026-02-25

Primera release consolidada del fork. Integra todas las mejoras sobre el upstream de dexidp.

### ✨ Añadido

#### Conector Keystone — TOTP / MFA

- Soporte completo de autenticación en **dos pasos con TOTP** (RFC 6238)
- Detección automática de `ErrTOTPRequired` cuando Keystone responde `401 + Openstack-Auth-Receipt`
- Flujo MFA: credenciales → formulario TOTP → validación con receipt
- Context keys: `TOTPContextKey`, `ReceiptContextKey` para pasar estado entre capas

#### Conector Keystone — Nuevas funcionalidades

- **`TokenIdentity()`**: validación de tokens Keystone existentes vía `GET /v3/auth/tokens` (Token Exchange)
- **`UserIDKey`**: campo de config para derivar el UserID como UUID SHA1 de `email` o `name`
- **Multi-dominio dinámico**: el dominio puede venir del formulario de login (`showDomain: true`)
- Mejorado manejo de errores en `getAdminToken()` y `getTokenResponse()`
- Corrección de bug: `defer resp.Body.Close()` correctamente ordenado en `getUserGroups()`

#### Internacionalización (i18n)

- Sistema i18n completo con 5 idiomas: **EN, ES, FR, DE, PT**
- Ficheros YAML embebidos en el binario con `//go:embed` (`server/i18n/*.yaml`)
- Añadir idioma = crear `.yaml` y recompilar, sin modificar Go
- Parser de `Accept-Language` completo (soporta `es-ES,es;q=0.9,en;q=0.8`)
- Nueva función `SupportedLanguages()` para introspección
- Claves: login, password, TOTP, dominio, approval, device, OOB, error, footer

#### UI / Frontend

- Iconos SVG para los 12 tipos de connector en la pantalla de login:
  `github`, `gitlab`, `google`, `microsoft`, `linkedin`, `bitbucket`, `gitea`, `ldap`, `keystone`, `saml`, `oidc`, `atlassiancrowd`
- Formulario de password completamente traducido (0 strings hardcoded)
- Temas dark/light actualizados (CSS, logos, favicons)
- Nuevo `robots.txt`

#### Refactor de interfaz

- `CallbackConnector`: eliminado `connData []byte` de `LoginURL` y `HandleCallback` (simplificación)
- `connector.go`: eliminado `UserNotInRequiredGroupsError` (movido a cada connector)
- Todos los connectors adaptados: bitbucketcloud, gitea, github, gitlab, google, linkedin, microsoft, mock, oidc, openshift

#### DevOps

- Workflow CI/CD: `.github/workflows/ghcr-publish.yaml`
    - Build + push automático a `ghcr.io/rasty94/dex` en cada push a `master`
    - Multi-arch: `linux/amd64` + `linux/arm64`
    - Tags automáticos: `latest`, `sha-<short>`, `vX.Y.Z`, `vX.Y`, `vX`
    - GitHub Actions Cache para builds más rápidos
- `.dockerignore` ampliado: excluye `dex_mod/`, `Ejemplos/`, `docs/`, tests, IDE, OS artifacts
- Imagen Docker: `ghcr.io/rasty94/dex:latest`

#### Tests

- `connector/keystone/key_test.go`: tests de generación de claves UUID/SHA1
- `connector/keystone/validate_test.go`: tests de validación de tokens

#### Documentación

- `documentacion/keystone_connector.md`: análisis completo con TOTP y permisos OpenStack
- `documentacion/Permisos base keystone.md`: referencia de políticas Keystone
- `documentacion/policy_modificado.yml`: política Keystone ajustada para Dex
- `documentacion/despliegue-docker-tls.md`: guía de despliegue con TLS

### 🔧 Cambiado

- `connector/keystone`: conector elevado de `alpha` a `beta` (estabilidad demostrada en producción)
- `go.mod`/`go.sum`: actualización de dependencias

### 🐛 Corregido

- Bug: `defer resp.Body.Close()` se llamaba antes de `io.ReadAll()` en `getUserGroups()`
- Encoding issue: ficheros de templates con CRLF normalizados

---

## Base upstream

Este fork está basado en [dexidp/dex](https://github.com/dexidp/dex) commit `2ecf64e8` (rama `master`, ~Feb 2026).
