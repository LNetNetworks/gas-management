# metatx-reordering Specification

## Purpose
Tolerar que las metatx de un usuario lleguen desordenadas. Sobre HTTP el orden de llegada no esta
garantizado y el RelayHub exige el nonce exacto, asi que una metatx adelantada por la red muere
gastando una transaccion del writer node. Con el buffer, espera a que se cierre el hueco.

## Requirements

### Requirement: Una metatx adelantada espera su turno

Cuando el nonce de una metatx es mayor al que el sistema espera para ese usuario, SHALL retenerla
hasta que le llegue el turno, en lugar de enviarla o rechazarla. Al cerrarse el hueco, SHALL
enviarla como a cualquier otra.

Retener no es un caso de error: es la unica forma de no gastar una transaccion del writer node para
descubrir algo que el sistema ya sabe.

#### Scenario: Llegan dos metatx en orden invertido

- **WHEN** llegan dos metatx del mismo usuario y primero la de nonce mayor
- **THEN** la adelantada queda retenida
- **AND** al llegar y enviarse la que faltaba, la retenida se envia a continuacion
- **AND** las dos se envian, en orden de nonce

#### Scenario: Una rafaga que llega desordenada

- **WHEN** llega una rafaga de metatx de un usuario en un orden cualquiera
- **THEN** todas se envian
- **AND** el orden de envio es el de sus nonces, sin importar el de llegada

### Requirement: La ventana mide estancamiento, no espera total

La espera de una metatx retenida SHALL acotarse por una ventana que se renueva cada vez que el nonce
esperado para ese usuario avanza. La ventana MUST NOT medir el tiempo total de espera.

Si midiera el total, una rafaga larga perderia la cola por reloj estando todo sano: con doce metatx
a un segundo de envio cada una, las ultimas se caen de una ventana de tres segundos mientras el hub
va por la quinta.

#### Scenario: Una rafaga mas larga que la ventana

- **WHEN** una rafaga tarda en enviarse mas que la ventana, pero avanza sin detenerse
- **THEN** ninguna metatx retenida se descarta por vencimiento
- **AND** todas terminan enviandose

#### Scenario: La cadena deja de avanzar

- **WHEN** una metatx queda retenida y el nonce esperado no avanza durante toda la ventana
- **THEN** la espera termina por vencimiento

### Requirement: Al vencer la ventana se responde el error de siempre, sin haber gastado nada

Cuando una metatx retenida vence sin que le llegue el turno, el sistema SHALL rechazarla con el
mismo motivo con el que se rechaza un nonce equivocado, indicando el esperado y el recibido, y MUST
NOT haber enviado nada al hub por ella.

Retener no puede empeorar el resultado: lo peor que le pasa a una metatx retenida es exactamente lo
que le pasaba antes de existir el buffer, y sin gastar una transaccion del writer node.

#### Scenario: Una metatx que nunca recibe su turno

- **WHEN** una metatx retenida vence sin que se cierre el hueco
- **THEN** se rechaza indicando el nonce esperado y el que traia
- **AND** no se envio ninguna transaccion al hub por esa metatx

### Requirement: Cuantas metatx puede tener un usuario en vuelo

El sistema SHALL acotar la cantidad de metatx de un mismo usuario que estan a la vez en vuelo o
retenidas. Al alcanzarse el tope, SHALL rechazar la que llega indicando que se supero el limite, con
cuantas hay y cual es el maximo. El tope SHALL comprobarse tambien mientras una metatx espera su
turno, porque el cupo se puede llenar durante la espera.

Existe para acotar el dano cuando la cadena de nonces se rompe: al rechazarse la metatx `k`, las
`k+1` en adelante ya salieron y cada una gasta una transaccion del writer node.

#### Scenario: Se supera el tope

- **WHEN** un usuario supera la cantidad maxima de metatx en vuelo
- **THEN** la que llega se rechaza indicando el motivo, cuantas hay en vuelo y el maximo
- **AND** las que ya estaban en vuelo siguen su curso

#### Scenario: El cupo se llena durante la espera

- **WHEN** una metatx esta retenida y el cupo de su usuario se llena mientras espera
- **THEN** se rechaza indicando que se supero el limite, y no por nonce equivocado

#### Scenario: El tope es por usuario

- **WHEN** un usuario esta en su tope de metatx en vuelo
- **THEN** las metatx de otros usuarios se atienden con normalidad

### Requirement: La retencion se puede observar

El sistema SHALL emitir un evento al retener una metatx y otro al terminar su espera, con los
nombres y campos que define `relay-event-stream`. El segundo SHALL indicar cuanto espero y por que
termino la espera.

Sin estos dos eventos, una metatx retenida aparece como un envio tardio y sin explicacion: la
retencion es lo unico del reordenamiento que no se deduce de ningun otro evento.

#### Scenario: Una metatx retenida y despues enviada

- **WHEN** una metatx queda retenida y luego le llega el turno
- **THEN** se emite el evento de retencion con el nonce que trae, el esperado, la distancia entre
  ambos y la ventana
- **AND** al terminar la espera se emite el evento de turno con cuanto espero y el motivo
- **AND** los dos comparten el identificador de esa metatx

#### Scenario: Una metatx que no se retiene

- **WHEN** una metatx llega con el nonce esperado y se envia sin esperar
- **THEN** no se emite ningun evento de retencion ni de turno para esa metatx

### Requirement: Con el reordenamiento apagado no se retiene nada

Mientras el reordenamiento este deshabilitado, el sistema MUST NOT retener ninguna metatx, MUST NOT
aplicar el tope de en vuelo y MUST NOT emitir los eventos de retencion ni de turno. El
comportamiento observable MUST ser identico al de antes de esta capacidad.

#### Scenario: Una metatx adelantada con el flag apagado

- **WHEN** llega una metatx adelantada y el reordenamiento esta apagado
- **THEN** se envia de inmediato, como antes de esta capacidad
- **AND** el bus no contiene eventos de retencion ni de turno

### Requirement: Retener no ata el resto del servicio

Una metatx retenida MUST NOT impedir que se atiendan metatx de otros usuarios, ni las demas rutas
del servicio, ni el camino JSON-RPC. La cantidad de metatx retenidas a la vez MUST estar acotada por
el tope por usuario.

#### Scenario: El servicio mientras hay metatx retenidas

- **WHEN** hay metatx retenidas esperando su turno
- **THEN** el resto de las peticiones se atiende con normalidad
- **AND** el tiempo de respuesta de una metatx que no se retiene no depende de cuantas haya retenidas
