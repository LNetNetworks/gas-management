## 1. Configuracion

- [x] 1.1 Agregar a `model/Config.go` los bloques `[reorder]`, `[dashboard]` y `[log]` con sus
  campos, y verificar con un test que un `config.toml` sin ninguno de los tres carga y deja cada
  parametro en el default de la tabla del spec
- [x] 1.2 Leer esos tres bloques clave por clave, fuera del `Unmarshal` compartido y sin declarar
  defaults en el decodificador —que anulan la deteccion de clave ausente—, y verificar con un test
  que un valor no interpretable en una clave nueva no impide que el resto de la configuracion
  cargue
- [x] 1.3 Aplicar el default y registrar la clave descartada cuando el valor no es interpretable
  como su tipo, y verificar con tests por caso: `bufferSize` no numerico y `level` no reconocido;
  en los dos el arranque no aborta
- [x] 1.4 Validar el rango de las claves numericas, que el decodificador acepta sin objetar, y
  verificar con un test que `windowMs = -1` toma el default `3000`, registra el descarte y el
  servicio arranca normalmente
- [x] 1.5 Distinguir la clave ausente del `0` explicito en `dashboard.bufferSize`, y verificar con
  tests que ausente toma `500` y que `0` deja el bus apagado sin retener eventos
- [x] 1.6 Sumar las claves nuevas comentadas a `config.toml` como plantilla, y verificar que el
  servicio arranca con el `config.toml` de una instalacion previa sin emitir error ni advertencia

## 2. Bus de eventos

- [x] 2.1 Crear el paquete `events/` con el ring buffer de capacidad fija y el contador `seq`, y
  verificar con tests que `seq` es estrictamente creciente y que al superar la capacidad se
  conservan los mas recientes sin alterar su `seq`
- [x] 2.2 Implementar el replay posterior a un `seq` y verificar con tests los tres casos del spec:
  desde un `seq` intermedio, desde uno ya descartado (devuelve todo lo retenido, sin error) y sin
  indicar punto de partida
- [x] 2.3 Implementar la suscripcion con un canal con buffer por suscriptor y envio no bloqueante
  segun D4, y verificar con un test que un suscriptor que no lee no bloquea la publicacion y que
  los demas siguen recibiendo
- [x] 2.4 Recuperar el panico dentro de la goroutine de entrega de cada suscriptor, y verificar con
  un test que un suscriptor que entra en panico no tumba el proceso ni impide que los demas
  reciban el mismo evento
- [x] 2.5 Cortocircuitar `Publish` con una bandera atomica cuando `dashboard.enabled` es `false`,
  y verificar con un benchmark que en ese caso no se toma ningun candado y el costo por evento es
  despreciable frente al camino habilitado

## 3. Log estructurado

- [x] 3.1 Agregar a `audit/` el emisor de una linea JSON por evento hacia la salida estandar, con
  `warn` y `error` por la de error, y verificar con un test que cada emision produce exactamente
  una linea que parsea como objeto JSON con `ts`, `level`, `event` e `instanceId`
- [x] 3.2 Generar el `instanceId` una vez por proceso y verificar con un test que todas las lineas
  de un mismo proceso lo comparten
- [x] 3.3 Aplanar y acotar el texto multilinea de un error antes de emitirlo, y verificar con un
  test que un error con traza de varias lineas sigue produciendo una sola linea JSON valida
- [x] 3.4 Publicar en el bus antes de aplicar el filtro por `log.level`, y verificar con un test que
  un evento `debug` con el nivel en `info` no se escribe en la salida pero si queda en el bus
- [x] 3.5 Llamar la inicializacion del emisor desde `main` despues de leer la configuracion, segun
  D5, y verificar que el orden de arranque es leer config, inicializar y recien despues levantar el
  servidor
- [x] 3.6 Emitir una linea reducida con `ts`, `level`, `event` y la marca del fallo cuando un evento
  no se puede serializar, y verificar con un test que la peticion que lo origino se responde
  normalmente
- [x] 3.7 Verificar con un test que el log de texto existente conserva su formato, su destino y sus
  entradas, y que ninguno de los dos logs altera el contenido del otro
- [x] 3.8 Derivar los campos de error de un evento segun D12 —`error` siempre, `code` de la misma
  fuente que arma la respuesta JSON-RPC, `errorType` del enum de `errors/`—, y verificar con tests
  que un rechazo sin codigo igual emite `error`, y que el `code` emitido coincide con el de la
  respuesta devuelta al cliente en el mismo rechazo

## 4. Correlacion por peticion y por metatx

- [x] 4.1 Propagar `context.Context` por las firmas de `controller` y `service` que participan del
  camino de relay, y verificar que `go build ./...` y `go test ./...` siguen en verde sin cambios
  en las aserciones existentes
