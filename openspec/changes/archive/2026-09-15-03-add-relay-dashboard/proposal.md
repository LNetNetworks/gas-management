## Why

Cuando una rafaga de metatx sale mal en el RelaySigner en Go, la unica evidencia es
`./log/idbServiceLog.log` leido despues del hecho. El relayer en Node tiene un monitor en vivo
—`GET /dashboard` + `GET /dashboard/stream`, `node_relayer/src/dashboard.ts` y
`src/dashboard-page.ts`— que muestra la cola de nonces, que metatx se retuvo y cuanto, y como
quedo cada una. Es la herramienta con la que se diagnostica el reordenamiento, y aqui no existe
nada equivalente (`node_relayer/ENDPOINTS-GO-VS-NODE.md`, seccion 3).

Va antes que `04-add-nonce-reordering` a proposito: sin el dashboard, validar el buffer de
reordenamiento es leer logs a mano. Corresponde a la fase **F3** de
`docs/PLAN-reorden-dashboard.md`.

## What Changes

- **`GET /dashboard/stream`**: SSE sobre el bus de eventos de `01-add-relay-event-bus`. Reanudacion
  con `?after=<seq>` y con la cabecera `last-event-id`, heartbeat cada 15 s,
  `X-Accel-Buffering: no` para que ningun proxy lo bufferee, y techo de 8 clientes simultaneos —
  el noveno recibe `503`. Solo **lee** el bus: no toca el camino de la metatx.
- **`GET /dashboard`**: la pagina. Se porta `node_relayer/src/dashboard-page.ts` a
  `dashboard/index.html` y se sirve con `go:embed` — sin build, sin dependencias JS. La pagina no
  se modifica: consume el mismo vocabulario de eventos que `01-add-relay-event-bus` congelo.
- **Cabeceras CORS configurables** (hoy el servicio no manda ninguna), lo que ademas destraba que
  un dapp de browser llame al relayer directo.
- **Apagado por defecto**: con `dashboard.enabled = false` no se registran las rutas y el bus no
  publica nada.

## Capabilities

### New Capabilities
- `relay-dashboard`: las dos rutas del monitor — el stream SSE (replay, heartbeat, limite de
  clientes, codigos de respuesta) y la entrega de la pagina embebida —, y la garantia de que
  ninguna de las dos altera el camino de la metatx.
- `relay-cors`: que cabeceras CORS emite el servicio, en que rutas, y como se configuran.

### Modified Capabilities
- `relay-http-routing`: se agregan `GET /dashboard` y `GET /dashboard/stream` al ruteo por path, y
  su registro queda condicionado a `dashboard.enabled`.

## Impact

- **Codigo**: `main.go` (registro condicional de rutas), paquete nuevo `dashboard/` con el
  handler SSE y el `index.html` embebido, y un consumidor del bus de `events/`.
- **APIs**: dos rutas nuevas, solo cuando el flag esta activo.
- **Rendimiento**: el stream lee el ring buffer; el techo de 8 clientes y el `bufferSize` acotan
  la memoria. Una conexion SSE ocupa una goroutine por cliente.
- **Seguridad**: **el dashboard expone `from`, hashes y gas de todas las metatx, y no tiene
  autenticacion**. Por eso el default es `enabled = false` fuera de local, y por eso queda solo en
  `:9001`: el nginx del writer enruta por metodo leyendo el body, asi que un `GET /dashboard` por
  el puerto 80 termina en besu `:4545`. Exponerlo de verdad exige tocar la plantilla de
  `besu-networks`, que va como PR aparte y fuera del alcance de esta propuesta.
- **Riesgo**: si el emisor de eventos se desincroniza del frontend portado, la pagina deja de
  pintar sin error visible. Lo cubre el contrato congelado en `01-add-relay-event-bus`.
- **Depende de**: `01-add-relay-event-bus` (bus, `bufferSize`, contrato de eventos) y de
  `02-add-relay-http-endpoints` (router por path).
