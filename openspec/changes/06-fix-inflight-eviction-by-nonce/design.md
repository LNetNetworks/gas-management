## Context

Ver `proposal.md` - Why para la motivacion y los numeros medidos.

Lo que condiciona el diseno es que las estructuras actuales **no guardan el nonce de lo retenido**:

- `service/tracker.go:228` - `waitingOf` lee `heldCount[key]`, un **contador** por usuario. Sabe
  cuantas hay retenidas, no cuales.
- `service/tracker.go:183-195` - `turnWaiters[key]` es un conjunto de canales **anonimos**. No hay
  forma de asociar un canal con la metatx que duerme en el.
- `service/tracker.go:210-220` - `notifyTurnLocked` despierta a **todos** cerrando sus canales; la
  unica senal que existe hoy es "reevalua", igual para todos.
- `service/reorder.go:129` - el cupo se comprueba antes de `awaitTurn`, que es la unica pieza que
  conoce nonces.

Es decir: ni se puede saber cual es el nonce mas alto retenido, ni despertar a uno solo, ni decirle
algo distinto de "reevalua". Las tres cosas hacen falta.

Tambien importa el comentario de `reorder.go:127-128`, que explica por que el chequeo esta donde
esta: *"una rafaga que ya se paso del techo no tiene que hacer cola para enterarse"*. La intencion
-responder rapido- es buena y se conserva; lo que cambia es que la decision deje de tomarse a ciegas.

## Goals / Non-Goals

**Goals:**

- Que el descarte por cupo elija por nonce y no por orden de llegada.
- Que el resultado de una rafaga sea **determinista**: mismo conjunto de metatx, mismo desenlace,
  sin importar el orden de llegada HTTP.
- Conservar el rechazo inmediato: quien tiene el nonce mas alto se entera enseguida, sin hacer cola.
- No introducir codigos de error nuevos ni cambiar la superficie HTTP.

**Non-Goals:**

- No se toca el techo de la red (~5 metatx en vuelo por writer node, impuesto por el txpool de
  Besu). Este cambio reparte mejor el cupo configurado, no lo agranda.
- No se cambia `maxInflightPerUser` ni su default de 16.
- No se toca la ventana de reordenamiento ni el watcher de resultados.
- No se propone un reparto justo entre usuarios: el cupo sigue siendo por usuario y global del nodo,
  y esa asimetria queda como esta.
- No se hace **estricto** el cupo instante a instante. Hoy la puerta no serializa a las peticiones de
  un mismo usuario y el tope se puede pasar por un momento; eso se conserva. Lo que el cambio
  garantiza es a quien se rechaza y que el cupo converja al tope. Ver D3.

## Decisions

### D1: El registro de retenidas pasa de contador a mapa con nonce

`heldCount map[string]int` se reemplaza por un registro por usuario que asocia cada metatx retenida
con su nonce y con la forma de desalojarla. `waitingOf` sigue existiendo con la misma semantica -su
comentario actual sigue valiendo: cuenta lo retenido aunque entre dos vueltas de la espera no este
dormido- y pasa a leerse del tamano del registro.

**Alternativa descartada:** llevar en paralelo un `maxHeldNonce map[string]uint64`. Mas barato, pero
al desalojar hay que recalcular el maximo recorriendo igual, y quedan dos estructuras que se pueden
desincronizar. Una sola fuente de verdad cuesta lo mismo y no miente.

### D2: El desalojo se senaliza con una marca, no con un canal nuevo

La entrada retenida gana una marca de desalojada. Al desalojar se marca y se cierra su canal; la
goroutine, al despertar, ve la marca y devuelve `tooManyInflight` en vez de reevaluar el nonce.

Esto preserva el patron actual -canales que se cierran y nunca se escriben, como documenta
`waitTurnLocked`-, asi que despertar sigue sin poder bloquear a quien despierta.

**Alternativa descartada:** un canal de desalojo por waiter al que se le envia un valor. Obliga a
un `select` con mas ramas y a manejar el caso de nadie escuchando; la marca no.

### D3: El desalojo no admite a nadie; el cupo converge en la espera

El desalojo se limita a **sacar a la victima**. No reserva el lugar liberado para quien lo provoco,
porque la puerta no tiene donde anotarlo: el lugar de una metatx recien se ocupa en `hold()`, ya
dentro de `awaitTurn`, o en `reserveLocked` al enviar. Entre la comprobacion de la puerta y esa
anotacion no hay lock tomado -`lockUser` es posterior a `awaitTurn`-, asi que varias peticiones del
mismo usuario pasan la puerta con la misma lectura de `inflightOf`.

Esto **no es una regresion**: es como funciona hoy, y es la razon de ser del segundo chequeo (D6).