- [x] 4.2 Generar el `reqId` en el handler HTTP antes de decodificar el cuerpo, y verificar con un
  test que una peticion con cuerpo ilegible igual emite su evento correlacionado
- [x] 4.3 Generar el `metaTxId` al entrar al camino de relay y verificar con un test que dos metatx
  distintas no comparten identificador y que sus eventos no se mezclan
- [ ] 4.4 Derivar con `context.WithoutCancel` el contexto de los eventos posteriores a la respuesta
  segun D2, y verificar con un test que un evento emitido tras retornar el handler conserva `reqId`
  y `metaTxId`
- [x] 4.5 Recordar, al enviar la metatx, el `reqId` y el `metaTxId` contra el hash de la transaccion
  enviada, con TTL por entrada y tope de entradas segun D11, y verificar con tests que una entrada
  vencida se libera y que al llegar al tope se descartan las mas antiguas
- [x] 4.6 Rehidratar ese contexto en los caminos que procesan un receipt —`GetTransactionReceipt` y
  `GetMetaTxResult`—, y verificar con un test que el evento emitido ahi lleva el `metaTxId` de la
  metatx y el `reqId` de la peticion que la relayo, no el de la peticion que consulto el receipt
- [x] 4.7 Verificar con un test que un receipt cuyo hash ya no esta en el mapa no rompe nada: el
  evento no se emite o se emite sin `metaTxId`, y la peticion que consulto el receipt se responde
  igual que antes

## 5. Emision en el camino de relay

- [x] 5.1 Emitir `relay.received` antes de decodificar, con `rawTxHash` y `rawTxBytes`, y verificar
  con un test que una raw tx malformada tambien deja el evento con su `metaTxId`
- [x] 5.2 Decodificar el sufijo del gas model —los ultimos 64 bytes del `data`— para obtener
  `nodeAddress`, `expiration`, `expiresInSeconds` y `selector`, y emitir `relay.decoded` con los
  campos del contrato; verificar con un test que estan todos los que la pagina consume
- [x] 5.3 Verificar con tests que esa decodificacion solo registra y nunca valida: un `data` mas
  corto que el sufijo emite los cuatro campos sin valor, la metatx sigue su curso y la respuesta
  JSON-RPC es identica a la actual
- [x] 5.4 Devolver desde `blockchain/client.go` la transaccion enviada en lugar de solo su hash
  para poder informar `writerNodeNonce`, y verificar con un test que la respuesta de
  `eth_sendRawTransaction` sigue siendo el hash y no cambia de forma
- [x] 5.5 Emitir `relay.sent` con los campos del contrato, incluidos los que este servicio aun no
  calcula —`simulated`, `simulatedErrorCodeName` y `pendingForUser`— con un valor interpretable en
  lugar de omitirlos
- [x] 5.6 Emitir `relay.rejected` con `error`, y con `code` y `errorType` cuando el rechazo los
  trae, y verificar con un test que comparte el `metaTxId` de los eventos previos de esa metatx
- [x] 5.7 Emitir `relay.hub_rejected` donde el servicio ya detecta `BadTransactionSent` e invalida
  el nonce del sender, con `transactionHash`, `from`, `errorCode` y el `errorCodeName` que ya
  traduce el servicio, y verificar con un test que lleva el `metaTxId` de la metatx original
- [x] 5.8 Agregar un test de contrato que recorra los eventos emitidos y falle si a alguno le falta
  `metaTxId` o cualquier campo de la tabla del spec, para que un renombrado no rompa la pagina en
  silencio

## 6. Verificacion de cierre

- [x] 6.1 Correr `go test ./... -race` y verificar que pasa, con atencion a las carreras entre la
  publicacion y la suscripcion concurrentes, y entre la escritura y la lectura del mapa de
  correlacion
- [x] 6.2 Lanzar una rafaga por `POST /` y verificar que el bus contiene, por cada metatx, su
  `relay.received`, su `relay.decoded` y luego su `relay.sent` o su `relay.rejected`, y que no
  aparece ningun `relay.held` ni `relay.turn`
- [x] 6.3 Verificar con `reorder.enabled = false` y `dashboard.enabled = false` que las respuestas
  del camino JSON-RPC son identicas a las del binario anterior, comparando cuerpo y codigo de error
  para un caso exitoso y uno rechazado
- [x] 6.4 Verificar que el servicio arranca y opera con el `config.toml` de produccion sin las
  claves nuevas, y que la unica diferencia observable son las lineas JSON en la salida estandar
- [x] 6.5 Relayar una metatx que el hub rechace, consultar su receipt en una peticion posterior, y
  verificar que el `relay.hub_rejected` resultante queda asociado en el bus a la misma metatx que
  su `relay.received`, su `relay.decoded` y su `relay.sent`
