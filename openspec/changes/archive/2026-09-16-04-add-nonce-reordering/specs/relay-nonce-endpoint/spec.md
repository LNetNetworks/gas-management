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

Lo que se entrega es un TURNO y no una reserva: consultar MUST NOT adelantar por si solo el proximo
nonce. Lo que hace que dos clientes se lleven numeros distintos es que la metatx del primero llegue
-el caso normal, en el orden de milisegundos-, no el acto de consultar.

Por eso un numero entregado y no usado MUST NOT bloquear la cola: al vencer su plazo, el siguiente
que pregunte SHALL llevarse ese mismo numero. Si consultar adelantara el numero, un cliente que
pregunta y no envia dejaria al siguiente firmando un nonce que el hub no va a aceptar todavia, y su
metatx quedaria retenida hasta rechazarse.

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

- **WHEN** dos clientes consultan el nonce del mismo usuario a la vez, el reparto esta encendido y la
  metatx del primero llega mientras el segundo espera
- **THEN** el segundo recibe el nonce siguiente al que se llevo el primero
- **AND** ninguno de los dos recibe un numero que el otro ya tenia

#### Scenario: Un nonce entregado y nunca usado

- **WHEN** se entrega un nonce y no llega la metatx que lo use dentro de su plazo
- **THEN** el siguiente cliente que consulte recibe ese mismo nonce
- **AND** ningun cliente queda esperando de forma indefinida

### Requirement: El nonce informado es el mismo que responde el camino JSON-RPC

El proximo nonce que informa esta ruta y el que responde `eth_getTransactionCount` en estado
pendiente SHALL salir del MISMO estado: las dos puertas son dos formas de preguntar lo mismo, y si
cada una llevara su cuenta, un cliente que use una y otra firmaria con nonces incompatibles.

Mientras el reparto de nonces este apagado -el estado por defecto-, para la misma direccion y en el
mismo momento las dos SHALL informar el MISMO valor.

Con el reparto encendido las dos SHALL entregar de la misma secuencia y ningun numero SHALL
entregarse dos veces, por ninguna de las dos. Ahi la invariante no puede ser que dos llamadas
devuelvan lo mismo -el punto del reparto es justamente que no lo hagan-, sino que ninguna entregue
un numero que otra ya entrego.

#### Scenario: Las dos puertas coinciden

- **WHEN** se consulta el nonce de una direccion por esta ruta y por el camino JSON-RPC, con el
  reparto apagado
- **THEN** el proximo nonce informado es el mismo

#### Scenario: Las dos puertas reparten de la misma secuencia

- **WHEN** se consulta el nonce de la misma direccion por las dos puertas con el reparto encendido
- **THEN** cada consulta recibe un numero distinto
- **AND** ninguno de los dos numeros fue entregado antes por ninguna de las dos puertas
