## Purpose

Poner los eventos de operacion del RelaySigner a disposicion de un observador en vivo, mediante un
bus en memoria con numeracion, retencion acotada y reanudacion, y fijar el vocabulario de eventos
—nombres y campos— del que depende la pagina del dashboard para reconstruir lo que paso con cada
metatx.

## ADDED Requirements

### Requirement: El bus es un derivado del log, no una instrumentacion aparte

Todo evento que el sistema registra segun `relay-structured-logging` SHALL publicarse tambien en el
bus de eventos, sin que el codigo que lo origina tenga que emitirlo dos veces. No SHALL existir
ningun evento publicado en el bus que no provenga del log estructurado, ni ningun evento del log
estructurado que el bus omita.

Esta equivalencia es la que garantiza que la vista en vivo y el log cuenten la misma historia.

#### Scenario: Un evento registrado aparece en el bus

- **WHEN** el sistema registra un evento de operacion
- **THEN** ese mismo evento, con los mismos campos, queda disponible en el bus
- **AND** conserva su `event`, `level`, `ts`, `reqId` y `metaTxId`

#### Scenario: Agregar un evento nuevo al log

- **WHEN** se incorpora un evento de operacion nuevo al log estructurado
- **THEN** ese evento queda disponible en el bus sin trabajo adicional

### Requirement: Numeracion de secuencia

El bus SHALL asignar a cada evento publicado un numero de secuencia `seq` entero, estrictamente
creciente y sin repeticiones durante la vida del proceso. El `seq` SHALL ser el unico dato que un
observador necesita para saber desde donde continuar.

#### Scenario: Eventos consecutivos

- **WHEN** el bus publica varios eventos
- **THEN** cada uno recibe un `seq` mayor que el del evento anterior
- **AND** ningun `seq` se repite

### Requirement: Retencion acotada

El bus SHALL retener los ultimos `dashboard.bufferSize` eventos publicados. Al superarse esa
capacidad SHALL descartar los mas antiguos, conservando siempre los mas recientes. La retencion
MUST estar acotada en todo momento, de modo que una rafaga prolongada no haga crecer la memoria del
proceso sin limite.

#### Scenario: Se supera la capacidad

- **WHEN** se publican mas eventos que la capacidad configurada
- **THEN** el bus retiene exactamente los mas recientes hasta completar su capacidad
- **AND** descarta los mas antiguos
- **AND** la numeracion `seq` de los eventos retenidos no se altera

### Requirement: Reanudacion desde un punto conocido

El bus SHALL permitir recuperar los eventos retenidos posteriores a un `seq` dado. Solicitar la
reanudacion desde un `seq` anterior al mas antiguo retenido SHALL devolver todo lo retenido, sin
error.

#### Scenario: Reanudar tras una desconexion

- **WHEN** un observador solicita los eventos posteriores al ultimo `seq` que recibio
- **THEN** el bus devuelve solo los eventos retenidos con `seq` mayor a ese valor
- **AND** los devuelve en orden de `seq` creciente

#### Scenario: Reanudar desde un punto ya descartado

- **WHEN** un observador solicita los eventos posteriores a un `seq` mas antiguo que el retenido
- **THEN** el bus devuelve todos los eventos que conserva
- **AND** no devuelve error

#### Scenario: Consultar sin indicar punto de partida

- **WHEN** un observador solicita los eventos sin indicar un `seq`
- **THEN** el bus devuelve todos los eventos retenidos

### Requirement: Suscripcion a eventos nuevos

El bus SHALL permitir que un observador se suscriba para recibir los eventos que se publiquen a
partir de ese momento, y SHALL permitir cancelar esa suscripcion. Tras cancelarla, el observador
MUST NOT recibir eventos nuevos.

#### Scenario: Un suscriptor recibe lo que se publica

- **WHEN** un observador se suscribe y luego se publica un evento
- **THEN** el observador recibe ese evento

#### Scenario: Se cancela la suscripcion

