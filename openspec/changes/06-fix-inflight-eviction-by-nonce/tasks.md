## 1. Registro de retenidas con nonce

- [ ] 1.1 Reemplazar `heldCount map[string]int` (`service/tracker.go`) por un registro por usuario
  que asocie cada metatx retenida con su nonce y con la forma de desalojarla, manteniendo
  `waitingOf` con la misma semantica -cuenta lo retenido aunque entre dos vueltas de la espera no
  este dormido-. Verificar con `go test ./... -race` que los tests existentes de retencion y de cupo
  siguen en verde sin tocarlos
- [ ] 1.2 Cambiar `hold`/`unhold` para que registren y borren por metatx (nonce + canal) en vez de
  sumar y restar un contador, y verificar con un test que tras una rafaga retenida y resuelta el
  registro del usuario queda vacio -sin fugas por usuario, como ya cuida `stopWaitingLocked`-
- [ ] 1.3 Agregar la consulta del nonce mas alto retenido de un usuario, y verificar con tests de
  tabla los casos: sin retenidas, una sola, varias, y dos con el mismo nonce

## 2. Desalojo

- [ ] 2.1 Agregar la marca de desalojo en la entrada retenida y la operacion que, bajo
  `sendersLock`, elige la victima, la marca y la descuenta del registro, todo en la misma seccion
  critica (D2, D3). La operacion **no reserva** el lugar liberado para quien la provoco: por D3 el
  cupo se hace cumplir en la espera, no en la puerta. Verificar con un test que el descuento ocurre
  al marcar y no al despertar la goroutine
- [ ] 2.2 Hacer que `awaitTurn` distinga el despertar por desalojo del despertar por reevaluacion y
  devuelva `tooManyInflight` en el primer caso, emitiendo `relay.turn` con `reason`
  `too_many_inflight`. Verificar con un test que la desalojada recibe ese rechazo y no un BAD_NONCE
- [ ] 2.3 Verificar con un test `-race` que una goroutine desalojada no vuelve a descontar del
  registro al despertar (doble descuento), y que el cupo del usuario nunca baja de cero

## 3. Politica de descarte por nonce

- [ ] 3.1 Cambiar el chequeo de la puerta (`service/reorder.go:129`) para que, con el cupo lleno,
  compare el nonce de la que llega con el mas alto retenido: si es menor, desaloja y admite; si es
  mayor o igual, rechaza la que llega. Verificar con los escenarios "Se supera el tope" y "Se supera
  el tope y la que llega destraba la cola" del delta spec
- [ ] 3.2 Cambiar el chequeo de la espera (`service/reorder_turn.go:121`) para que cada retenida se
  pregunte si es **ella** la de nonce mas alto de su usuario y, si lo es, se rechace por tope (D6).
  Este es el punto que hace cumplir el cupo, no la puerta. Verificar con el escenario "El cupo se
  llena durante la espera" que la rechazada es la de nonce mas alto entre las retenidas
- [ ] 3.3 Cubrir los casos borde de D4 con tests: nonce igual al maximo retenido (rechaza la que
  llega), dos retenidas con el mismo nonce (desaloja la que llego despues), y cupo lleno solo de
  en-vuelo sin retenidas (rechaza la que llega)
- [ ] 3.4 Dejar un test que fije la asimetria deliberada entre el `>=` de la puerta y el `>` de la
  espera (D6), con un comentario que explique por que no son el mismo operador: la puerta pregunta
  "hay lugar para una mas" y puede dejar pasar de mas porque su predicado lo satisfacen varias
  goroutines a la vez; la espera pregunta "el cupo se paso" y lo satisface una sola, que es lo que
  la hace converger

## 4. Determinismo y no regresion

- [ ] 4.1 Test que lanza la misma rafaga de N metatx con cupo C en varios ordenes de llegada
  distintos y verifica que en todos sobreviven las mismas C -las de nonce mas bajo-, que es el
  escenario "Una rafaga que se pasa del cupo conserva su cadena"
- [ ] 4.2 Test de convergencia del cupo (D3, riesgo del desborde transitorio): forzar que varias
  metatx del mismo usuario pasen la puerta a la vez, medir el desborde maximo de retenidas por
  encima del tope y verificar que, al despertar los que esperan, el cupo vuelve al tope y las que
  sobran se rechazan con `TOO_MANY_INFLIGHT` -las de nonce mas alto primero-, nunca con BAD_NONCE
- [ ] 4.3 Test de la tormenta de desalojos del apartado Risks: un cliente que insiste con nonces
  bajos no debe producir un ciclo de desalojos mutuos; verificar que el sistema converge y que
  ninguna goroutine queda colgada
- [ ] 4.4 Verificar que el tope sigue siendo por usuario: un desalojo por cupo nunca alcanza la
  metatx de otro usuario, y otro usuario se atiende con normalidad mientras uno esta en su tope
- [ ] 4.5 Verificar que con `[reorder] enabled = false` no se entra a este camino y el
  comportamiento es byte a byte el de antes del cambio
- [ ] 4.6 Correr `go build ./...` y `go test ./... -race` y verificar que quedan en verde
  (invariante 3 del proyecto)

## 5. Documentacion

- [ ] 5.1 Actualizar el comentario de `tooManyInflight` (`service/reorder.go`) y el de
  `reorder.go:127-128` para que digan que la decision es por nonce y no por llegada, y verificar que
  no quedan comentarios afirmando lo contrario (`grep` por "la que llega")
- [ ] 5.2 Actualizar la entrada de `maxInflightPerUser` en `config.toml` y en el README: el tope
  sigue siendo el mismo, pero lo que se descarta al superarlo pasa a ser la de nonce mas alto.
  Verificar que el README no siga sugiriendo que bajarlo protege el cupo, que es lo que medimos
  falso
- [ ] 5.3 Registrar la divergencia con `node_relayer` en `node_relayer/COMPARACION-GO-NODE.md`: Go
  desaloja por nonce, Node rechaza al que llega (`src/relayer.ts:839-840`). Verificar que queda
  anotada como diferencia deliberada y no como brecha pendiente

## 6. Verificacion de cierre en el nodo de pruebas

- [ ] 6.1 Con `maxInflightPerUser = 5` en el nodo `34.69.184.205`, correr
  `node_relayer/examples/nonce-stress.ts --n 8` seis veces y verificar que da **5 minadas en las seis
  corridas**. Linea base medida el 2026-09-18 con el codigo actual: 4, 3, 3, 1, 2, 3
- [ ] 6.2 Repetir con `maxInflightPerUser = 16` y verificar que sigue dando 5 de 8, sin regresion
  respecto de la linea base actual (5 en las seis corridas)
- [ ] 6.3 Con el monitor abierto, reproducir una rafaga que se pase del cupo y verificar en el panel
  de eventos que la desalojada aparece con `relay.turn` `reason=too_many_inflight` y que ninguna
  metatx del medio de la cadena queda huerfana
- [ ] 6.4 Verificar que el cupo del usuario nunca supera el maximo durante la rafaga, observando que
  la suma de retenidas y en vuelo que informa el monitor no pasa de `maxInflightPerUser`
