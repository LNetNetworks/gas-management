## Context

Ver `proposal.md` para el por que. Lo que cambio desde que se escribio, y que hay que corregir antes
de planificar sobre datos viejos:

1. **El lock global ya se acoto.** El proposal dice que `controller/processController.go:163`
   serializa todos los relays de punta a punta. Eso fue cierto hasta `02-add-relay-http-endpoints`:
   hoy el candado es `relayLock` (`service/prepare.go:101`), se toma dentro de `ReserveGasAndSend`
   (`service/prepare.go:200`) y cubre solo `VerifyGasLimit` mas el envio; `POST /relay` espera el
   receipt afuera. Lo que falta de ese bullet es la serializacion **por usuario** de la reserva del
   nonce del hub, que si es nueva.
2. **`relay.settled` ya se emite.** Sale de `service/relay.go:240` al resolver un receipt, asi que
   el requisito "Alcance de emision" de `relay-event-stream` -que lo daba por no emitido- estaba
   desactualizado y el delta lo corrige. Lo que agrega el watcher es detectarlo **sin que el cliente
   pregunte**.

El estado de partida real:

- `service/relaySignerService.go:791-829`: la cache `senders` (`next`, `updatedAt`), con TTL y
  `invalidateNonce`. Solo la lee `GetTransactionCount(pending)` y, desde 02, `NonceOf`
  (`service/nonce.go:53`). Nadie valida contra ella al enviar.
- `service/nonce.go:64`: el parametro `peek` se acepta y se descarta (`_ = peek`).
- `service/correlation.go`: el puente `transactionHash -> metaTxId`, que ya existe para que
  `relay.settled` y `relay.hub_rejected` salgan con su `metaTxId` desde otra peticion.
- `service/relaySignerService.go:660`: `ProcessNewBlocks`, suscripcion WS a cabeceras con
  reconexion y backoff acotado. Hoy solo resetea el cupo de gas por bloque.
- `model/RuntimeConfig.go`: el bloque `[reorder]` se lee clave por clave y hoy no gobierna nada.
- `go-ethereum v1.9.15` (2020) y un cliente RPC por request (`Connect`/`Close`).

## Goals / Non-Goals

**Goals:**

- Que el estado de nonces sea autoritativo y que la validacion ocurra **antes** de gastar una
  transaccion del writer node.
- Que una metatx adelantada espere en vez de morir, sin que esa espera ate al resto del servicio.
- Que el flag apagado deje el comportamiento observable **identico** al de hoy, y que esa
  equivalencia se verifique, no se afirme.
- Que ninguna estructura nueva crezca sin cota ni deje a un usuario trabado.

**Non-Goals:**

- El pre-chequeo por simulacion `eth_call` (queda para otro cambio).
- El cursor de nonces del lado del cliente: vive en el `MetaTxClient` de Node, no en el relayer.
- Multi-instancia: el estado vive en memoria del proceso, igual que en Node y que el cupo de gas.
  Este servicio tiene que seguir siendo el unico que use la clave del writer node.
- Reemplazar el cliente RPC por request por uno compartido. Es deseable y esta en el proposal como
  impacto posible, pero es un cambio transversal propio y no lo necesita ninguno de los requisitos.

## Decisions

### D1. El tracker reemplaza la cache, y apagado ES la cache

Una sola estructura por usuario -`next`, `pending`, y la marca de tiempo que ya tenia la cache- con
su propio mutex, en `service/`. No conviven dos fuentes de verdad sobre el mismo nonce: dos cuentas
separadas terminan discrepando y el sintoma es un usuario que no puede relayar sin que nada este
roto (es la leccion de D5 de 03).

Con `reorder.enabled = false` el tracker se comporta **exactamente** como la cache que reemplaza:
se escribe despues de enviar, se lee solo para responder el nonce pendiente, expira por TTL y se
invalida ante un `BadTransactionSent`. No reserva, no valida, no retiene. La equivalencia se
verifica con los mismos tests que hoy cubren la cache, corriendo contra el tracker.

| Alternativa | Por que no |
|---|---|
| Dejar la cache y agregar el tracker al lado | dos verdades sobre el mismo numero; el bug no aparece hasta una rafaga |
| Tracker siempre activo, flag solo para retener | cambia la semantica observable con el flag apagado, que es justo lo que el flag promete no hacer |

### D2. Dos candados, con orden fijo: primero el del usuario, despues el global

