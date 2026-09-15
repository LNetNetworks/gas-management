# relay-http-routing Specification

## Purpose
Repartir por path las peticiones que recibe el RelaySigner, de modo que puedan convivir el unico
camino que existe hoy —el catch-all JSON-RPC de `POST /`— y las rutas REST nuevas, sin que un
cliente que hoy apunta el servicio como si fuera el RPC del nodo note ninguna diferencia.

## Requirements

### Requirement: `POST /` conserva su comportamiento actual

La ruta `POST /` MUST seguir atendiendo todo el trafico JSON-RPC exactamente como antes de esta
capacidad: los mismos metodos, las mismas respuestas, los mismos codigos de error y el mismo
passthrough de las transacciones privadas. Un cliente que hoy apunta el RelaySigner como si fuera
el RPC del nodo MUST seguir funcionando sin cambiar nada.

Esta ruta sigue siendo el destino de cualquier path que no tenga un manejador propio.

#### Scenario: Un metodo JSON-RPC conocido

- **WHEN** un cliente envia un metodo JSON-RPC por `POST /`
- **THEN** la respuesta es identica a la que producia el servicio antes de esta capacidad
- **AND** el codigo de estado HTTP tambien es el mismo

#### Scenario: Un path sin manejador propio

- **WHEN** llega una peticion a un path que no corresponde a ninguna ruta definida
- **THEN** la atiende el camino JSON-RPC, como antes de esta capacidad

### Requirement: Rutas del servicio

El sistema SHALL atender, ademas del catch-all, las rutas `GET /info`, `GET /nonce/{address}` y
`POST /relay`, cada una con el comportamiento que definen sus capacidades.

Una ruta definida que se invoca con un metodo HTTP que no le corresponde SHALL responder `405`, y
no caer al camino JSON-RPC: un `GET /relay` es un error del cliente, no una peticion JSON-RPC.

#### Scenario: Una ruta con el metodo equivocado

- **WHEN** un cliente pide `GET /relay` o `POST /info`
- **THEN** el servicio responde `405`
- **AND** no procesa la peticion como JSON-RPC

#### Scenario: Una direccion mal formada en la ruta de nonce

- **WHEN** un cliente pide `GET /nonce/` sin direccion, o con algo que no es una direccion
- **THEN** el servicio responde `400` con el motivo
- **AND** no cae al camino JSON-RPC

### Requirement: Las rutas nuevas no requieren autenticacion

Las rutas nuevas MUST NOT exigir autenticacion, igual que el resto del servicio. Esto es
deliberado y acota donde puede exponerse el servicio: `GET /info` revela direcciones y el balance
del nodo, y `POST /relay` consume su cupo de gas igual que el camino JSON-RPC.

#### Scenario: Peticion sin credenciales

- **WHEN** un cliente invoca cualquiera de las rutas nuevas sin credenciales
- **THEN** el servicio la atiende normalmente

### Requirement: Un fallo en una ruta nueva no afecta al camino JSON-RPC

Un error al atender `GET /info`, `GET /nonce/{address}` o `POST /relay` MUST NOT interrumpir el
proceso ni degradar la atencion de `POST /`. Vale aca la invariante del servicio: ningun fallo
externo —de red, de transporte o de decodificacion— tumba el proceso.

#### Scenario: El nodo no responde mientras se atiende una ruta nueva

- **WHEN** el nodo esta caido y un cliente pide `GET /info`
- **THEN** el servicio responde un error al cliente
- **AND** sigue atendiendo `POST /` con normalidad
- **AND** el proceso sigue en ejecucion
