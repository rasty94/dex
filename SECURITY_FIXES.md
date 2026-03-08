# Correcciones de Seguridad y SonarQube

## 1. Vulnerabilidades de Dependencias / Versiones

✅ **Corregido**: Se actualizaron las herramientas locales instalando Go 1.25.8 y se modificó `go.mod` de `1.25.0` a `1.25.8`. Esto solventa de inmediato **15 vulnerabilidades críticas** detectadas por `govulncheck` en la Standard Library de Go 1.25.0 (como `net/http`, `crypto/tls`, `crypto/x509`, etc).

## 2. Problemas detectados en SonarQube (Críticos y Blocker)

### A. Secretos, Tokens y Contraseñas Hardcodeadas

**Reglas:** `secrets:S8215`, `go:S6437`, `go:S2068`, `go:S6418`

**Ficheros afectados:**

- `Ejemplos/config.yaml`
- `examples/config-dev.yaml`
- `examples/k8s/dex.yaml`
- `examples/grpc-client/client.go`
- `server/errors_test.go`
- `server/handlers_test.go`
- `server/server_test.go`
- `server/refreshhandlers_test.go`
- `storage/ent/postgres_test.go`
- `storage/kubernetes/client_test.go`
- `storage/conformance/conformance.go`
- `storage/conformance/transactions.go`
- `connector/atlassiancrowd/atlassiancrowd.go`
- `connector/atlassiancrowd/atlassiancrowd_test.go`
- `storage/sql/config_test.go`

### B. Validación TLS/SSL Deshabilitada

**Reglas:** `go:S4830`, `go:S5527`
_(Se debe asegurar o justificar explícitamente si son mocks de tests)_

**Ficheros afectados:**

- `storage/kubernetes/storage_test.go`
- `connector/gitea/gitea_test.go`
- `connector/atlassiancrowd/atlassiancrowd_test.go`
- `connector/gitlab/gitlab_test.go`
- `connector/github/github_test.go`
- `connector/bitbucketcloud/bitbucketcloud_test.go`

### C. Cifrado Débil (RSA menor a 2048 bits)

**Reglas:** `go:S4426`

**Ficheros afectados:**

- `connector/oauth/oauth_test.go`
- `connector/oidc/oidc_test.go`

### D. Creación de Archivos con Permisos Inseguros

**Reglas:** `go:S5445`

**Ficheros afectados:**

- `storage/kubernetes/client_test.go`
