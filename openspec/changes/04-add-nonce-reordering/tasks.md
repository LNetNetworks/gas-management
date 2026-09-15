## 1. Tracker de nonces en vuelo

- [x] 1.1 Crear la estructura por usuario con `next`, `pending` y la identidad de la cadena, con su
  propio mutex, segun D1 y D4, y verificar con un test que reservar, liberar y descartar desde
  varias goroutines a la vez no pierde ni duplica ninguna posicion
- [x] 1.2 Reemplazar la cache `senders` por el tracker manteniendo apagado el comportamiento actual
  -escritura despues de enviar, lectura solo para el nonce pendiente, TTL e invalidacion ante
  `BadTransactionSent`-, y verificar que los tests que hoy cubren la cache pasan sin cambios contra
  el tracker
- [x] 1.3 Servir `eth_getTransactionCount(pending)` y el `nextNonce` / `pending` de
  `GET /nonce/{address}` desde el tracker, y verificar con un test que las dos puertas responden el
  mismo valor para la misma direccion en el mismo momento
- [x] 1.4 Implementar la gracia antes de olvidar a un usuario que se quedo sin metatx en vuelo,
  segun D4, y verificar con tests que durante la gracia el proximo nonce sigue siendo el reservado,
  que pasada la gracia se vuelve a leer de la cadena, y que una metatx nueva durante la gracia
  continua la cadena en curso en vez de empezar una nueva
- [x] 1.5 Descartar la cadena entera ante un envio fallido o un rechazo del hub, y verificar con un
  test que un resultado que llega tarde y pertenece a una cadena ya descartada no altera la cantidad
  en vuelo ni el proximo nonce de la cadena que la reemplazo
- [x] 1.6 Verificar con un test que ninguna secuencia de fallos, resultados atrasados y peticiones
  concurrentes deja a un usuario sin poder relayar

## 2. Reserva y validacion en el envio

- [x] 2.1 Agregar el candado por usuario que cubre reservar el nonce del hub y enviar, con el orden
  usuario → global de D2, y verificar con un test que dos metatx del mismo usuario con nonces
  consecutivos se envian en orden de nonce y que metatx de usuarios distintos no se esperan entre si
- [x] 2.2 Validar el nonce contra el tracker antes de enviar y rechazar el que no corresponde
  indicando el esperado y el recibido, y verificar con un test que no se envia ninguna transaccion
  al nodo en ese caso
- [x] 2.3 Verificar con un test que un usuario puede encadenar varias metatx con nonces consecutivos
  sin esperar a que se minen, y que ninguna se rechaza por traer un nonce que la cadena todavia no
  refleja
- [x] 2.4 Verificar con un test `-race` que una rafaga concurrente de varios usuarios no produce
  carreras ni bloqueos, y que ningun candado se sostiene mientras se espera un receipt

## 3. Buffer de reordenamiento

- [x] 3.1 Retener la metatx cuyo nonce es mayor al esperado y despertarla cuando el esperado avanza,
  segun D3, y verificar con un test que dos metatx que llegan en orden invertido se envian las dos,
  en orden de nonce
- [x] 3.2 Implementar la ventana como plazo por estancamiento, renovado cada vez que el esperado
  avanza, y verificar con tests que una rafaga que tarda mas que la ventana pero avanza no pierde
  ninguna, y que una espera sin avance termina por vencimiento
- [x] 3.3 Rechazar al vencer con el mismo motivo que un nonce equivocado, y verificar con un test
  que no se envio ninguna transaccion al hub por esa metatx
- [x] 3.4 Aplicar el tope de metatx en vuelo por usuario en la puerta y tambien durante la espera, y
  verificar con tests que al superarlo se rechaza indicando cuantas hay y el maximo, que el motivo
  es el del tope y no el del nonce cuando el cupo se llena durante la espera, y que las metatx de
  otros usuarios se atienden con normalidad
- [x] 3.5 Emitir `relay.held` y `relay.turn` con los campos del vocabulario de `relay-event-stream`,
  y verificar con un test que una metatx retenida y despues enviada deja los dos eventos con su
  `metaTxId`, y que una que no se retiene no deja ninguno
- [x] 3.6 Verificar con un test que una metatx retenida no demora la respuesta de las metatx de
  otros usuarios ni la de las demas rutas

## 4. Watcher de resultados

- [x] 4.1 Resolver los resultados de lo que haya en vuelo en cada cabecera nueva, colgado de
  `ProcessNewBlocks` segun D5, y verificar con un test que una metatx relayada por el camino
  JSON-RPC y nunca consultada deja igual su `relay.settled` con su `metaTxId`
