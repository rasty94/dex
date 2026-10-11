# API gRPC de gestión — convenciones

> Revisión del 2026-10-11. Para quien añada RPCs o campos a `api/v2/api.proto`, o
> escriba un cliente de la API. Las reglas valen para lo **nuevo**; lo que ya existe se
> deja como está salvo donde se dice lo contrario, y lo pendiente va en la última
> sección.

## La restricción que manda: el proto es de upstream

`api/v2/api.proto` y los handlers de `server/apiserver/` son de `dexidp/dex`, y upstream
los toca en casi cada sincronización (la de octubre añadió `Password.groups = 5` y
`UpdatePasswordReq.new_groups = 4`). Cada línea que cambiemos ahí es un conflicto en el
siguiente merge, y cada número de campo que ocupemos puede chocar con uno suyo. De ahí
salen tres reglas:

1. **Lo nuestro, fuera de sus ficheros.** Si algo se puede resolver en `cmd/dex`, en un
   interceptor o en el panel, se hace ahí. Los códigos de error son el ejemplo.
2. **Nada de campos nuestros en mensajes suyos.** Hoy lo único propio del fork en el
   proto es la RPC `ReloadConfig` con sus dos mensajes. Si alguna vez hace falta un
   campo en un mensaje de upstream, que sea a partir del **número 1000**, lejos de los
   que ellos van ocupando en orden.
3. **Compatibilidad de cable siempre.** No se renumera, no se cambia el tipo de un
   campo y no se quita nada que un cliente pueda estar mandando.

## Errores

### Lo que había

La apiserver construye sus errores con `errors.New` y `fmt.Errorf("…: %v", err)`; no
había ni un `status.Error`. Todos llegaban al cliente como `codes.Unknown`, así que un
campo que falta, un objeto de solo lectura y una base de datos caída eran
indistinguibles salvo leyendo el texto. Además conviven otros dos estilos:

- **Booleanos en la respuesta** (`not_found`, `already_exists`; 17 en el proto) para
  «no existe» y «ya existe». Son de upstream, y el panel depende de ellos.
- **Error en el cuerpo**: `ReloadConfigResp` devuelve `success` y `error` con la
  llamada en OK.

### Lo que hay ahora

Un interceptor en `cmd/dex/grpc_codes.go`, el más interno de la cadena, asigna el
código sin tocar el mensaje:

| Código | Cuándo |
| --- | --- |
| `InvalidArgument` | falta un campo (`no … supplied`), `nothing to update`, grant type desconocido, grupo vacío, caducidad de refresh token inválida, hash bcrypt inválido o de coste fuera de rango, `sub` que no es base64 o protobuf |
| `NotFound` | `storage.ErrNotFound`, también cuando llega aplanado por `%v` (`get …: not found`) |
| `AlreadyExists` | `storage.ErrAlreadyExists` |
| `FailedPrecondition` | objeto estático del fichero de configuración (`read-only`), purga RGPD bloqueada por una contraseña estática, discovery sin configurar |
| `Unimplemented` | la RPC está tras un feature flag apagado |
| `Internal` | todo lo demás: es un fallo de dex o de su almacenamiento |
| *(sin tocar)* | un error que ya trae código, como el `Unauthenticated` del token |

- **El mensaje no cambia.** El panel y cualquier cliente que case por texto siguen
  funcionando. Lo correcto, para un cliente nuevo, es mirar el código.
- **Los booleanos se mantienen.** Una llamada que hoy contesta `not_found: true` sigue
  contestando OK con el booleano: cambiarlo rompería a los clientes de upstream.
- **La clasificación lee mensajes**, porque los handlers aplanan los errores con `%v` y
  pierden el tipo. Para que eso no se pudra en silencio,
  `TestEveryAPIServerMessageIsClassified` recorre el código de `server/apiserver` y
  falla si algún mensaje construido allí acabaría en `Internal`. **Cuando falle tras un
  merge, upstream ha añadido un mensaje: se añade la regla en `grpcCode`**, no se
  relaja el test.

### Para lo nuevo

- Una RPC nuestra devuelve `status.Error` con su código desde el handler; el
  interceptor respeta el que ya venga puesto.
- «No existe» en una RPC nuestra es `NotFound`, no un booleano.
- Nada de errores dentro del cuerpo con la llamada en OK (`ReloadConfig` se queda así
  por compatibilidad con quien ya lo use).

## Vaciar un campo

Un campo `repeated` vacío no viaja en protobuf: el servidor no puede distinguir «déjalo
como está» de «vacíalo». Hoy conviven tres soluciones:

| Forma | Dónde | ¿Se puede vaciar? |
| --- | --- | --- |
| Lista repetida a secas | `UpdateClientReq` (redirect URIs, trusted peers, allowed connectors…), `UpdatePasswordReq.new_groups` | No: se reemplaza, nunca se vacía |
| Escalar `optional` | `backchannel_logout_uri`, `refresh_token_lifetime` | Sí: presencia explícita |
| Lista envuelta en un mensaje | `UpdateConnectorReq.new_grant_types` | Sí: el mensaje viaja aunque la lista vaya vacía |

**Regla para lo nuevo:** un escalar que haya que poder vaciar es `optional`; una lista
que haya que poder vaciar va envuelta en un mensaje, como `new_grant_types`. No se usa
`FieldMask`: obliga a cambiar el contrato de cada `Update*` y nos separaría del proto de
upstream sin ganar nada sobre las otras dos formas.

**Lo que ya existe no se cambia**, por la restricción de arriba. Lo que sí se hace es no
fingir: el panel avisa de que una lista de cliente vaciada no cambia, y **se niega** a
vaciar los grupos de un usuario, porque los grupos dan permisos y un éxito aparente
dejaría puesto uno que el administrador cree haber quitado.

## Validación

La API es la autoridad: lo que no valide ella lo puede guardar cualquiera que tenga el
token, sin pasar por el panel. El panel puede validar antes, por usabilidad, pero no
en lugar de la API.

Hoy no se cumple en todo: ver la lista de pendientes.

## Listados

`List*` devuelve todo de una vez. **Regla para lo nuevo:** un listado que pueda crecer
sin techo (sesiones, identidades) lleva `page_size` y `page_token` desde el principio.
En los de upstream no se añade nada mientras no lo hagan ellos; la paginación del panel
sigue esperando (sección 6 de [TODO.md](../TODO.md)).

## Autorización y auditoría

Revisado sobre las 32 RPCs del proto:

- **Autenticación**: el interceptor de token se encadena para todo el servidor, así que
  ninguna RPC unaria se lo salta; no hay RPCs de stream. Sin tokens, la única
  autenticación es TLS mutuo (`grpc.tlsClientCA`); sin ninguno de los dos, la API queda
  abierta y dex solo lo avisa al arrancar.
- **Auditoría**: todas las RPCs que cambian estado empiezan por uno de los prefijos que
  registra el interceptor de auditoría (`Create`, `Update`, `Delete`, `Revoke`,
  `Terminate`, `Reset`, `ReloadConfig`), con el token (`caller`) y el actor. Una RPC
  nueva que cambie estado con otro verbo (`Purge…`, `Rotate…`) **no se auditaría**: hay
  que añadir su prefijo a `mutatingPrefixes`.
- `VerifyPassword` no se audita porque no cambia nada, pero sirve para probar
  contraseñas sin pasar por el limitador de login. Quien tiene el token ya es
  administrador total, así que no abre nada nuevo; conviene saberlo al repartir tokens.

## Pendiente

Lo que la revisión dejó abierto está en la sección 7 de [TODO.md](../TODO.md).
