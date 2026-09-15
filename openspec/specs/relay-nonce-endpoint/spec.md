# relay-nonce-endpoint Specification

## Purpose
Responder, en una sola llamada y sin hablar JSON-RPC, las dos preguntas que necesita quien va a
firmar una metatx: que nonce tiene ese usuario en el RelayHub, y con cual tiene que firmar ahora si
ya mando otras que siguen sin minarse.

## Requirements

### Requirement: Los dos nonces de un usuario

`GET /nonce/{address}` SHALL informar, para la direccion pedida, el nonce que tiene en el RelayHub
y el que hay que usar para firmar la proxima metatx. Los dos SHALL informarse en decimal y en
hexadecimal, porque quien firma los necesita en una forma y quien depura en la otra.

Los dos valores son distintos y la diferencia es el punto del endpoint: el primero es lo que dice
la cadena, el segundo cuenta ademas las metatx que este servicio ya relayo y todavia no se minaron.
Encadenar metatx sin el segundo produce nonces repetidos.

#### Scenario: Consulta de una direccion

- **WHEN** un cliente pide el nonce de una direccion
- **THEN** la respuesta incluye la direccion consultada, el nonce en la cadena y el proximo a usar
- **AND** cada uno de los dos nonces viene en decimal y en hexadecimal

#### Scenario: Un usuario con metatx en vuelo

- **WHEN** un usuario tiene metatx ya relayadas y todavia sin minar
- **THEN** el proximo nonce a usar es mayor que el que informa la cadena

#### Scenario: Un usuario sin actividad previa

- **WHEN** se consulta una direccion que nunca relayo por este servicio
- **THEN** los dos nonces coinciden

### Requirement: Cuantas metatx hay en vuelo

`GET /nonce/{address}` SHALL informar cuantas metatx de esa direccion estan relayadas y todavia sin
resolverse. Es lo que permite a un cliente decidir si sigue encadenando o espera.

#### Scenario: Consulta del trabajo en vuelo

- **WHEN** un cliente pide el nonce de una direccion
- **THEN** la respuesta indica cuantas metatx de esa direccion estan en vuelo

### Requirement: Consultar sin afectar a otros clientes

`GET /nonce/{address}` SHALL aceptar un parametro que pida consultar **sin reservar** el proximo
nonce. Existe para inspeccionar el estado sin meterse en la cola de un cliente que esta relayando.

Mientras este servicio no reserve nonces, la respuesta con y sin ese parametro MUST ser la misma:
el parametro se acepta desde ahora para que un cliente escrito contra el relayer de referencia
funcione sin cambios, y cobra efecto cuando exista la reserva.

#### Scenario: Consulta con el parametro

- **WHEN** un cliente pide el nonce con el parametro de consulta sin reserva
- **THEN** la respuesta tiene la misma forma que sin el
- **AND** el estado del servicio no cambia por haberlo consultado

#### Scenario: Un parametro con un valor cualquiera

- **WHEN** el parametro llega con un valor que no se reconoce
- **THEN** el servicio responde normalmente, tratandolo como no pedido

### Requirement: Una direccion invalida se rechaza con su motivo

Cuando la direccion de la ruta no es una direccion valida, `GET /nonce/{address}` SHALL responder
`400` indicando el motivo, y MUST NOT consultar la cadena.

La direccion SHALL tratarse sin distinguir mayusculas de minusculas, de modo que la misma direccion
escrita de dos formas devuelva la misma respuesta. Una misma direccion en dos formas no puede
producir dos estados distintos.

#### Scenario: Direccion mal formada

- **WHEN** un cliente pide el nonce de algo que no es una direccion
- **THEN** el servicio responde `400` con el motivo
- **AND** no consulta la cadena

#### Scenario: La misma direccion escrita distinto

- **WHEN** se consulta la misma direccion en minusculas y con mayusculas de checksum
- **THEN** las dos respuestas informan los mismos nonces

### Requirement: El nonce informado es el mismo que responde el camino JSON-RPC

El proximo nonce que informa esta ruta MUST coincidir con el que responde `eth_getTransactionCount`
en estado pendiente por el camino JSON-RPC, para la misma direccion y en el mismo momento.

Son dos puertas a la misma verdad: si difieren, un cliente que use una y otra firma con nonces
incompatibles.

#### Scenario: Las dos puertas coinciden

- **WHEN** se consulta el nonce de una direccion por esta ruta y por el camino JSON-RPC
- **THEN** el proximo nonce informado es el mismo
