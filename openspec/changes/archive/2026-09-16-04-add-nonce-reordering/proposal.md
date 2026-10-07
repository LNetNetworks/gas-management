## Why

Es la brecha de fondo contra el relayer en Node y la razon de ser de toda la serie: **el
RelaySigner en Go no reserva nonces**. La cache `senders` (`service/relaySignerService.go:715-758`)
solo alimenta `eth_getTransactionCount(pending)`; `controller/processController.go` no mira el
nonce en ningun momento. La metatx se manda y decide el hub on-chain, asi que:

- Un usuario no puede encadenar dos metatx antes de que la primera se mine: la segunda llega con
  un nonce que el hub todavia no acepta y responde `BadTransactionSent(BadNonce)` **con la tx del
  writer node ya gastada**. En Node se midieron 6 metatx del mismo usuario en 2-3 bloques.
- Sobre HTTP el orden de llegada no esta garantizado. Una metatx adelantada por la red muere en
  vez de esperar a que se cierre el hueco.

Node lo resuelve con un tracker autoritativo de lo en vuelo mas un buffer de reordenamiento
(`node_relayer/src/relayer.ts:211,435,758`; `COMPARACION-GO-NODE.md`, secciones 1 y 2).
Corresponde a las fases **F4 y F5** de `docs/PLAN-reorden-dashboard.md`.

## What Changes

- **Tracker de nonces en vuelo** por `(nodo, usuario)`: reemplaza la cache `senders` por una
  estructura autoritativa — nonce on-chain mas las enviadas sin receipt. Pasa a servir
  `eth_getTransactionCount(pending)` y el `nextNonce` / `pending` de `GET /nonce/{address}`.
  Incluye la **gracia antes de olvidar la cadena** que tiene Node (`releasePending`): si el
  tracker se vaciara al instante del ultimo receipt, en medio de una rafaga el `nextNonce` caeria
  al nonce minado y un reintento resincronizaria a un numero que otra metatx ya tomo.
- **Buffer de reordenamiento**: si el nonce que llega es mayor al esperado, la metatx espera a que
  se cierre el hueco en vez de gastar una tx del writer node. La ventana (`reorder.windowMs`,
  default 3000) mide **estancamiento, no espera total**: se renueva cada vez que la cadena avanza,
  para que una rafaga larga no pierda la cola por reloj estando todo sano. Al vencer se responde
  el error de siempre, sin haber gastado nada.
- **Tope de metatx en vuelo por usuario** (`reorder.maxInflightPerUser`, default 16), que acota el
  dano cuando la cadena de nonces se rompe: al rechazarse la metatx k, las k+1..k+n ya salieron y
  cada una gasta una tx del writer node.
- **Watcher de receipts**: emite `relay.settled` y libera el tracker. Se cuelga de la suscripcion
  a bloques que ya existe (`ProcessNewBlocks`) o corre como poller unico.
- **Acotar el `lock` global** (`processController.go:163`), que hoy serializa **todos** los relays
  de punta a punta. Pasa a cubrir solo el envio, como en Node. Sin esto el reordenamiento no
  compra nada, y es el punto de mayor riesgo del cambio.
- **Emision de `relay.held` y `relay.turn`** con los campos que el dashboard espera.
- **Apagado por defecto** (`reorder.enabled = false`): con el flag en falso el comportamiento es
  **byte a byte** el de hoy, y esa equivalencia se verifica explicitamente.

Fuera de alcance: el pre-chequeo por simulacion `eth_call`, el cursor de nonces del lado cliente
(vive en el `MetaTxClient` de Node, no en el relayer) y el multi-instancia — el tracker vive en
memoria del proceso, igual que en Node, asi que este servicio debe seguir siendo el unico que use
la clave del writer node.

## Capabilities

### New Capabilities
- `metatx-nonce-tracking`: el tracker autoritativo — que reserva, cuando valida, como se libera,
  la gracia tras el ultimo receipt, y como se descarta una cadena rota sin que un receipt atrasado
  toque a la que la reemplazo.
- `metatx-reordering`: el buffer — cuando se retiene una metatx, la semantica de ventana por
  estancamiento, el tope de en vuelo por usuario, y que se responde al vencer.
- `metatx-receipt-watching`: la deteccion del receipt, la emision de `relay.settled` y la
  liberacion del tracker.

### Modified Capabilities
- `relay-nonce-endpoint`: `nextNonce` y `pending` pasan a reflejar lo realmente en vuelo, y
  `?peek=true` adquiere significado real — sin `peek`, la consulta reserva.
- `relay-event-stream`: `relay.held` y `relay.turn` pasan de declarados a emitidos.
- `relay-runtime-configuration`: `[reorder]` pasa de andamiaje inerte a gobernar el camino
  critico; se especifica el efecto de cada clave.

## Impact

- **Codigo**: `service/relaySignerService.go` (la cache `senders` desaparece),
  `controller/processController.go` (el lock y el camino de envio), `blockchain/client.go`
  (posible cliente RPC compartido en vez de uno por request), y el watcher colgado de
  `ProcessNewBlocks`.
- **APIs**: sin rutas nuevas, pero **con `reorder.enabled = true` cambia una semantica
  observable**: `eth_sendRawTransaction` de una metatx adelantada no responde hasta que le toca el
  turno o vence la ventana. Es inherente a reordenar sobre HTTP y es lo que hace Node.
- **Riesgo**: es el unico cambio de la serie que toca el camino critico y que hoy funciona en
  produccion. Mitigado por el flag apagado, por ir ultimo en la serie, y por la validacion de
  cierre. Acotar el lock global puede introducir carreras: tests con `-race`, el tracker con su
  propio mutex y sin llamadas RPC dentro del lock.
- **Validacion de cierre** (F5): rafagas de 6, 12 y 20 metatx con uno y varios usuarios; caso de
  `maxInflightPerUser` excedido; caso de metatx que vence en el buffer;
  `node_relayer/examples/nonce-stress.ts --n 6` apuntado a este servicio debe dar el mismo
  resultado que contra Node; comparacion con la corrida de referencia de
  `node_relayer/DASHBOARD.md` (llegada `72,68,70,67,71,69` -> envio `67..72`, 4 reordenadas,
  espera media 2.24 s); y verificar la equivalencia byte a byte con el flag apagado.
- **Documentacion**: README, y actualizar `node_relayer/ENDPOINTS-GO-VS-NODE.md` con las
  diferencias que esta serie elimina.
- **Restriccion heredada**: `go-ethereum v1.9.15` (2020) limita lo disponible; no se actualiza en
  este cambio.
- **Depende de**: `01-add-relay-event-bus`, `02-add-relay-http-endpoints` (la logica extraida a
  `service/`) y `03-add-relay-dashboard` (para observar el reordenamiento durante la validacion).
