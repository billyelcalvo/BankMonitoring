# BankMonitoring

Backend en Go con `net/http`, `github.com/golang-jwt/jwt/v5` para JWT y `pgx/v5` para PostgreSQL.

## Requisitos

- Go 1.25 o superior (requerido por pgx v5.11).
- PostgreSQL 13 o superior.

## Estructura

```text
cmd/api/main.go             Entrada, configuración y ciclo de vida del servidor
internal/service/auth/     Emisión de JWT, autenticación y permisos
internal/httpapi/routes.go  Rutas y handlers HTTP
internal/domain/entities/  Entidades de transferencias
internal/domain/valueobjects/  Estados de transferencias
internal/domain/repositories/  Contratos de persistencia del dominio
internal/repository/       Pool pgx e implementación de persistencia
internal/repository/migrations/  Migraciones SQL
go.mod                     Módulo y dependencias
```

## Ejecutar

```sh
export JWT_SECRET="$(openssl rand -base64 32)"
export DATABASE_URL='postgres://usuario:clave@localhost:5432/bankmonitoring?sslmode=disable'
go run ./cmd/api
```

`JWT_SECRET` es una clave aleatoria de al menos 32 bytes, codificada en base64.
El servidor rechaza claves ausentes, inválidas o demasiado cortas. Conserva la
misma clave entre reinicios e instancias; cambiarla invalida los tokens anteriores.
No la guardes en el repositorio.

El servidor escucha en `:8080`. Para cambiar la dirección en Bash:

```sh
HTTP_ADDR=127.0.0.1:3000 go run ./cmd/api
```

La configuración se lee de las variables del entorno; no se cargan archivos `.env` automáticamente.

`DATABASE_URL` se lee en `repository.NewPool`. El pool se crea al arrancar y se
cierra al apagar el servidor; no se ejecuta un `Ping`. El ejemplo de conexión es
para desarrollo local. Ajusta las credenciales y TLS a tu servidor PostgreSQL.

## Persistencia e idempotencia

Antes de usar el repositorio, aplica una vez la migración sobre tu base de datos:

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f internal/repository/migrations/001_create_transfers.sql
```

La aplicación no ejecuta migraciones automáticamente.

El frontend debe generar una clave con `crypto.randomUUID()` por cada operación
y reutilizarla al reintentar esa misma operación. La clave de idempotencia es
independiente del ID de la transferencia, que genera PostgreSQL.

El repositorio se usa desde el servicio así:

```go
transfers := repository.NewTransferRepository(pool)
transfer, created, err := transfers.CreateIdempotent(ctx, userID, idempotencyKey, request)
```

`userID` debe provenir del `sub` del JWT verificado. La clave se busca dentro de
ese usuario, de modo que otro usuario no pueda recuperar sus transferencias.

- Clave nueva: guarda la transferencia en estado `pending` y devuelve `created=true`.
- Misma clave y mismos campos: devuelve la transferencia existente con su estado
  actual y `created=false`.
- Misma clave con cambios de origen, destino, monto, moneda o descripción:
  devuelve `repositories.ErrIdempotencyConflict` (para mapear a HTTP 409).
- UUID inválido, monto no positivo, cuentas vacías o iguales, o moneda sin tres
  letras mayúsculas: devuelve `repositories.ErrInvalidTransfer`.

Se conserva la solicitud original en JSONB para compararla aunque cambie el
estado de la transferencia. Se comparan campos, no el orden ni los espacios del
JSON recibido. No se deben modificar `original_request`, `user_id` ni
`idempotency_key`, ni borrar registros mientras se admitan reintentos.

La restricción única `(user_id, idempotency_key)` junto con
`INSERT ... ON CONFLICT DO NOTHING` impide crear duplicados concurrentes. Si otra
petición insertó primero, se consulta su resultado en una nueva sentencia y se
compara la solicitud. Véase [ON CONFLICT de PostgreSQL](https://www.postgresql.org/docs/current/sql-insert.html).

Esta implementación persiste transferencias; todavía no ejecuta movimientos de
dinero ni expone una ruta HTTP para crearlas. El servicio deberá verificar el
permiso y el acceso a la cuenta antes de llamar al repositorio.

## Comprobar el servidor

```sh
curl -i http://localhost:8080/health
```

Devuelve HTTP 200 con `Content-Type: application/json`:

```json
{"status":"ok"}
```

El endpoint indica que el servidor está activo. Las rutas desconocidas devuelven 404 y los métodos no admitidos, 405. `GET /health` también admite `HEAD` por el comportamiento de `net/http`.

## Desarrollo

```sh
go fmt ./...
go vet ./...
go test ./...
go build -o bin/api ./cmd/api
```

Los tests cubren validación de JWT, rechazo de tokens alterados o vencidos,
autenticación HTTP, permisos, rutas protegidas y la lógica de idempotencia.
Los tests del repositorio usan respuestas simuladas y no conectan a PostgreSQL;
la migración y la concurrencia real requieren pruebas de integración posteriores.

## Autenticación y autorización

Un único JWT firmado con HS256 contiene `sub` (ID interno del usuario),
`permissions`, `iss`, `aud`, `iat` y `exp`. Su vigencia es de 15 minutos.
El emisor es `bankmonitoring` y la audiencia es `bankmonitoring-api`.
El payload se puede leer: no contiene contraseñas, correo ni datos bancarios.

`auth.TokenService.Issue(userID, permissions)` emite el token desde código del
servidor. Se debe llamar después de verificar las credenciales del usuario y
obtener sus permisos de una fuente confiable. Aún no hay almacenamiento de
usuarios, endpoint de login, renovación ni revocación de tokens. Los permisos
incluidos en un token siguen vigentes hasta su vencimiento.

`GET /health` es público. `GET /me` requiere un token y devuelve `user_id` y
`permissions`:

```sh
curl -i http://localhost:8080/me \
  -H "Authorization: Bearer $ACCESS_TOKEN"
```

`ACCESS_TOKEN` debe contener un token emitido por el servidor. El middleware
`Authenticate` valida la firma, el algoritmo, el emisor, la audiencia y las
fechas; guarda los claims validados en el contexto de la petición. Devuelve
401 si falta el token o es inválido.

Los permisos disponibles son `transfers:read` y `transfers:create`. Para proteger
una operación, se combinan los middleware en este orden:

```go
tokens.Authenticate(auth.RequirePermission(auth.PermissionTransfersCreate, handler))
```

`RequirePermission` devuelve 403 si el usuario autenticado no tiene el permiso.
Las futuras operaciones de transferencia también deberán comprobar que el
usuario tiene acceso a la cuenta de origen; el permiso general no lo garantiza.
Todavía no hay endpoints de transferencias.

La validación usa las opciones documentadas de
[golang-jwt](https://golang-jwt.github.io/jwt/usage/parse/).

Los logs se escriben en JSON a la salida estándar. Al recibir `Ctrl+C` o `SIGTERM`, el servidor deja de aceptar conexiones y espera hasta 10 segundos a que terminen las solicitudes activas.