- **WHEN** un observador cancela su suscripcion y luego se publica un evento
- **THEN** ese observador no recibe el evento
- **AND** los demas suscriptores siguen recibiendolo

### Requirement: La observacion nunca es causa de un fallo

Un observador que falle al recibir un evento —por error, por bloqueo o por terminacion abrupta—
MUST NOT interrumpir la publicacion hacia los demas observadores ni afectar el procesamiento de la
metatx que origino el evento. El bus SHALL aislar ese fallo y continuar.

#### Scenario: Un suscriptor falla al recibir

- **WHEN** un suscriptor falla de forma abrupta al procesar un evento
- **THEN** los demas suscriptores reciben ese mismo evento
- **AND** la peticion que origino el evento se procesa y se responde normalmente
- **AND** el proceso del servicio sigue en ejecucion

### Requirement: Con el dashboard deshabilitado el bus es inerte

Cuando `dashboard.enabled` es `false`, el bus MUST NOT retener eventos ni notificar suscriptores, y
publicar un evento MUST NOT tener costo observable sobre el procesamiento de la peticion. El log
estructurado SHALL seguir emitiendose con normalidad.

#### Scenario: Publicacion con el dashboard apagado

- **WHEN** el sistema registra eventos con `dashboard.enabled = false`
- **THEN** el bus no retiene ningun evento
- **AND** el log estructurado sigue emitiendo sus lineas
- **AND** la memoria del proceso no crece con la cantidad de eventos

### Requirement: Vocabulario de eventos

El sistema SHALL emitir los eventos de una metatx con los nombres y campos definidos aqui. Este
vocabulario es un contrato: la pagina del dashboard reconstruye el estado de cada metatx a partir
de estos nombres y campos exactos, de modo que renombrar un evento o un campo, o cambiar su
significado, rompe la vista sin producir ningun error visible.

Todo evento referido a una metatx MUST incluir `metaTxId`. Un evento sin `metaTxId` no puede
asociarse a ninguna metatx y sera ignorado por la vista en vivo.

| Evento | Significado | Campos propios |
|---|---|---|
| `relay.received` | llego una metatx por HTTP | `rawTxHash`, `rawTxBytes` |
| `relay.decoded` | se decodifico y valido su forma | `from`, `to`, `isDeploy`, `nonce`, `userGasLimit`, `metaTxGasLimit`, `nodeAddress`, `expiration`, `expiresInSeconds`, `dataBytes`, `selector` |
| `relay.held` | quedo retenida esperando su turno | `nonce`, `expected`, `gap`, `windowMs` |
| `relay.turn` | le llego el turno | `heldMs`, `reason` |
| `relay.sent` | se envio al hub | `transactionHash`, `hubNonce`, `writerNodeNonce`, `metaTxGasLimit`, `simulated`, `simulatedErrorCodeName`, `pendingForUser` |
| `relay.settled` | se resolvio en la cadena | `blockNumber`, `gasUsed`, `executed`, `errorCodeName`, `deployedAddress` |
| `relay.rejected` | se rechazo sin enviarla | `error`, y `code` / `errorType` cuando el rechazo los trae |
| `relay.hub_rejected` | el hub la rechazo al ejecutarla, sin consumir el nonce | `transactionHash`, `from`, `errorCode`, `errorCodeName` |
| `relay.settle_failed` | no se pudo determinar como termino | `error`, y `code` / `errorType` cuando los trae |

Ademas de sus campos propios, cada evento SHALL llevar los campos comunes definidos por
`relay-structured-logging` y el `seq` del bus.

En los eventos de rechazo el campo obligatorio es `error`: es el que la vista usa para mostrar el
motivo, y sin el no muestra ninguno. `code` y `errorType` son opcionales porque no todo rechazo los
trae, y `code` SHALL ser el mismo valor que viaja en la respuesta JSON-RPC de esa peticion, de modo
que el evento y la respuesta no puedan indicar motivos distintos.

#### Scenario: Una metatx aceptada y enviada

