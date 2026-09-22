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
retenidas. Al alcanzarse el tope, SHALL rechazar **la de nonce mas alto entre las candidatas** -la
que llega y las que su usuario tiene retenidas-, indicando que se supero el limite, con cuantas hay
y cual es el maximo. El tope SHALL comprobarse tambien mientras una metatx espera su turno: ahi es
donde el cupo se hace cumplir, porque el cupo se puede llenar durante la espera y porque la
comprobacion de entrada no serializa a las peticiones de un mismo usuario.

El tope acota la ADMISION, no la salida: a una metatx ya admitida y a la que le llega su turno no
se le vuelve a comprobar, porque bloquearla seria descartar del medio de la cadena. Por eso el tope
no es una exactitud instante a instante -una rafaga simultanea lo pasa por un momento- ni fija
cuantas metatx sobreviven a una rafaga que se paso. Lo que SHALL ser determinista es **a quien se
descarta**, y ninguna de las descartadas gasta una transaccion del writer node.

Rechazar por nonce y no por orden de llegada es lo que preserva la cadena. Las metatx de un usuario
no son intercambiables: llevan nonces consecutivos que el hub exige exactos, asi que descartar una
del medio deja a todas las posteriores esperando un nonce que ya nunca va a avanzar. Descartar la
mas alta no invalida ninguna otra. Decidir por orden de llegada hace que el resultado de una misma
rafaga dependa del azar de la red.

Existe para acotar el dano cuando la cadena de nonces se rompe: al rechazarse la metatx `k`, las
`k+1` en adelante ya salieron y cada una gasta una transaccion del writer node.

#### Scenario: Se supera el tope

- **WHEN** un usuario en su tope recibe una metatx cuyo nonce es mayor que el de todas sus retenidas
- **THEN** se rechaza la que llega, indicando el motivo, cuantas hay en vuelo y el maximo
- **AND** las que ya estaban en vuelo o retenidas siguen su curso

#### Scenario: Se supera el tope y la que llega destraba la cola

- **WHEN** un usuario en su tope recibe una metatx cuyo nonce es menor que el de alguna retenida
- **THEN** se desaloja la retenida de nonce mas alto, con el mismo motivo de tope superado que
  recibiria la que llega
- **AND** la que llega pasa a esperar su turno normalmente
- **AND** si una rafaga simultanea pasa el tope por un momento, las retenidas que sobran se rechazan
  por tope superado -las de nonce mas alto primero- y no por nonce equivocado

#### Scenario: Una rafaga que se pasa del cupo conserva su cadena

- **WHEN** un usuario envia mas metatx de las que permite el cupo, en cualquier orden de llegada
- **THEN** las que se descartan son siempre las de nonce mas alto, nunca una del medio de la cadena
- **AND** las que se envian forman una cadena de nonces contigua, sin huecos, asi que ninguna queda
  huerfana esperando un nonce que ya no va a llegar
- **AND** ninguna se descarta por nonce equivocado

#### Scenario: El cupo se llena durante la espera

- **WHEN** una metatx esta retenida y el cupo de su usuario se llena mientras espera
- **THEN** se rechaza indicando que se supero el limite, y no por nonce equivocado
- **AND** la rechazada es la de nonce mas alto entre las retenidas

#### Scenario: El tope es por usuario

- **WHEN** un usuario esta en su tope de metatx en vuelo
- **THEN** las metatx de otros usuarios se atienden con normalidad
- **AND** un desalojo por cupo nunca alcanza a la metatx de otro usuario

#### Scenario: El desalojo no inventa un error nuevo

- **WHEN** una metatx es desalojada por cupo
- **THEN** su cliente recibe el mismo rechazo por tope superado que ya existia
- **AND** no se gasta una transaccion del writer node por ella

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
