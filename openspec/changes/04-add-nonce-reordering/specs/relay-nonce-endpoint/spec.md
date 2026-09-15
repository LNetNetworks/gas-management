## MODIFIED Requirements

### Requirement: Cuantas metatx hay en vuelo

`GET /nonce/{address}` SHALL informar cuantas metatx de esa direccion estan relayadas y todavia sin
resolverse. Es lo que permite a un cliente decidir si sigue encadenando o espera.

Ese numero SHALL ser el que lleva el tracker de `metatx-nonce-tracking`: las metatx enviadas y sin
resultado, mas las retenidas esperando su turno. Deja de ser una diferencia calculada contra la
cadena y pasa a ser lo que el servicio realmente tiene en vuelo, que es contra lo que valida al
enviar.

#### Scenario: Consulta del trabajo en vuelo

- **WHEN** un cliente pide el nonce de una direccion
- **THEN** la respuesta indica cuantas metatx de esa direccion estan en vuelo

#### Scenario: El numero informado es el que se usa para validar

- **WHEN** un usuario tiene metatx en vuelo y retenidas
- **THEN** la cantidad informada las cuenta a todas
- **AND** el proximo nonce informado es el que el servicio va a exigirle a la proxima metatx

#### Scenario: Una direccion en su tope de metatx en vuelo

- **WHEN** un usuario alcanzo el tope de metatx en vuelo
- **THEN** la cantidad informada permite al cliente ver que llego al limite

### Requirement: Consultar sin afectar a otros clientes

`GET /nonce/{address}` SHALL aceptar un parametro que pida consultar **sin reservar** el proximo
nonce. Existe para inspeccionar el estado sin meterse en la cola de un cliente que esta relayando.

El reparto de nonces SHALL estar gobernado por su propia clave de configuracion, apagada por
defecto. Mientras este apagada, la respuesta con y sin ese parametro MUST ser la misma y consultar
MUST NOT cambiar el estado del servicio, que es el comportamiento de hoy.

Con el reparto encendido, las consultas de un mismo usuario SHALL serializarse y cada una SHALL
llevarse un nonce distinto, de modo que dos clientes que preguntan a la vez no firmen el mismo
numero. Una consulta con el parametro MUST seguir sin reservar: informa lo que hay sin tomar
posicion en la cola.

Un numero entregado y no usado MUST NOT bloquear la cola: al vencer su plazo, el siguiente que
pregunte SHALL llevarse ese mismo numero. Una reserva que solo su duenio pudiera destapar trabaria
detras a todos los demas clientes.

#### Scenario: Consulta con el parametro

- **WHEN** un cliente pide el nonce con el parametro de consulta sin reserva
- **THEN** la respuesta tiene la misma forma que sin el
- **AND** el estado del servicio no cambia por haberlo consultado

#### Scenario: Un parametro con un valor cualquiera

- **WHEN** el parametro llega con un valor que no se reconoce
- **THEN** el servicio responde normalmente, tratandolo como no pedido

#### Scenario: Dos clientes preguntan a la vez con el reparto apagado

- **WHEN** dos clientes consultan el nonce del mismo usuario a la vez y el reparto esta apagado
- **THEN** los dos reciben el mismo proximo nonce, como antes de esta capacidad

#### Scenario: Dos clientes preguntan a la vez con el reparto encendido

- **WHEN** dos clientes consultan el nonce del mismo usuario a la vez y el reparto esta encendido
- **THEN** cada uno recibe un nonce distinto y consecutivo

#### Scenario: Un nonce entregado y nunca usado

- **WHEN** se entrega un nonce y no llega la metatx que lo use dentro de su plazo
- **THEN** el siguiente cliente que consulte recibe ese mismo nonce
- **AND** ningun cliente queda esperando de forma indefinida
