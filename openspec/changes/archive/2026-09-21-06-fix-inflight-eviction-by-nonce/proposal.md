## Why

El cupo de `maxInflightPerUser` decide a quien descartar **sin mirar el nonce**: el chequeo corre
antes de `awaitTurn` (`service/reorder.go:129`), que es la unica pieza que sabe de nonces, asi que
el criterio real es el orden de llegada HTTP. Como las metatx de un usuario forman una cadena de
nonces estrictamente secuencial, descartar la del medio invalida todas las posteriores: quedan
esperando un `expectedNonce` que ya nunca va a avanzar, vencen la ventana y el hub las rechaza una
por una.

Medido en el nodo de pruebas `34.69.184.205` (2026-09-18), rafagas de 8 metatx de un usuario, 6
corridas por valor, warm-up descartado:

| `maxInflightPerUser` | Minadas por corrida | Media |
|---|---|---|
| 16 (default) | 5, 5, 5, 5, 5, 5 | 5,0 |
| 5 (al ras del techo de la red) | 4, 3, 3, 1, 2, 3 | 2,7 |

Con cupo 16 y rafagas de 8 el tope **no se alcanza nunca**, asi que ese 5,0 no lo produce esta
politica: lo impone el techo del txpool del writer node. Dicho sin vueltas, el cambio **no mejora
el caso por defecto**; lo que arregla es la fila de abajo, que bajar el cupo deje de ser un castigo.

La variabilidad **es** el defecto: mismo input, mismo servicio, misma red, y el resultado cambia
entre corridas segun en que orden llegaron las peticiones. En la corrida de 1/8 se descarto la
segunda metatx de la cadena, y las cuatro siguientes murieron con
`invalid nonce: the RelayHub expects 223`.

Hay una ironia de fondo: el cupo existe, segun su propio comentario, para "acotar el dano cuando la
cadena de nonces se rompe". Al elegir mal a quien descartar, es el quien la rompe.

> **Encuadre — esto NO es una brecha contra `node_relayer`.** El relayer de Node tiene el mismo
> comportamiento: `src/relayer.ts:839-840` aplica el techo "en la puerta, antes de que la metatx se
> ponga a esperar turno", con el mismo `inflight >= max` sobre la que llega. Go esta en paridad y el
> defecto es compartido; se heredo al portar. Se propone igual porque la paridad es el objetivo del
> repo, no su techo: un comportamiento no determinista que rompe cadenas de nonces no mejora por
> estar replicado en los dos relayers. Si se aprueba, conviene reportarlo tambien contra Node para
> que la paridad no se rompa en la direccion contraria.

## What Changes

- Cuando el cupo de un usuario esta lleno y llega una metatx cuyo nonce es **menor** que el de
  alguna retenida, se desaloja la retenida de nonce mas alto y se admite la que llega. La de nonce
  bajo es la que puede destrabar la cola; la alta no sirve de nada sin ella.
- Cuando la que llega tiene el nonce mas alto de todas, se la sigue rechazando como hoy: es
  efectivamente la que sobra.
- El desalojo reutiliza el motivo `too_many_inflight` que ya existe (`service/reorder_turn.go:34`),
  asi que la metatx desalojada termina con el mismo rechazo `TOO_MANY_INFLIGHT` que hoy recibe la
  que llega tarde. **No se introduce ningun codigo de error nuevo.**
- Lo que pasa a ser determinista es **a quien se descarta**: siempre la de nonce mas alto entre las
  candidatas, nunca una del medio de la cadena. De ahi se sigue lo que importa, que la cadena que
  sale sea **contigua**: sin huecos, sin metatx huerfanas esperando un nonce que ya no va a llegar.
- El **numero** de sobrevivientes de una rafaga que se paso del cupo NO queda fijado, y no es un
  descuido: el cupo acota la admision, no la salida. A una metatx ya admitida y en turno no se le
  vuelve a comprobar el cupo -bloquearla seria romper la cadena que este cambio protege-, asi que
  al destrabarse la cola cada retenida corre una carrera entre recibir su turno y ser el excedente.
  Con cupo 3 y una rafaga de 5 pueden salir 3 o 4. Ver design.md, D7.
- El cupo tampoco se vuelve estricto instante a instante: la puerta no serializa a las peticiones de
  un mismo usuario -hoy tampoco lo hace-, asi que una rafaga simultanea lo pasa por un momento y la
  comprobacion de la espera descarta de a una a las de nonce mas alto. Ninguna de las que sobran
  gasta una transaccion del writer node. Ver design.md, D3 y D6.

No es **BREAKING**: el conjunto de codigos de error no cambia, el contrato de `POST /` no cambia, y
con el reordenamiento apagado no se entra a este camino. Lo que cambia es **cual** de las metatx de
una rafaga que se paso del cupo recibe el rechazo — que hoy es azar.

## Capabilities

### New Capabilities

Ninguna.

### Modified Capabilities

- `metatx-reordering`: el requirement "Cuantas metatx puede tener un usuario en vuelo" hoy dice que
  al alcanzarse el tope "SHALL rechazar la que llega". Pasa a exigir que se rechace **la de nonce
  mas alto entre las candidatas** (la que llega y las retenidas), que es lo que preserva la cadena.

## Impact

- `service/reorder.go` — el chequeo de cupo previo a `awaitTurn` deja de ser un rechazo incondicional
  y pasa a comparar nonces.
- `service/reorder_turn.go` — necesita poder desalojar a una retenida concreta; hoy solo sabe
  despertar a todas las de un usuario para que reevaluen.
- `service/tracker.go` — `inflightOf` devuelve un conteo; hace falta ademas conocer el nonce mas alto
  retenido de un usuario.
- Sin cambios en `config.toml`: `maxInflightPerUser` conserva nombre, default (16) y significado.
- Sin cambios en la superficie HTTP ni en `GET /info`.
- Efecto secundario deseado: `maxInflightPerUser` vuelve a ser configurable a la baja. Hoy bajarlo
  castiga el rendimiento (2,7 contra 5,0 minadas), cuando deberia ser la herramienta para repartir el
  cupo de la red entre usuarios.
