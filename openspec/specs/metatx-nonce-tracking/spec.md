# metatx-nonce-tracking Specification

## Purpose
Saber, para cada usuario, con que nonce del RelayHub tiene que firmar su proxima metatx contando
las que este servicio ya relayo y todavia no se minaron. Es lo que permite encadenar varias metatx
del mismo usuario sin esperar a que cada una se mine, y lo que permite rechazar una metatx con el
nonce equivocado antes de gastar una transaccion del writer node.

## Requirements

### Requirement: El proximo nonce lo determina lo que el servicio ya relayo

El sistema SHALL mantener, por cada par (nodo, usuario), el proximo nonce del RelayHub a usar y
cuantas metatx de ese usuario estan relayadas y sin resolverse. Mientras el usuario tenga metatx en
vuelo, ese valor -y no el que informa la cadena- SHALL ser el que el sistema informa como proximo
nonce y contra el que valida.

Puede ser autoritativo porque la casilla del hub que cuenta es la de este nodo, y ningun otro
proceso la toca. La condicion es que este servicio sea el unico que use la clave del writer node.

#### Scenario: Un usuario sin metatx en vuelo

- **WHEN** se pide el proximo nonce de un usuario que no tiene ninguna metatx en vuelo
- **THEN** el sistema lo lee de la cadena
- **AND** informa cero metatx en vuelo

#### Scenario: Un usuario con metatx en vuelo

- **WHEN** un usuario tiene metatx relayadas y todavia sin resolverse
- **THEN** el proximo nonce informado es el que sigue a la ultima relayada
- **AND** la cantidad en vuelo es la cantidad de metatx relayadas y sin resolverse

#### Scenario: Metatx encadenadas antes de que se mine la primera

- **WHEN** un usuario relaya varias metatx con nonces consecutivos sin esperar a que se minen
- **THEN** el sistema las acepta todas
- **AND** ninguna se rechaza por traer un nonce que la cadena todavia no refleja

### Requirement: El nonce se valida antes de gastar una transaccion del writer node

Antes de enviar, el sistema SHALL comparar el nonce que trae la metatx con el que espera para ese
usuario. Si no coinciden y no corresponde retenerla, MUST rechazarla sin enviarla, indicando el
nonce esperado y el recibido.

Hoy no se valida: la metatx se manda y el hub la rechaza on-chain con la transaccion del writer node
ya gastada, y el cliente se entera recien al consultar el receipt.

#### Scenario: Una metatx con un nonce ya consumido

- **WHEN** llega una metatx con un nonce menor al que el sistema espera para ese usuario
- **THEN** se rechaza sin enviarla
- **AND** el motivo indica el nonce esperado y el que traia
- **AND** no se gasta ninguna transaccion del writer node

#### Scenario: Una metatx con el nonce esperado

- **WHEN** llega una metatx cuyo nonce es el que el sistema espera
- **THEN** se envia al hub
- **AND** el proximo nonce esperado para ese usuario pasa a ser el siguiente

### Requirement: El orden de reserva se conserva

Las reservas de nonce de un mismo usuario SHALL asignarse de a una, de modo que la metatx con nonce
`n` se envie antes que la de nonce `n+1`. Reservas de usuarios distintos MUST NOT esperarse entre
si.

Es lo que hace que encadenar funcione: las transacciones de una cuenta se ejecutan en orden de
nonce, asi que si los nonces del hub se reservan en el mismo orden en que se toman los de la cuenta
del writer node, la metatx `n` se mina antes que la `n+1` aunque caigan en el mismo bloque.

#### Scenario: Dos metatx del mismo usuario a la vez

- **WHEN** dos metatx del mismo usuario con nonces consecutivos llegan al mismo tiempo
- **THEN** se envian en orden de nonce
- **AND** cada una reserva un nonce distinto

#### Scenario: Metatx de usuarios distintos a la vez

- **WHEN** llegan a la vez metatx de usuarios distintos
- **THEN** ninguna espera a que se resuelva la reserva de otro usuario

### Requirement: Lo en vuelo se libera al resolverse, con una gracia antes de olvidar