- **WHEN** el sistema recibe una metatx valida y la envia al hub
- **THEN** publica `relay.received`, `relay.decoded` y `relay.sent`, en ese orden
- **AND** los tres comparten el mismo `metaTxId`
- **AND** cada uno incluye los campos propios definidos para su nombre

#### Scenario: Una metatx rechazada

- **WHEN** el sistema rechaza una metatx sin enviarla al hub
- **THEN** publica `relay.rejected` con el campo `error` describiendo el motivo
- **AND** incluye `code` y `errorType` si el rechazo los trae
- **AND** el `code` publicado coincide con el de la respuesta JSON-RPC de esa peticion
- **AND** ese evento comparte el `metaTxId` de los eventos previos de esa metatx

#### Scenario: El hub rechaza una metatx ya enviada

- **WHEN** el receipt de una metatx enviada indica que el hub la rechazo
- **THEN** publica `relay.hub_rejected` con `errorCode` y su `errorCodeName`
- **AND** ese evento comparte el `metaTxId` de los eventos previos de esa metatx

#### Scenario: Un campo sin valor aplicable

- **WHEN** el sistema emite un evento cuyo campo definido no tiene valor aplicable en este
  servicio, como `simulated` mientras no exista pre-chequeo por simulacion
- **THEN** el campo se emite igualmente con un valor que la vista pueda interpretar
- **AND** no se omite del evento

### Requirement: Eventos que no pertenecen a ninguna metatx

El sistema PUEDE registrar eventos de operacion que no se refieren a ninguna metatx. Esos eventos
MUST NOT llevar `metaTxId`, porque no hay ninguna a la que asociarlos, y SHALL llevar el `reqId` de
la peticion que los origino cuando exista. La vista en vivo los ignora al reconstruir el estado de
las metatx, pero siguen siendo visibles en el log y en el bus.

El unico de esta capacidad es `http.bad_body`, con el mismo nombre que usa el relayer en Node: una
peticion cuyo cuerpo no se puede leer o no se puede interpretar como JSON-RPC. Se registra con los
campos de error definidos para `relay.rejected`.

Existe porque el `reqId` se genera antes de leer el cuerpo: una peticion que nunca llega a tener
una metatx tambien tiene que dejar rastro, en lugar de desaparecer.

#### Scenario: Una peticion con el cuerpo ilegible

- **WHEN** el sistema recibe una peticion cuyo cuerpo no se puede leer o no es JSON-RPC valido
- **THEN** registra `http.bad_body` con el `reqId` de esa peticion
- **AND** el evento no lleva `metaTxId`
- **AND** la respuesta al cliente es la misma que antes de esta capacidad

### Requirement: Alcance de emision de esta capacidad

El sistema SHALL emitir `relay.received`, `relay.decoded`, `relay.sent`, `relay.rejected` y
`relay.hub_rejected` en el camino de relay existente. `relay.hub_rejected` se emite donde el
servicio ya detecta el rechazo del hub al procesar un receipt.

Los eventos `relay.held`, `relay.turn`, `relay.settled` y `relay.settle_failed` quedan definidos
por este contrato pero su emision corresponde a la capacidad de reordenamiento de nonces y a su
watcher de receipts, que aun no existen.

#### Scenario: Rafaga por el camino actual

- **WHEN** varios clientes envian metatx por el camino JSON-RPC existente
- **THEN** el bus contiene, para cada metatx, su `relay.received`, su `relay.decoded` y luego su
  `relay.sent` o su `relay.rejected`
- **AND** no contiene `relay.held` ni `relay.turn`

#### Scenario: El cierre se observa en otra peticion

- **WHEN** el receipt de una metatx se procesa en una peticion HTTP distinta de la que la relayo
- **THEN** el evento resultante lleva el `metaTxId` de esa metatx
- **AND** lleva el `reqId` de la peticion que la relayo, no el de la que consulto el receipt
- **AND** la vista en vivo lo asocia a la misma metatx que sus eventos previos