- [x] 4.2 Verificar con un test que sin nada en vuelo el watcher no hace ninguna llamada extra al
  nodo
- [x] 4.3 Arbitrar el cierre unico en el puente de correlacion segun D6, y verificar con un test que
  una metatx cerrada por el watcher y consultada despues por el cliente no emite un segundo cierre,
  y que la respuesta al cliente no cambia
- [x] 4.4 Descontar del tracker al cerrarse y despertar a las metatx retenidas de ese usuario, y
  verificar con un test que al cerrarse una baja la cantidad en vuelo y las retenidas reevaluan su
  turno
- [x] 4.5 Acotar la espera del resultado con `receiptTimeoutMs`, emitir `relay.settle_failed` al
  vencer y liberar la posicion, y verificar con un test que una metatx sin resultado no deja al
  usuario con cupo consumido
- [x] 4.6 Verificar con un test que un fallo al observar la cadena no tumba el proceso, que la
  observacion se reanuda sola cuando el nodo vuelve, y que un resultado que no se puede interpretar
  se registra como indeterminado en lugar de propagarse

## 5. Reparto de nonces

- [x] 5.1 Agregar `reorder.autoNonce` y `reorder.autoNonceTicketMs` al bloque, apagados por defecto,
  y verificar con un test que con el reparto apagado la respuesta con y sin `peek` es identica a la
  de hoy y consultar no cambia ningun estado
- [x] 5.2 Serializar las consultas del mismo usuario y entregar un numero distinto a cada una con el
  reparto encendido, segun D7, y verificar con un test que dos consultas concurrentes reciben nonces
  distintos y consecutivos
- [x] 5.3 Implementar el ticket que vence sin tapar el numero, y verificar con un test que un nonce
  entregado y no usado vuelve a entregarse al siguiente que consulte y que nadie queda esperando de
  forma indefinida
- [x] 5.4 Verificar con un test que `peek=true` sigue sin reservar con el reparto encendido

## 6. Configuracion y superficie

- [x] 6.1 Hacer que cada clave de `[reorder]` gobierne lo que le corresponde, y verificar con tests
  que un valor fuera de rango se descarta, se usa el defecto y el servicio arranca igual dejando
  registro
- [x] 6.2 Informar en `GET /info` los valores vigentes del bloque, incluidos `autoNonce` y
  `autoNonceTicketMs` que hoy son fijos, y verificar con un test que la respuesta refleja la
  configuracion real
- [x] 6.3 Verificar con un test que con `reorder.enabled = false` no se entra al tracker
  autoritativo, ni a la espera, ni al watcher, segun D8
- [x] 6.4 Documentar el bloque en `config.toml` y en el README -incluida la semantica observable con
  el flag encendido- y actualizar `node_relayer/ENDPOINTS-GO-VS-NODE.md` con las diferencias que
  esta serie elimina

## 7. Verificacion de cierre

- [x] 7.1 Correr `go test ./... -race` y verificar que pasa, con atencion a las rafagas concurrentes
  de varios usuarios
- [x] 7.2 Verificar contra el binario anterior que las respuestas de `POST /` y de las rutas
  existentes son identicas con el reordenamiento apagado, incluido el caso de una metatx con el
  nonce equivocado
- [x] 7.3 Verificar que el servicio arranca con el `config.toml` de una instalacion previa y que el
  reordenamiento y el reparto quedan apagados sin configurar nada
- [x] 7.4 Lanzar rafagas de 6, 12 y 20 metatx de un mismo usuario contra el servicio en ejecucion y
  verificar que se minan todas, sin ningun `BadNonce` y sin transacciones del writer node gastadas
  de mas
- [x] 7.5 Lanzar rafagas de varios usuarios a la vez y verificar que ninguno demora al otro y que
  cada uno conserva su orden de nonce
- [x] 7.6 Verificar el caso de `maxInflightPerUser` excedido y el de una metatx que vence en el
  buffer: motivo correcto en la respuesta, y nada gastado en la cadena
- [ ] 7.7 Correr `node_relayer/examples/nonce-stress.ts --n 6` apuntado a este servicio y verificar
  que da el mismo resultado que contra el relayer de Node
- [x] 7.8 Con el monitor de 03 abierto, reproducir una rafaga desordenada y comparar con la corrida
  de referencia de `node_relayer/DASHBOARD.md` -llegada `72,68,70,67,71,69`, envio `67..72`, 4
  reordenadas-, verificando que la pagina pinta la retencion y el reordenamiento sin huecos y sin
  errores en la consola del navegador