Cuando una metatx se resuelve, el sistema SHALL descontarla de lo en vuelo de su usuario. Cuando no
queda ninguna, MUST esperar una gracia antes de olvidar lo que sabe de ese usuario, en lugar de
olvidarlo en el acto.

Sin la gracia, en medio de una rafaga el proximo nonce informado caeria al ultimo minado apenas se
resuelve una metatx, y un cliente que resincroniza ahi firmaria un nonce que otra metatx de la misma
rafaga ya tomo.

#### Scenario: Se resuelve una de varias

- **WHEN** se resuelve una metatx de un usuario que tiene otras en vuelo
- **THEN** la cantidad en vuelo baja en uno
- **AND** el proximo nonce informado no cambia

#### Scenario: Se resuelve la ultima

- **WHEN** se resuelve la ultima metatx en vuelo de un usuario
- **THEN** durante la gracia el proximo nonce informado sigue siendo el reservado
- **AND** pasada la gracia sin actividad nueva, se vuelve a leer de la cadena

#### Scenario: Llega una metatx durante la gracia

- **WHEN** llega una metatx nueva del mismo usuario mientras corre la gracia
- **THEN** continua la cadena en curso en lugar de empezar una nueva
- **AND** la gracia se cancela

### Requirement: Una cadena rota se descarta entera

Cuando el envio de una metatx falla, o el hub rechaza una ya enviada sin consumir su nonce, el
sistema SHALL descartar todo lo que sabe de ese usuario, de modo que la proxima metatx se valide
contra la cadena. Un resultado que llega tarde y corresponde a una cadena ya descartada MUST NOT
alterar lo que se sabe del usuario en ese momento.

Reservar sobre un hueco no sirve: si el nonce `n` nunca se consumio, todo lo reservado despues esta
mal. Y si un receipt atrasado de la cadena vieja descontara sobre la nueva, el conteo de en vuelo
quedaria corrido para siempre.

#### Scenario: Falla el envio

- **WHEN** el envio de una metatx al hub falla
- **THEN** se descarta lo que el sistema sabe de ese usuario
- **AND** la proxima metatx de ese usuario se valida contra el nonce de la cadena

#### Scenario: El hub rechaza una metatx ya enviada

- **WHEN** el resultado de una metatx indica que el hub la rechazo sin consumir su nonce
- **THEN** se descarta lo que el sistema sabe de ese usuario

#### Scenario: Un resultado atrasado de una cadena descartada

- **WHEN** llega el resultado de una metatx cuya cadena ya se habia descartado
- **THEN** no altera la cantidad en vuelo ni el proximo nonce del usuario

### Requirement: Ningun usuario queda bloqueado de forma permanente

Ninguna secuencia de fallos, resultados atrasados o peticiones concurrentes SHALL dejar a un usuario
sin poder relayar. Ante cualquier duda sobre lo que el sistema cree saber, MUST poder volver a leer
el nonce de la cadena.

Es la invariante que ya sostiene la cache que este tracker reemplaza, y no se pierde al reemplazarla.

#### Scenario: Una rafaga que termina mal

- **WHEN** una rafaga de un usuario termina con envios fallidos y resultados que llegan tarde
- **THEN** el usuario puede volver a relayar leyendo el nonce de la cadena
- **AND** no hace falta reiniciar el servicio

### Requirement: Con el reordenamiento apagado el comportamiento no cambia

Mientras el reordenamiento este deshabilitado, lo que el sistema recuerde de los nonces MUST seguir
siendo una pista para el cliente y no una reserva: MUST NOT retener ninguna metatx y MUST NOT
rechazar ninguna por su nonce. Las respuestas de todas las puertas MUST ser identicas a las de antes
de esta capacidad.

#### Scenario: Una metatx con el nonce equivocado y el flag apagado

- **WHEN** llega una metatx con un nonce que el hub no va a aceptar y el reordenamiento esta apagado
- **THEN** se envia igual, como antes de esta capacidad
- **AND** la respuesta es la misma que producia el servicio antes

#### Scenario: Consulta del nonce con el flag apagado

- **WHEN** se consulta el nonce de un usuario con el reordenamiento apagado
- **THEN** la respuesta es la misma que producia el servicio antes de esta capacidad