Lo que decide el diseno es que los dos predicados tienen propiedades de concurrencia opuestas:

    PUERTA    "existe alguna retenida con nonce MAYOR que el mio"
              lo pueden satisfacer TODAS a la vez -> no se autolimita

    ESPERA    "YO soy la de nonce mas alto entre las retenidas"
              lo satisface EXACTAMENTE UNA        -> se autolimita

Por eso la invariante del cupo no vive en la puerta sino en la espera. La puerta es el camino
rapido -responder enseguida a quien claramente sobra- y puede dejar pasar de mas; la espera es la
que devuelve el cupo a su tope, rechazando de a una a las de nonce mas alto hasta converger.

    t0  retenidas = 7   (desborde de una rafaga simultanea, cupo 5)
    t1  retenidas = 6   se rechaza la de nonce mas alto, TOO_MANY_INFLIGHT
    t2  retenidas = 5   idem la siguiente
        -> quedan las 5 de nonce MAS BAJO: el mismo conjunto, por otro camino

El desborde es transitorio, dura a lo sumo una ventana (`reorder.windowMs`, 3000 ms por defecto) y
no gasta ni una transaccion del writer node: las que sobran nunca se enviaron.

Lo que **si** ocurre bajo `sendersLock` en una sola seccion critica es elegir la victima, marcarla y
descontarla del registro. **El descuento no espera a que la goroutine desalojada despierte**: se
descuenta al marcar, y la goroutine, al despertar, ve que ya no esta registrada y no vuelve a
descontar. Sin eso el mismo lugar se descontaria dos veces.

**Alternativa descartada: cupo duro, reservando en la puerta.** Obliga a que el registro deje de ser
"las que estan retenidas" y pase a ser "las que tienen lugar asignado", con alta antes de
`awaitTurn` y una transicion reservado -> retenida | en vuelo. La reserva habria que liberarla en
los seis caminos de salida que ya existen -error de `expectedNonce`, `badNonce`, fallo de
`reserveGasAndSend`, `window_expired`, `client_gone`, `too_many_inflight`-, y el modo de falla
empeora: un desborde es transitorio y se autocorrige, pero una reserva filtrada es **pegajosa**
-ese usuario pierde un lugar de su cupo hasta que se reinicie el proceso- y no hay nada que la
detecte. Se paga estructura y un fallo peor para comprar una exactitud instantanea que no es el
objetivo: el objetivo es que el conjunto sobreviviente sea determinista, y eso se consigue igual.

**Alternativa descartada: frontera fija en `expected + C`, sin desalojo.** Rechazar en la puerta
toda metatx cuyo nonce sea `>= expected + maxInflightPerUser` llega al mismo conjunto sobreviviente
-las C de nonce mas bajo- sin registro con nonce, sin marca de desalojo y sin nada de D1 ni D2: una
comparacion aritmetica con un valor que la puerta ya tiene a mano.

        expected = e , C = 5

        e   e+1  e+2  e+3  e+4 | e+5  e+6  e+7
        [-------- admite ------]|[--- rechaza ---]
                                ^
                           frontera fija, no depende del orden de llegada

Se descarta por tres motivos, en orden de peso:

1. **Pelea con la razon de ser de la ventana.** El reordenamiento existe para retener a la que llego
   adelantada hasta que se cierre el hueco. Una frontera fija la rechaza en la puerta, antes de
   darle la chance: si la cadena avanza durante la ventana, un nonce que estaba en `e+6` pasa a
   estar en turno, y con la frontera nunca llega a enterarse. Se tiraria justo el caso que la
   funcionalidad esta para rescatar.
2. **No acota lo que el cupo acota.** `expected` es el proximo nonce sin enviar, asi que no cuenta a
   las que ya salieron y siguen sin resolverse. Un usuario con pendientes admitiria C **mas alla**
   de esas, superando el tope. Habria que conservar igual el `inflight >= C`, y entonces reaparece
   el caso que motiva este change -cupo lleno de retenidas altas y llega una baja-, que la frontera
   sola no resuelve.
3. **Le da dos significados a una sola clave.** `maxInflightPerUser` hoy responde "cuanto del nodo
   puede ocupar un usuario". La frontera lo convierte ademas en "cuanto puede adelantarse una metatx
   respecto de la cadena", que es lo que ya mide `reorder.windowMs` en tiempo. Dos cotas distintas
   detras del mismo numero se vuelven imposibles de ajustar por separado.

Vale anotarla igual porque es, de lejos, la version mas barata de la idea, y si alguna vez se
decide que retener adelantadas no compensa, es el diseno al que habria que volver.

### D4: Empates y casos borde

- **La que llega tiene nonce mayor o igual que el maximo retenido:** se la rechaza a ella, como hoy.
  Con nonce igual no hay nada que ganar cambiando de victima, y preferir a la que ya espera mantiene
  el trabajo ya hecho.