- **Por usuario**: cubre reservar el nonce del hub y el envio. Serializa la asignacion sin
  serializar el throughput entre usuarios.
- **Global** (`relayLock`, el que ya existe): cubre la reserva del cupo de gas y el envio, porque el
  nonce de la **cuenta** del writer node se resuelve dentro del envio.

El orden es siempre usuario → global, nunca al reves, y el candado del usuario no se sostiene
mientras se espera un receipt. El invariante que hace que encadenar funcione es que los nonces del
hub se reserven en el mismo orden en que se toman los de la cuenta del writer node: por eso la
reserva y el envio van bajo el mismo candado de usuario, y no en dos pasos.

Se evita explicitamente llamar al nodo con el candado global tomado mas alla de lo que ya hace hoy
`VerifyGasLimit`: cada llamada RPC adentro de un candado global es throughput que se pierde para
todos.

| Alternativa | Por que no |
|---|---|
| Un solo candado global para todo | es lo que ya se acoto en 02; volver atras anula el reordenamiento |
| Reservar el nonce y enviar en dos secciones criticas | entre las dos, otra metatx del mismo usuario puede tomar el nonce de la cuenta antes y romper el orden de minado |

### D3. La espera vive en la goroutine de la peticion, no en una cola aparte

Una metatx adelantada espera sobre un canal, despertada por dos fuentes: el avance del nonce
esperado de su usuario (cuando otra metatx de la rafaga se envia o se resuelve) y un temporizador.
No hay cola persistente ni goroutine despachadora.

```
   llega nonce n                      esperado = e
        |
        +-- n <= e ---> enviar
        |
        +-- n >  e ---> relay.held
                         |
                    espera hasta: avanza e  -> reevaluar (y renovar la ventana)
                                  vence     -> relay.turn(window_expired) -> rechazo BAD_NONCE
                                  cupo lleno-> relay.turn(too_many_inflight) -> rechazo
```

La ventana se renueva **cada vez que `e` avanza**, no desde que la metatx llego: mide estancamiento.
Con doce metatx a un segundo de envio cada una, una ventana de tres segundos medida sobre el total
tiraria la cola mientras el hub va por la quinta.

Consecuencia observable, aceptada y ya escrita en el proposal: con el flag encendido,
`eth_sendRawTransaction` de una metatx adelantada no responde hasta que le toca el turno o vence la
ventana. Es inherente a reordenar sobre HTTP y es lo que hace Node.

| Alternativa | Por que no |
|---|---|
| Cola con goroutine despachadora por usuario | mas piezas y el mismo resultado; ademas hay que apagarla cuando el usuario se vacia |
| Responder al cliente "reintenta" | traslada el problema al cliente, que es justo lo que el buffer evita |

### D4. La cadena de nonces tiene identidad, y un resultado atrasado no toca la que la reemplazo

Cada cadena de un usuario es un valor con identidad propia (un puntero, comparado por identidad al
liberar). Al romperse -envio fallido o rechazo del hub- se descarta entera y la proxima metatx relee
la cadena. Un receipt que llega tarde y pertenece a una cadena ya descartada no descuenta sobre la
nueva.

Sin esto, el conteo de en vuelo queda corrido para siempre despues del primer fallo, y el sintoma es
un usuario que agota su cupo sin tener nada en vuelo. Es exactamente lo que resuelve `releasePending`
en Node comparando la referencia.

La gracia antes de olvidar (`windowMs`) usa la misma ventana que la retencion, a proposito: las dos
responden a la misma pregunta -cuanto dura una rafaga- y dos numeros distintos para lo mismo es una
perilla mas sin informacion nueva.

### D5. El watcher se cuelga de la suscripcion a bloques que ya existe

`ProcessNewBlocks` ya tiene lo caro: suscripcion WS, reconexion con backoff acotado y la garantia de
no morir. En cada cabecera nueva, ademas de resetear el cupo, se resuelven los resultados de lo que
haya en vuelo. Sin nada en vuelo no hace ninguna llamada extra.

El plazo de `receiptTimeoutMs` corre por metatx: al vencer se emite `relay.settle_failed` y se libera
su posicion. Una metatx perdida no puede consumir cupo para siempre.

| Alternativa | Por que no |
|---|---|
| Poller propio con su timer | duplica la reconexion y el backoff que ya estan resueltos |
| Filtro de logs del hub por bloque | mas eficiente con volumen alto, pero es otra superficie contra `go-ethereum v1.9.15`; queda como pregunta abierta |

