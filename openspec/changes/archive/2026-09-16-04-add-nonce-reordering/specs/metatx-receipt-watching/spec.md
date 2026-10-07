## Purpose

Enterarse de como termino cada metatx enviada sin depender de que el cliente pregunte. Es lo que
libera lo que el usuario tiene en vuelo -y por lo tanto lo que destraba la cola de su rafaga- y lo
que permite informar el cierre de una metatx relayada por el camino JSON-RPC, donde hoy nadie vuelve
a mirarla si el cliente no consulta el receipt.

## ADDED Requirements

### Requirement: El cierre de una metatx se detecta sin que el cliente lo pida

Para cada metatx enviada, el sistema SHALL detectar por si mismo cuando se resolvio en la cadena y
SHALL registrar como termino, con el evento y los campos que define `relay-event-stream`.

Hoy eso solo ocurre si el cliente consulta el receipt o el resultado de la metatx: una relayada por
el camino JSON-RPC cuyo cliente nunca vuelve a preguntar no deja rastro de como termino.

#### Scenario: Una metatx relayada y nunca consultada

- **WHEN** se relaya una metatx por el camino JSON-RPC y el cliente no consulta su receipt
- **THEN** el sistema detecta igual como termino
- **AND** registra el cierre con el identificador de esa metatx

#### Scenario: Una metatx que el hub rechaza

- **WHEN** el resultado de una metatx indica que el hub la rechazo
- **THEN** se registra el rechazo con su codigo de error y el nombre del codigo
- **AND** se descarta lo que el sistema sabe de los nonces de ese usuario

### Requirement: El cierre de una metatx se informa una sola vez

Una metatx SHALL registrar su cierre una sola vez, sin importar por cual camino se detecto: el
watcher, la consulta del receipt o la del resultado de la metatx. MUST NOT emitirse dos veces el
cierre de la misma metatx.

La vista en vivo reconstruye el estado por metatx; dos cierres para la misma la harian contar dos
veces.

#### Scenario: El cliente consulta el receipt de una metatx ya cerrada por el watcher

- **WHEN** el watcher ya registro el cierre de una metatx y el cliente consulta su receipt
- **THEN** la respuesta al cliente es la misma que antes de esta capacidad
- **AND** no se registra un segundo cierre para esa metatx

### Requirement: El cierre libera lo que el usuario tiene en vuelo

Al detectarse el cierre de una metatx, el sistema SHALL descontarla de lo en vuelo de su usuario,
segun lo que define `metatx-nonce-tracking`, y SHALL dar oportunidad a las metatx retenidas de ese
usuario de reevaluar su turno.

#### Scenario: Se cierra una metatx con otras esperando

- **WHEN** se cierra una metatx de un usuario que tiene otras retenidas
- **THEN** baja la cantidad en vuelo de ese usuario
- **AND** las retenidas reevaluan su turno

### Requirement: Una metatx que nunca se resuelve no queda en vuelo para siempre

El sistema SHALL acotar cuanto espera el resultado de una metatx enviada. Al vencer ese plazo, MUST
registrar que no se pudo determinar como termino, con el evento que define `relay-event-stream`, y
MUST liberar lo que esa metatx retiene del usuario.

Sin esto, una metatx perdida deja al usuario con una posicion en vuelo que nunca se libera, y su
cupo se va consumiendo hasta que no puede relayar mas.

#### Scenario: Una metatx sin resultado

- **WHEN** una metatx enviada no se resuelve dentro del plazo
- **THEN** se registra que no se pudo determinar como termino
- **AND** se libera lo que esa metatx retenia del usuario

### Requirement: El watcher no tumba el proceso ni el camino de la metatx

Un fallo al observar la cadena -de red, de transporte o de decodificacion- MUST NOT interrumpir el
proceso ni degradar la atencion de las peticiones. Si la observacion se corta, SHALL reanudarse sola
cuando el nodo vuelva, sin intervencion.

Vale la invariante del servicio: ningun fallo externo tumba el proceso. Es la misma propiedad que ya
sostiene el seguimiento de bloques del que este watcher se cuelga.

#### Scenario: El nodo se cae mientras hay metatx en vuelo

- **WHEN** el nodo deja de responder mientras hay metatx esperando su resultado
- **THEN** el servicio sigue atendiendo peticiones
- **AND** al volver el nodo, la observacion se reanuda sin intervencion
- **AND** el proceso sigue en ejecucion

#### Scenario: Un resultado que no se puede interpretar

- **WHEN** el resultado de una metatx no se puede interpretar
- **THEN** se registra que no se pudo determinar como termino
- **AND** el proceso sigue en ejecucion

### Requirement: Con el reordenamiento apagado el cierre se detecta como hasta ahora

Mientras el reordenamiento este deshabilitado, el sistema MUST seguir registrando el cierre de una
metatx donde ya lo hace -al procesar el receipt o el resultado que el cliente consulta- y las
respuestas de esas consultas MUST ser identicas a las de antes de esta capacidad.

#### Scenario: Consulta del receipt con el flag apagado

- **WHEN** un cliente consulta el receipt de una metatx con el reordenamiento apagado
- **THEN** la respuesta es identica a la que producia el servicio antes de esta capacidad
