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
  (`enabled`, `windowMs`, `maxInflightPerUser`, `receiptTimeoutMs`), `[dashboard]` (`enabled`,
  `bufferSize`) y `[log]` (`level`, `rawTx`), todos con defaults conservadores y ambos `enabled`
  en `false`. El bloque `[log]` es el equivalente de las variables `LOG_LEVEL` y `LOG_RAW_TX` de
  Node, que aqui viven en `config.toml` para no introducir variables de entorno donde el resto del
  servicio no las usa. Un `config.toml` existente, sin esas claves, sigue arrancando igual.
- **Emisor de log estructurado** en `audit/`: una linea JSON por evento con `event`, `ts`,
  `level`, `instanceId`, `reqId` y los campos propios del evento, hacia la salida estandar
  (`warn` y `error` por la de error, para que journald los clasifique). Va **en paralelo** al log
  de texto actual, que no se toca porque hay operadores que lo parsean.
- **`reqId` por request HTTP**, generado en el handler y propagado por `context.Context` — el
  equivalente del `AsyncLocalStorage` de Node —, mas un **`metaTxId` por metatx** e
  **`instanceId` por proceso**. El `metaTxId` es obligatorio: la pagina indexa cada metatx por
  ese campo y descarta el evento que no lo trae. El `instanceId` es lo que permite ver en el log
  si dos instancias atendieron a la vez, escenario que rompe la cadena de nonces.
- **Bus de eventos en memoria** en `events/`: ring buffer de `dashboard.bufferSize` (default 500)
  con `seq` incremental, `Subscribe`/`Unsubscribe` y `Replay(afterSeq)`. Con
  `dashboard.enabled = false`, `Publish` retorna sin costo. El bus es un **derivado del log**, no
  una instrumentacion aparte: el emisor estructurado publica en el bus, asi que no hay dos
  verdades sobre lo que paso y agregar un evento al log lo agrega al dashboard. El nivel de log
  regula la consola, no el bus: un evento por debajo del nivel configurado igual se publica.
- **Contrato de eventos congelado**, identico al de Node, porque el frontend del dashboard se
  porta tal cual y depende de los nombres y campos exactos. Todo evento referido a una metatx
  lleva `metaTxId` — la pagina descarta los que no lo traen—, ademas de los campos comunes
  `ts`, `level`, `event`, `instanceId`, `reqId` y el `seq` del bus:
  `relay.received` (`rawTxHash`, `rawTxBytes`), `relay.decoded` (`from`, `to`, `isDeploy`,
  `nonce`, `userGasLimit`, `metaTxGasLimit`, `nodeAddress`, `expiration`, `expiresInSeconds`,
  `dataBytes`, `selector`), `relay.held` (`nonce`, `expected`, `gap`, `windowMs`), `relay.turn`
  (`heldMs`, `reason`), `relay.sent` (`transactionHash`, `hubNonce`, `writerNodeNonce`,
  `metaTxGasLimit`, `simulated`, `simulatedErrorCodeName`, `pendingForUser`), `relay.settled`
  (`blockNumber`, `gasUsed`, `executed`, `errorCodeName`, `deployedAddress`), `relay.rejected`
  (`error`, y `code` / `errorType` cuando los trae), `relay.hub_rejected` (`transactionHash`,
  `from`, `errorCode`, `errorCodeName`) y `relay.settle_failed` (`error`, `code`, `errorType`).
  Los dos ultimos estaban fuera del plan y la pagina portada los consume: fijan estado terminal,
  alimentan el contador de fallidas y la marca de la linea de tiempo.
- **Emision de los eventos que el camino actual ya puede producir**: `relay.received`,
  `relay.decoded`, `relay.sent`, `relay.rejected` y `relay.hub_rejected` —este ultimo donde el
  servicio ya detecta `BadTransactionSent` al procesar un receipt e invalida el nonce del sender—.
  `relay.held` / `relay.turn` los emite `04-add-nonce-reordering`, y `relay.settled` /
  `relay.settle_failed` el watcher de receipts de esa misma propuesta.
- **Correlacion del cierre entre peticiones**: en este servicio el receipt no llega en la peticion
  que relayo la metatx, sino en la que el cliente hace despues para consultarlo. Un mapa en memoria
  de `txHash` al `reqId` y `metaTxId` originales, con TTL y tope de entradas, es lo que permite que
  un evento de cierre siga perteneciendo a su metatx. Sin el, la pagina lo descarta en silencio.
- **Campos del contrato que hoy no se calculan**: se decodifica el sufijo del gas model —los
  ultimos 64 bytes del `data`, con `nodeAddress` y `expiration`— **solo para registrarlo, nunca
  para validar**, y `blockchain/client.go` pasa a devolver la transaccion enviada en vez de solo su
  hash, para poder informar el nonce del writer node. La respuesta de `eth_sendRawTransaction` no
  cambia.

No hay cambios de comportamiento observables: el contrato JSON-RPC de `POST /` queda intacto.

## Capabilities

### New Capabilities
- `relay-runtime-configuration`: como se cargan y validan los bloques `[reorder]`, `[dashboard]` y
  `[log]`, sus defaults, el descarte de un valor invalido, y la garantia de que un `config.toml`
  sin esas claves arranca con el comportamiento actual.
- `relay-structured-logging`: formato de la linea JSON por evento, campos obligatorios, y la
  convivencia con el log de texto existente.
- `relay-event-stream`: el bus en memoria (ring buffer, `seq`, suscripcion, replay) y el
  **contrato de eventos** — nombre y campos de cada uno — que consumen el dashboard y los clientes.

### Modified Capabilities
<!-- Ninguna: no cambia el comportamiento de ninguna capacidad ya especificada. -->

## Impact

- **Codigo**: `model/Config.go`, `config.toml`, `audit/` (emisor JSON nuevo, sin tocar el
  existente), paquete nuevo `events/`, `blockchain/client.go` (devolver la transaccion enviada en
  lugar de solo su hash), y puntos de emision en `controller/relayController.go`,
  `controller/processController.go` y `service/relaySignerService.go`, que ademas aloja el mapa de
  correlacion y la decodificacion del sufijo del gas model.
- **APIs**: ninguna. No se agregan rutas ni cambia ninguna respuesta.
- **Operacion**: un archivo/stream de log adicional. El log de texto que hoy se parsea no cambia.
- **Dependencias**: ninguna nueva; ring buffer y JSON con la libreria estandar.
- **Aguas abajo**: habilita `03-add-relay-dashboard` y `04-add-nonce-reordering`. Congelar el contrato
  de eventos aqui es lo que permite portar el frontend de Node sin modificarlo.
