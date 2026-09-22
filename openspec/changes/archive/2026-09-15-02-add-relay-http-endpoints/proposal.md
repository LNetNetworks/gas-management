## Why

El RelaySigner en Go expone **una sola ruta**: `mux.HandleFunc("/", ...)` en `main.go:78`, un
catch-all JSON-RPC. No hay health check, ni `/info`, ni forma de preguntar el nonce sin hablar
JSON-RPC, ni un camino que espere el resultado de la metatx. El relayer en Node tiene tres rutas
REST que hoy no tienen equivalente aqui — `GET /info`, `GET /nonce/:address` y `POST /relay`
(`node_relayer/src/app.ts`) — y son las que usan los scripts, los integradores y quien despliega un
contrato y necesita el `relayHubProxyAddress` para el `trustedForwarder`. Hoy esos valores solo
estan en `config.toml` y en el log de arranque.

Es la seccion 3 de `node_relayer/ENDPOINTS-GO-VS-NODE.md` y la fase **F2** de
`docs/PLAN-reorden-dashboard.md`.

## What Changes

- **Router por path** en `main.go`, manteniendo `/` como catch-all JSON-RPC para no romper a los
  clientes actuales ni al nginx del writer node, que enruta por metodo.
- **`GET /info`**: `nodeAddress`, `relayHubAddress` + `relayHubSource`, `relayHubProxyAddress`,
  `chainId`, `rpcUrl`, `nodeBalance`, `currentGasLimit` (ya se calcula), el estado del
  permissioning y los parametros de reorden. Campo a campo contra la respuesta de Node, salvo los
  que aqui no aplican.
- **`GET /nonce/{address}`**: `{address, nonce, nonceHex, nextNonce, nextNonceHex, pending}`, con
  `?peek=true` para consultar sin reservar. Mientras el tracker de `04-add-nonce-reordering` no
  exista, `nextNonce` y `pending` se sirven de la cache `senders` actual y `pending` es 0 o 1;
  esa propuesta los vuelve autoritativos sin cambiar la forma de la respuesta.
- **`POST /relay`**: acepta `{rawTx}` (y el alias `signedTransaction`), **espera** el receipt con
  techo `reorder.receiptTimeoutMs` y devuelve el resultado decodificado — `transactionHash`,
  `isDeploy`, `deployedAddress` (del evento `ContractDeployed`, no del receipt), `blockNumber`,
  `gasUsed`, `errorCode`, `errorCodeName`, `executed`, `from`, `to`, `nonce`, `metaTxGasLimit`,
  `output`, `events`. Error `400` con `{error, code, details}` y los `code` del catalogo de Node.
- **Extraccion del decodificado y las validaciones** de `controller/processController.go` a
  `service/`, hoy acopladas al camino JSON-RPC, para que `POST /relay` y `POST /` compartan la
  misma logica en vez de duplicarla. Es un refactor sin cambio de comportamiento para `POST /`.
- **`POST /` no cambia**: mismos metodos, mismas respuestas, mismos codigos de error.

Fuera de alcance de esta propuesta, aunque sean brechas reales contra Node: el passthrough crudo
de los metodos no interceptados, los batches JSON-RPC, `eth_subscribe` sobre WebSocket de cara al
cliente y las cabeceras CORS (estas ultimas llegan con `03-add-relay-dashboard`).

## Capabilities

### New Capabilities
- `relay-http-routing`: el ruteo por path del servicio — que rutas existen, cual es el catch-all,
  y la garantia de que `POST /` conserva su comportamiento actual.
- `relay-info-endpoint`: forma y semantica de `GET /info`, incluido como se reporta el origen de
  cada direccion resuelta.
- `relay-nonce-endpoint`: forma y semantica de `GET /nonce/{address}`, la diferencia entre `nonce`
  y `nextNonce`, y el efecto de `?peek=true`.
- `relay-sync-relay-endpoint`: `POST /relay` — espera del receipt, forma del resultado
  decodificado, catalogo de codigos de error y comportamiento ante timeout.

### Modified Capabilities
<!-- Ninguna. `relayhub-address-resolution` se consume desde /info pero sus requisitos no cambian. -->

## Impact

- **Codigo**: `main.go` (router), `controller/` (handlers nuevos), `service/` (logica extraida de
  `processController`), `relayhub/` (lectura del proxy y del hub para `/info`).
- **APIs**: tres rutas nuevas. Ninguna ruptura: lo que hoy responde `POST /` sigue igual, y los
  paths que antes caian en el catch-all y ahora tienen handler propio (`/info`, `/nonce/*`,
  `/relay`) no eran usables como JSON-RPC en la practica.
- **Despliegue**: el nginx del writer node enruta por metodo leyendo el body, asi que estas rutas
  **no son alcanzables por el puerto 80**; quedan en `:9001` (red interna o tunel SSH). Abrirlas
  exige tocar la plantilla de `besu-networks`, que va como PR aparte.
- **Seguridad**: como el resto del servicio, sin autenticacion. `/info` expone direcciones y
  balance del writer node, y `POST /relay` consume su cupo de gas igual que el JSON-RPC.
- **Depende de**: `01-add-relay-event-bus` (config y `reqId`). **Habilita**: el refactor a `service/`
  es lo que le permite a `04-add-nonce-reordering` interponerse en un solo lugar.
