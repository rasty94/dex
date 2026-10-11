# Registro de cambios de la colección `rasty94.dex`

Solo lo que cambia en la colección. El registro del fork entero está en la raíz del
repositorio.

## [2.2.0] — sin publicar

Primera versión como colección. Los ocho roles (`internal_ca`, `valkey`, `dex`,
`dex_dashboard`, `haproxy`, `keepalived`, `mariadb`, `orchestrator`) y el playbook
`site.yml`, tal como se entregaron en las fases 1 a 4 del despliegue.

### Añadido

- **Política TLS del balanceador.** `haproxy_ssl_min_ver`, `haproxy_ssl_ciphers` y
  `haproxy_ssl_curves`, vacías por defecto, se traducen a `ssl-default-bind-*` en el
  `global` de HAProxy. Es donde termina el TLS en este despliegue: los
  `web.tlsCiphers`/`web.tlsCurvePreferences` que dex trae de upstream no tienen efecto
  aquí, porque dex sirve HTTP detrás del balanceador.

### Cambiado

- **`internal_ca_dir` se ancla en el inventario** (`{{ inventory_dir }}/.internal-ca`)
  y ya no en el repositorio. La CA es del despliegue, no del código: instalada la
  colección, el ancla anterior la mandaba a `~/.ansible/collections`. **Migración:**
  quien desplegara desde el checkout con `.internal-ca/` en la raíz del repositorio
  tiene que moverlo junto a su inventario antes de volver a desplegar; si no, el rol
  emite una CA nueva y reemite todos los certificados.
