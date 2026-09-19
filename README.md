# BankMonitoring

Backend en Go con `net/http` y `github.com/golang-jwt/jwt/v5` para JWT.

## Requisitos

- Go 1.22 o superior.

## Estructura

```text
cmd/api/main.go             Entrada, configuración y ciclo de vida del servidor
internal/auth/             Emisión de JWT, autenticación y permisos
internal/httpapi/routes.go  Rutas y handlers HTTP
internal/domain/entities/  Entidades de transferencias
internal/domain/valueobjects/  Estados de transferencias
go.mod                     Módulo y dependencias
```

## Ejecutar

```sh
export JWT_SECRET="$(openssl rand -base64 32)"
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
autenticación HTTP, permisos y rutas protegidas.

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