### D6. El cierre se registra una sola vez, y lo arbitra el puente que ya existe

`service/correlation.go` ya mapea `transactionHash -> metaTxId` para que el cierre salga con su
identificador desde otra peticion. Ahi mismo se marca la metatx como cerrada: gana quien llegue
primero -el watcher o la consulta del cliente- y el segundo no emite nada. La respuesta al cliente
que consulta el receipt no cambia en ningun caso.

### D7. El reparto de nonces entrega tickets, no reservas

Con `reorder.autoNonce = true`, las consultas de un usuario se serializan y cada una se lleva un
numero distinto. Lo que se abre es un **ticket**, no una reserva: si vence sin que llegue la metatx
que lo use, `next` no avanzo y el siguiente que pregunte se lleva ese mismo numero.

Es la diferencia que Node documenta por haber probado las dos: con reservas, una reserva sin usar
deja un hueco que solo su duenio puede destapar y el resto de los clientes se traba detras.

Por que del lado del servicio y no del cliente: el nonce va adentro de lo firmado -el hub recupera
el `from` de esos mismos bytes-, asi que el relayer no puede reescribirlo al recibir la metatx. Lo
unico que controla es el numero que entrega antes de que el cliente firme.

Apagado por defecto: encenderlo cambia lo que dos clientes concurrentes reciben, y `GET /info` ya
publica `autoNonce` y `autoNonceTicketMs` (hoy fijos en `false` y `0`), que pasan a informar la
configuracion real.

### D8. El flag apagado no ejecuta el camino nuevo

Con `reorder.enabled = false` no se entra al tracker autoritativo, ni a la espera, ni al watcher: no
es que se ejecuten y no hagan nada. Es la misma decision que D7 de 03 con las rutas del monitor, y
por el mismo motivo: lo que no se ejecuta no puede cambiar el comportamiento por accidente.

## Risks / Trade-offs

- **Es el unico cambio de la serie que toca el camino critico que hoy corre en produccion** → el
  flag apagado por defecto, la equivalencia verificada contra el binario anterior, y el orden de la
  serie (va ultimo).
- **Dos candados nuevos pueden introducir carreras o bloqueos** → orden fijo usuario → global, sin
  esperas de receipt con un candado tomado, sin llamadas RPC nuevas adentro del global, y
  `go test ./... -race` con rafagas concurrentes de varios usuarios.
- **Semantica observable con el flag encendido**: una metatx adelantada no responde hasta su turno →
  acotado por `windowMs` y por `maxInflightPerUser`, y documentado en el README.
- **Estructuras por usuario que crecen** → cada usuario se olvida al vaciarse (tras la gracia), el
  cupo por usuario acota lo retenido, y los mapas auxiliares de espera se borran al quedar vacios.
- **Una rafaga que se rompe a la mitad gasta transacciones del writer node** por las que ya salieron
  → es lo que acota `maxInflightPerUser`, y es la razon de que exista.
- **El watcher agrega llamadas al nodo por bloque** → solo cuando hay algo en vuelo, y por metatx en
  vuelo, no por bloque completo.

## Migration Plan

1. Se despliega con `reorder.enabled = false`: el binario nuevo se comporta como el anterior, y esa
   equivalencia se verifica contra el binario previo antes de encender nada.
2. Se enciende `reorder.enabled` en un despliegue de prueba, con el monitor de 03 abierto para ver
   la retencion y el reordenamiento en vivo.
3. `reorder.autoNonce` se enciende despues y por separado: cambia lo que reciben los clientes que
   consultan el nonce, no solo lo que pasa al enviar.
4. Rollback: apagar el flag y reiniciar. No hay estado persistido ni migracion de datos; lo unico
   que se pierde es lo que el proceso tenia en vuelo, que es lo que ya pasa hoy al reiniciar.

## Open Questions

- Si con volumen alto conviene resolver los resultados con un filtro de logs del hub por bloque en
  lugar de consultar por metatx en vuelo. No cambia ningun requisito ni el corte de tareas: es una
  optimizacion interna del watcher, medible recien con carga real.
- Si `nonceCacheTTL` sigue teniendo sentido como red de seguridad una vez que la gracia y el
  descarte de cadena rota hacen ese trabajo. Se conserva mientras tanto; retirarlo seria un cambio
  aparte, con su propia verificacion.