- **Dos retenidas con el mismo nonce:** desaloja la que llego despues. Son duplicados; el hub solo
  aceptara una.
- **No hay retenidas y el cupo esta lleno de en-vuelo:** se rechaza la que llega. Lo en vuelo ya
  gasto una transaccion del writer node y no se puede deshacer.

### D5: Sin flag nuevo

El camino entero vive detras de `[reorder] enabled`, que ya viene apagado por defecto, asi que la
invariante 2 del proyecto se cumple sin agregar nada. Un sub-flag para elegir entre dos politicas de
descarte multiplicaria configuraciones que probar para conservar un comportamiento que medimos como
peor y no determinista.

**Alternativa considerada:** `reorder.evictByNonce` con default `true`. Se descarta: nadie va a
querer el comportamiento viejo a proposito, y un flag es una promesa de soporte.

### D6: El chequeo de la espera es donde vive la invariante del cupo

`reorder_turn.go:121` vuelve a comprobar el cupo mientras la metatx espera, y hoy rechaza a la que
esta despertando -de nuevo, sin mirar nonces-. Pasa a preguntarse otra cosa: **si soy la de nonce
mas alto entre las retenidas de mi usuario, la que sobra soy yo**; si no, sigo esperando.

Este chequeo no es "el mismo criterio de la puerta, aplicado tambien aca". Es al reves: por D3, la
puerta puede dejar pasar de mas y este es el unico punto que puede corregirlo, porque su predicado
lo satisface una sola goroutine a la vez. La puerta optimiza el tiempo de respuesta; esta comprueba
la invariante.

De ahi se explica la asimetria de operadores, que **se mantiene**: la puerta usa `>=` -"hay lugar
para una mas?"- y la espera `>` -"el cupo se paso?"-. No son el mismo operador porque no son la
misma pregunta, y el `>` es alcanzable precisamente porque la puerta desborda. Conviene dejarlo
escrito en un test para que no se "corrija" por parecer un error de tipeo.

Corolario del que depende la convergencia: el desborde se corrige **cuando los que esperan
despiertan**, sea por `notifyTurnLocked` -avanzo el nonce esperado- o por el vencimiento del timer.
No hay un barrido aparte, y no hace falta: hasta que despierten, las de mas solo ocupan memoria.

## Risks / Trade-offs

- **[Una metatx retenida puede ser desalojada despues de haber esperado]** → Es el punto del cambio:
  esperar no da derecho a sobrevivir si otra puede destrabar la cola. El cliente recibe el mismo
  `TOO_MANY_INFLIGHT` de siempre y no se gasto ninguna transaccion del writer node por ella.

- **[Desborde transitorio del cupo]** → Por D3 la puerta puede dejar pasar de mas ante una rafaga
  simultanea del mismo usuario, y el tope se recupera recien cuando los que esperan despiertan. El
  costo son goroutines retenidas de mas durante a lo sumo una ventana; ninguna gasta transacciones
  del writer node. Hoy pasa lo mismo -el `>` de `reorder_turn.go:121` existe por eso-, asi que no es
  una regresion, pero con desalojo el camino se recorre mas seguido y conviene un test que mida el
  desborde maximo y verifique que converge al tope.

- **[Mas estado bajo `sendersLock`]** → El lock ya cubre el registro de waiters y el tracker. El
  trabajo agregado es recorrer las retenidas de **un** usuario, acotadas por `maxInflightPerUser`
  (16 por defecto): decenas de elementos, no miles. Aun asi, elegir la victima no debe hacer E/S ni
  llamar a la cadena dentro de la seccion critica.

- **[Una tormenta de desalojos si un cliente insiste con nonces bajos]** → Un cliente que reenvia en
  bucle la misma metatx de nonce bajo desalojaria una y otra vez a las altas. Acotado por el propio
  cupo y por la ventana, pero conviene cubrirlo con un test que verifique que el sistema converge y
  no entra en un ciclo de desalojos mutuos.

- **[Divergencia con `node_relayer`]** → Node rechaza al que llega (`src/relayer.ts:839-840`). Al
  cambiarlo, Go deja de estar en paridad en este punto, a favor. Queda registrado en el proposal;
  corresponde abrir el reporte equivalente contra Node.

## Migration Plan

No hay migracion de datos ni de configuracion: el estado es en memoria y `config.toml` no cambia.

El despliegue es el binario nuevo; el rollback, el binario anterior. Ninguna de las dos direcciones
necesita tocar configuracion ni reiniciar Besu.

Con `[reorder] enabled = false` no se entra a este camino, asi que un despliegue que no use el
reordenamiento no puede verse afectado.
