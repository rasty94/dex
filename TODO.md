# TODO — Dex Fork (rasty94/dex)

> Solo trabajo pendiente. Lo ya entregado vive en [DONE.md](DONE.md), incluida la
> tabla de estado y la sincronización con upstream, cerrada por completo: el re-port
> sobre el layout nuevo es ya la línea principal del fork, publicada como `fork-v2.0.0`.
>
> La primera sección no son propuestas: es funcionalidad que **ya está en el árbol**,
> apagada tras feature flags, esperando una decisión.
>
> Última actualización: 2026-09-02
> Imagen Docker: `ghcr.io/rasty94/dex:latest`
> Repositorio: <https://github.com/rasty94/dex>

---

## 🎁 Heredado del re-port, sin encender

> Lo que upstream construyó desde la divergencia y el fork ya tiene en el árbol, apagado
> tras feature flags a propósito: activarlo de golpe habría hecho el re-port irrevisable.
> No es trabajo nuevo, es trabajo hecho esperando una decisión.

### 0. 🔑 Sesiones de navegador (`sessions_enabled`)

Estudio hecho. Lo que enciende el flag:

- Cookie de sesión (`dex_session`, 24 h absolutas y 1 h de inactividad por defecto).
- Se salta la **pantalla de selección de conector** cuando hay sesión válida y el cliente
  no pide `prompt=select_account`. Sigue autenticando contra el conector: no es un
  inicio de sesión silencioso.
- Páginas nuevas, ya traducidas a los cinco idiomas durante el re-port y hoy inertes:
  la de sesión (`/`), la de logout y la casilla de *remember me*.
- Claim `sid`, back-channel logout y revocación con alcance de sesión.
- **Es requisito duro del MFA nativo**: dex se niega a arrancar con autenticadores
  configurados y el flag apagado. No son dos decisiones, es una.

Riesgo acotado: el **SSO entre clientes es opt-in**, `ssoSharedWithDefault` viene en
`none`, así que encenderlo no empieza a compartir sesiones entre aplicaciones por su
cuenta. El back-channel logout solo dispara para clientes con `backchannelLogoutURI`.

- [x] **Probado en `Ejemplos/dashboard`**, que queda con el flag encendido. Verificado
      sobre el despliegue: cookie `dex_session` con `HttpOnly`, casilla de *recordarme*
      marcada por defecto, página de sesión en `/dex` mostrando conector, hora de inicio,
      caducidad por inactividad, IP, grupos y navegador —toda en español, con las claves
      que se tradujeron durante el re-port y que hasta ahora no se veían— y página de
      logout que termina la sesión de verdad: después queda «Sin sesión iniciada» y la
      siguiente petición de auth vuelve a la lista de conectores.
- [x] **Medido el radio de impacto, y es pequeño.** Con una sesión viva, una petición de
      otro cliente **se salta la pantalla de selección de conector** y va directa al
      conector — pero **sigue pidiendo la contraseña**. No hay inicio de sesión silencioso
      entre clientes mientras `ssoSharedWith` esté en `none`, que es el valor por defecto.
      Es decir: encender el flag ahorra un clic y añade páginas nuevas, no cambia quién
      puede entrar dónde.
- [ ] **Decidir la clave de cifrado de la cookie** (`sessions.cookieEncryptionKey`). Es
      opcional; sin ella la cookie va firmada pero no sellada. Conviene ponerla por el
      mismo motivo que en `mfaTrust`, aunque aquí lo que guarda es un id de sesión y no
      una credencial de otro sistema.
- [ ] **Decidir si se enciende por defecto** en `config.docker.yaml`. A la vista de lo
      medido, el riesgo es bajo; lo que falta es querer las páginas nuevas.

### 0.1 🗂️ API de sesiones e identidades (`api_sessions_identities_crud`)

- [x] **Expuesta en el panel, lo principal.** La vista de Sesiones muestra ahora, en
      secciones separadas: la identidad del usuario (correo, grupos, último acceso,
      segundos factores y **si la cuenta está bloqueada**), sus consentimientos con la
      acción de retirarlos por cliente, sus navegadores con sesión, y los refresh tokens
      de siempre. Navegadores y tokens van aparte a propósito: cerrar una sesión revoca
      los tokens que salieron de ella, revocar un token no cierra la sesión. Acciones:
      cerrar un navegador, cerrarlos todos, y **cerrar todos los de un conector** desde la
      página de Conectores, para retirar un proveedor de identidad.
      Hizo falta exportar `tokens.ParseSubject`: las sesiones se buscan por el par
      `(userID, connectorID)` y `server/internal` no es importable desde `cmd/`.
- [x] **Purga RGPD de una identidad**, con inventario en la confirmación. Cuenta lo que
      va a desaparecer y nombra aparte la consecuencia que el título de la acción no
      sugiere: el almacén de contraseñas está indexado **por correo, sin conector**, así
      que purgar una identidad borra también la cuenta local con ese mismo correo.
