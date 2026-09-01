# Ejemplo — Dex con panel de administración

Levanta Dex y el panel de administración de solo lectura, listos para probar.

```bash
docker compose up -d --build
```

Panel: **<http://127.0.0.1:5560>** · Dex: <http://127.0.0.1:5556/dex>

La primera vez tarda unos minutos porque construye la imagen desde el repo.

---

## Con qué usuario entrar

El panel exige pertenecer a un grupo administrador. Este ejemplo trae tres
identidades para que se vea la diferencia:

| Cómo entrar | Grupos | Resultado |
| ----------- | ------ | --------- |
| Botón «Entrar como usuario de prueba» | `authors` | ✅ entra |
| `admin@example.com` / `password` | `dex-admins` | ✅ entra |
| `pepe@example.com` / `password` | `usuarios` | ❌ **403** |

Pepe existe a propósito: **se autentica correctamente y aun así el panel lo
rechaza**, que es justo lo que tiene que pasar. El intento queda en el log con su
identidad y sus grupos:

```bash
docker logs dex-dashboard | grep refused
```

Quién es administrador se decide en `DEX_DASHBOARD_ADMIN_GROUPS`, dentro de
`docker-compose.yml`.

---

## Qué se ve

| Vista | Contenido |
| ----- | --------- |
| Overview | versión de Dex y recuento de clientes, usuarios locales y conectores |
| Clients | los clientes OAuth2 de `dex.yaml` |
| Connectors | «—», porque el flag `api_connectors_crud` está apagado (ver abajo) |
| Local users | `admin@example.com` y `pepe@example.com` |
| Sessions | refresh tokens de un usuario, buscando por su `sub` |

**Nada de esto puede modificar Dex.** El panel es de solo lectura.

### Ver también los conectores

El listado de conectores está detrás de un feature flag de Dex. Para activarlo,
añade al servicio `dex` del compose:

```yaml
        environment:
            - DEX_API_CONNECTORS_CRUD=true
```

y `docker compose up -d`. Sin él, el panel avisa en la propia página en vez de
fallar entera.

---

## Por qué comparten espacio de red

El servicio `dashboard` lleva `network_mode: "service:dex"`. Es lo que hace que
`127.0.0.1` signifique lo mismo dentro del contenedor que en el navegador del
host, y por eso el ejemplo funciona sin tocar `/etc/hosts` ni montar un proxy.

Es la trampa más habitual al desplegar esto: **el `issuer` y el `baseURL` tienen
que ser URLs que resuelva el navegador**, no nombres internos de la red de
Docker. Con `issuer: http://dex:5556/dex` el panel arranca sin quejarse y el
login redirige a un host que el operador no conoce.

### Cómo se hace en producción

Ahí no se comparte espacio de red. Cada servicio tiene el suyo, y ambos van
detrás de un proxy inverso con nombres públicos:

```yaml
    dex:
        # sin ports: solo alcanzable desde la red interna y el proxy
        networks: [interna]

    dashboard:
        networks: [interna]
        environment:
            - DEX_DASHBOARD_BASE_URL=https://panel.example.com
            - DEX_DASHBOARD_OIDC_ISSUER=https://dex.example.com/dex
            - DEX_DASHBOARD_GRPC_ADDRESS=dex:5557
```

Con `baseURL` en HTTPS, la cookie de sesión del panel pasa a `Secure`
automáticamente.

---

## Lo que hay que cambiar antes de usarlo de verdad

Este ejemplo tiene secretos escritos en ficheros versionados, y eso está bien
para probar en local y solo para eso:

- `grpc.token` en `dex.yaml` — **quien lo tenga es administrador total de Dex**.
- El `secret` del cliente `dex-dashboard`.
- Los usuarios estáticos con contraseña `password`.

En un despliegue real van en secretos del orquestador. El panel además acepta
`DEX_DASHBOARD_GRPC_TOKEN_FILE` para leer el token de un fichero montado en vez
de una variable de entorno.

Y el panel se publica aquí solo en `127.0.0.1` a propósito: administra el
proveedor de identidad y no debería estar accesible desde fuera del host.

---

## Parar y limpiar

```bash
docker compose down            # para los contenedores
docker compose down -v         # y borra también la base de datos de Dex
```

---

## Más

- Cómo funciona por dentro: [../../documentacion/dashboard-administracion.md](../../documentacion/dashboard-administracion.md)
- Configuración del panel: [../../cmd/dex-dashboard/README.md](../../cmd/dex-dashboard/README.md)
