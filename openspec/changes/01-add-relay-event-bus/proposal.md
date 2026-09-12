## Why

El RelaySigner en Go solo deja rastro de lo que hace en `audit/log.go`: texto libre a
`./log/idbServiceLog.log`, sin correlacion entre las lineas de una misma request y sin forma de
consumirlo en vivo. El relayer en Node emite **una linea JSON por evento** con `reqId` propagado
(`src/log.ts`, `src/events.ts`) y publica esos mismos eventos en un bus en memoria del que se
alimenta el dashboard. Es la brecha 8 de `node_relayer/COMPARACION-GO-NODE.md`.

Va primero porque es la base de las otras tres propuestas de esta serie: el dashboard
(`03-add-relay-dashboard`) consume el bus, y el reordenamiento (`04-add-nonce-reordering`) es imposible
de diagnosticar sin la traza de por que una metatx se retuvo. Corresponde a las fases **F0 y F1**
de `docs/PLAN-reorden-dashboard.md`.

## What Changes

- **Bloques de configuracion nuevos** en `model/Config.go` y `config.toml`: `[reorder]`
  (`enabled`, `windowMs`, `maxInflightPerUser`, `receiptTimeoutMs`) y `[dashboard]` (`enabled`,
  `bufferSize`), todos con defaults conservadores y ambos `enabled = false`. Un `config.toml`
  existente, sin esas claves, sigue arrancando igual.
- **Emisor de log estructurado** en `audit/`: una linea JSON por evento con `event`, `ts`,
  `level`, `reqId` y los campos propios del evento. Va **en paralelo** al log de texto actual, que
  no se toca porque hay operadores que lo parsean.
- **`reqId` por request HTTP**, generado en el handler y propagado por `context.Context` — el
  equivalente del `AsyncLocalStorage` de Node.
- **Bus de eventos en memoria** en `events/`: ring buffer de `dashboard.bufferSize` (default 500)
  con `seq` incremental, `Subscribe`/`Unsubscribe` y `Replay(afterSeq)`. Con
  `dashboard.enabled = false`, `Publish` retorna sin costo.
- **Contrato de eventos congelado**, identico al de Node, porque el frontend del dashboard se
  porta tal cual y depende de los nombres y campos exactos: `relay.received`, `relay.decoded`,
  `relay.held` (`nonce`, `expected`, `gap`, `windowMs`), `relay.turn` (`heldMs`, `reason`),
  `relay.sent` (`transactionHash`, `hubNonce`, `writerNodeNonce`, `pendingForUser`),
  `relay.settled` (`blockNumber`, `gasUsed`, `executed`, `errorCodeName`), `relay.rejected`
  (`code`).
- **Emision de los eventos que el camino actual ya puede producir**: `relay.received`,
  `relay.decoded`, `relay.sent` y `relay.rejected`. `relay.held` / `relay.turn` los emite
  `04-add-nonce-reordering` y `relay.settled` el watcher de receipts de esa misma propuesta.

No hay cambios de comportamiento observables: el contrato JSON-RPC de `POST /` queda intacto.

## Capabilities

### New Capabilities
- `relay-runtime-configuration`: como se cargan y validan los bloques `[reorder]` y `[dashboard]`,
  sus defaults, y la garantia de que un `config.toml` sin esas claves arranca con el
  comportamiento actual.
- `relay-structured-logging`: formato de la linea JSON por evento, campos obligatorios, y la
  convivencia con el log de texto existente.
- `relay-event-stream`: el bus en memoria (ring buffer, `seq`, suscripcion, replay) y el
  **contrato de eventos** — nombre y campos de cada uno — que consumen el dashboard y los clientes.

### Modified Capabilities
<!-- Ninguna: no cambia el comportamiento de ninguna capacidad ya especificada. -->

## Impact

- **Codigo**: `model/Config.go`, `config.toml`, `audit/` (emisor JSON nuevo, sin tocar el
  existente), paquete nuevo `events/`, y puntos de emision en `controller/relayController.go`,
  `controller/processController.go` y `service/relaySignerService.go`.
- **APIs**: ninguna. No se agregan rutas ni cambia ninguna respuesta.
- **Operacion**: un archivo/stream de log adicional. El log de texto que hoy se parsea no cambia.
- **Dependencias**: ninguna nueva; ring buffer y JSON con la libreria estandar.
- **Aguas abajo**: habilita `03-add-relay-dashboard` y `04-add-nonce-reordering`. Congelar el contrato
  de eventos aqui es lo que permite portar el frontend de Node sin modificarlo.