- [ ] **La purga no es atómica, y eso es de upstream.** Con usuarios estáticos falla al
      llegar a la contraseña, pero para entonces ya ha cerrado las sesiones y revocado los
      tokens. El panel explica ahora en qué estado queda, pero el arreglo de fondo es de
      `server/apiserver`: o intentar primero lo que puede fallar, o no empezar la cascada
      si el registro de contraseña es de solo lectura. Candidato a llevar a upstream.
- [x] **«He perdido el móvil»: quitar un segundo factor desde el panel.** Los factores
      registrados tienen ahora su propia sección con una fila por autenticador y un botón
      que llama a `DeleteMFASecret`; el siguiente login ofrece darse de alta otra vez con
      un secreto nuevo. Exige login reciente, como el resto de lo destructivo, porque
      quitar el último deja la cuenta con la contraseña sola.
      Verificado de punta a punta contra el despliegue de ejemplo: alta de TOTP, retirada
      desde el panel, secreto fuera del almacén y alta nueva con otro secreto en el
      siguiente login.
- [ ] **`ResetMFA` y `DeleteWebAuthnCredential` no se han expuesto.** El primero es
      recorrer la lista de autenticadores, que en la práctica son uno o dos; el segundo
      solo importa cuando alguien tiene varias llaves bajo el mismo autenticador y pierde
      una — hoy se van todas con él. Añadirlos cuando aparezca ese caso.

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

> **Reescrita tras el re-port.** WebAuthn ya no hay que construirlo: upstream trae
> `server/mfa` con TOTP nativo (con protección contra reutilización del código) y
> WebAuthn completo, más sus RPC de gestión. Lo que queda es decidir cómo encaja con
> Keystone, no implementarlo.

- [x] **Convivencia del MFA nativo con el de Keystone: resuelta por configuración.**
      No hacía falta mecanismo nuevo — `chainForClient` ya resuelve la cadena mirando el
      conector y cada autenticador acepta `connectorTypes`. La trampa es que
      **`connectorTypes` vacío significa *todos* los conectores**, así que el valor por
      defecto es el peligroso. Dex avisa ahora al arrancar cuando un autenticador alcanza
      un conector que ya impone su propio segundo factor, y solo si ese conector existe
      en el despliegue.
- [ ] **Passkeys sin contraseña para Keystone.** Esto sí es trabajo nuevo: WebAuthn de
      upstream es un segundo factor, no un primer factor, así que el login sin contraseña
      contra Keystone sigue sin base.
- [ ] Políticas Condicionales: Permitir bloquear el login basado en roles o dominios específicos de OpenStack directamente en el Connector antes de emitir claims JWT.
      Upstream trae ahora políticas CEL, que podrían servir de base en vez de escribirlo en el conector.

### 6. 🎛️ Dashboard de administración

> **Las cuatro fases están entregadas**, más un bloque de endurecimiento de seguridad
> (re-autenticación, caducidad por inactividad, `__Host-`, límite de login), esqueletos de
> conector, revocación masiva de sesiones, exportación de configuración y vista de discovery.
> Lo hecho, con sus decisiones de diseño, está en
> [DONE.md](DONE.md); cómo funciona, en
> [documentacion/dashboard-administracion.md](documentacion/dashboard-administracion.md).
> Aquí queda solo lo pendiente.

- [ ] **Campos nuevos de `Client` y `Connector` en los formularios.** El re-port trajo de
      upstream `allowedConnectors`, `ssoSharedWith`, `backchannelLogoutURI`,
      `postLogoutRedirectURIs`, `refreshTokenLifetime` y `grantTypes`. No rompen nada —
      verificado que el `UpdateClient` de upstream solo aplica los campos presentes, así
      que el panel no los borra al guardar— pero tampoco los sabe editar.
- [ ] **Enseñar al panel los tokens con nombre.** Dex ya los acepta (`grpc.tokens`, con
      `caller` en la auditoría), pero el panel manda un único token y no sabe de nombres.
      Lo entregado está en [DONE.md](DONE.md).
- [ ] **Visor de intentos de login fallidos.** No se puede hacer con la API actual: dex los
      escribe en su log y no hay forma de consultarlos. Necesita un recolector de logs, no una
      vista más. La de Status ya dice *cuántos*; falta el *quién* y el *cuándo*.
- [ ] **Paginación en los listados.** Ya hay filtro por texto en clientes, conectores y
      usuarios, que resuelve el caso de «encontrar uno». Con miles de filas haría falta además
      paginar, y eso sí necesita que la API de dex lo soporte: hoy `ListClients` devuelve todo.
- [ ] **Sesiones compartidas entre réplicas.** Hoy viven en memoria del proceso: un reinicio
      pide login otra vez y el panel no sobrevive a estar replicado. Va de la mano de la caché
      distribuida de la sección 2.
- [ ] **htmx.** El panel no sirve JavaScript y la CSP está en `default-src 'none'`. Entra
      cuando alguna pantalla gane algo real con actualización parcial, no antes.
