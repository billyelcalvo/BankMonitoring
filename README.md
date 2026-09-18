# BankMonitoring

Backend en Go usando exclusivamente la biblioteca estándar (`net/http`).

## Requisitos

- Go 1.22 o superior.

## Estructura

```text
cmd/api/main.go             Entrada, configuración y ciclo de vida del servidor
internal/httpapi/routes.go  Rutas y handlers HTTP
go.mod                     Módulo, sin dependencias externas
```

## Ejecutar

```sh
go run ./cmd/api
```

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

Aún no hay tests automatizados. `go test ./...` permite comprobar que los paquetes compilan.

Los logs se escriben en JSON a la salida estándar. Al recibir `Ctrl+C` o `SIGTERM`, el servidor deja de aceptar conexiones y espera hasta 10 segundos a que terminen las solicitudes activas.
