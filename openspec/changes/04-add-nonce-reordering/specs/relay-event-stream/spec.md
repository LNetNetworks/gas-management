## MODIFIED Requirements

### Requirement: Alcance de emision de esta capacidad

El sistema SHALL emitir `relay.received`, `relay.decoded`, `relay.sent`, `relay.rejected` y
`relay.hub_rejected` en el camino de relay. `relay.hub_rejected` se emite donde el servicio detecta
el rechazo del hub al procesar un receipt.

`relay.settled` SHALL emitirse tanto donde el servicio ya resuelve un receipt -la consulta del
receipt y la del resultado de la metatx- como por el watcher de `metatx-receipt-watching`, que lo
detecta sin que el cliente pregunte. Una misma metatx MUST registrar su cierre una sola vez, sin
importar cual de los dos caminos llego primero.

`relay.settle_failed` SHALL emitirse cuando no se pueda determinar como termino una metatx, incluido
el caso en que su resultado no llega dentro del plazo.

`relay.held` y `relay.turn` SHALL emitirse cuando el reordenamiento retiene una metatx y cuando
termina su espera, segun define `metatx-reordering`. Con el reordenamiento deshabilitado no se emite
ninguno de los dos.

#### Scenario: Rafaga por el camino actual

- **WHEN** varios clientes envian metatx por el camino JSON-RPC y el reordenamiento esta apagado
- **THEN** el bus contiene, para cada metatx, su `relay.received`, su `relay.decoded` y luego su
  `relay.sent` o su `relay.rejected`
- **AND** no contiene `relay.held` ni `relay.turn`

#### Scenario: Una metatx retenida por el reordenamiento

- **WHEN** el reordenamiento retiene una metatx y despues le llega el turno
- **THEN** el bus contiene su `relay.held` y su `relay.turn`, con el mismo `metaTxId` que sus demas
  eventos

#### Scenario: El cierre se observa sin que el cliente consulte

- **WHEN** una metatx se resuelve en la cadena y el cliente nunca consulta su receipt
- **THEN** el bus contiene su `relay.settled`, con el `metaTxId` de esa metatx
- **AND** ese cierre no se repite si el cliente consulta el receipt despues

#### Scenario: El cierre se observa en otra peticion

- **WHEN** el receipt de una metatx se procesa en una peticion HTTP distinta de la que la relayo
- **THEN** el evento resultante lleva el `metaTxId` de esa metatx
- **AND** lleva el `reqId` de la peticion que la relayo, no el de la que consulto el receipt
- **AND** la vista en vivo lo asocia a la misma metatx que sus eventos previos
