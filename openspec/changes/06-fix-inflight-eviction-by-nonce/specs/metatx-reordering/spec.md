## MODIFIED Requirements

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
