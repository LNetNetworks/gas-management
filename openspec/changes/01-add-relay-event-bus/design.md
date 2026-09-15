## Context

Ver `proposal.md` para la motivacion y `specs/` para los requisitos. Lo que sigue son las
decisiones tecnicas que el paso de Node a Go obliga a tomar, porque el mecanismo de Node no tiene
equivalente directo.

Tres restricciones del codigo actual condicionan todo lo demas:

- **No hay `context.Context` en ninguna firma.** Los metodos de `service` reciben el `id` del
  JSON-RPC y nada mas; el handler HTTP no propaga nada. Node resuelve la correlacion con
  `AsyncLocalStorage`, que en Go no existe.
- **`audit/log.go` se inicializa en `init()`**, antes de que `main` lea `config.toml`. El emisor
  estructurado necesita configuracion (capacidad del bus, nivel), asi que no puede depender del
  mismo mecanismo.
- **Go corre el relay en una goroutine por request.** En Node los suscriptores del bus se invocan
  en linea porque el event loop no bloquea; en Go, invocar en linea a un suscriptor que escribe a
  un socket pone I/O de red dentro del camino de la metatx.

Go es 1.23.0, asi que `context.WithoutCancel` esta disponible.

## Goals / Non-Goals

**Goals:**

- Correlacionar todos los eventos de una peticion y de una metatx, incluidos los que se emiten
  despues de haber respondido al cliente.
- Que el bus no pueda afectar al camino de la metatx, ni por lentitud, ni por error, ni por panico.
- Que con el dashboard apagado el costo sea efectivamente nulo, no solo pequeno.
- Dejar el terreno preparado para los eventos que emiten los changes 03 y 04 sin volver a tocar
  esta capa.

**Non-Goals:**

- Sustituir el log de texto actual, ni cambiar su formato o su destino.
- Exponer el bus por HTTP: eso es `03-add-relay-dashboard`.
- Persistir eventos, agregarlos entre procesos o sobrevivir a un reinicio.

## Decisions

### D1. La correlacion viaja por `context.Context`, no por un almacenamiento implicito

Go no tiene `AsyncLocalStorage`. Las opciones reales eran:

| Opcion | Veredicto |
|---|---|
| `context.Context` como primer parametro | **elegida** |
| Un logger por peticion pasado como parametro explicito | funciona, pero es un `context` peor y sin cancelacion |
| Recuperar el id de la goroutine | descartada: fragil, no soportada, y se rompe en cuanto se cruza una goroutine |

Se agrega `ctx context.Context` a las firmas de `controller` y `service` que participan del camino
de relay. Es un cambio mecanico y amplio, pero paga dos veces: `02-add-relay-http-endpoints`
necesita propagar un timeout para que `POST /relay` espere el receipt, y `04-add-nonce-reordering`
necesita cancelacion para la ventana del buffer. Hacerlo aca, en un change sin cambio de
comportamiento y con los tests existentes como red, es mas barato que hacerlo dentro del change que
toca el camino critico.

### D2. Los eventos posteriores a la respuesta usan un contexto desacoplado de la cancelacion

El `ctx` de una peticion HTTP se cancela cuando el handler retorna. El watcher de receipts del
change 04 emite `relay.settled` mucho despues, y el spec exige que ese evento conserve el `reqId` y
el `metaTxId` de la metatx que lo origino.

Se deriva ese contexto con `context.WithoutCancel(ctx)`: conserva los valores, descarta la
cancelacion y el deadline. La alternativa —copiar los ids a una estructura propia y pasarla— vuelve
a inventar el `context` con otro nombre.

Esto cubre la goroutine que se desprende **dentro de la misma peticion**. No cubre el caso de este
servicio, donde el receipt llega en una peticion HTTP distinta, iniciada por el cliente: ahi no hay
`ctx` del que derivar. Ese salto lo resuelve D11.

### D3. El emisor estructurado publica en el bus; el bus no conoce al emisor

La dependencia va en un solo sentido: `audit/` importa `events/`, nunca al reves. Es lo que hace
cumplible el requisito de equivalencia entre log y bus sin que el codigo que origina el evento
tenga que saber que existe un dashboard, y lo que evita el ciclo de importacion.

Consecuencia practica: no existe una API para publicar en el bus directamente. El unico camino es
registrar el evento.

### D4. La entrega a suscriptores es asincronica, con descarte del cliente lento

Es la decision que mas se aparta de Node, y la que el requisito "la observacion nunca es causa de
un fallo" obliga a tomar.

```
   camino de la metatx                          fuera del camino
   ------------------                           ----------------

   registrar evento
        |
        v
   +----------+   copia    +--------------+
   | ring buf |<-----------|  Publish     |
   +----------+            +------+-------+
                                  |  envio no bloqueante
                    +-------------+-------------+
                    v             v             v
                 [ ch 1 ]      [ ch 2 ]      [ ch N ]   <- un canal con buffer por suscriptor
                    |             |             |
                    v             v             v
                 goroutine     goroutine     goroutine  <- aca vive el I/O, y aca se recupera
                                                           cualquier panico
```

Cada suscriptor recibe un canal con buffer propio. `Publish` intenta un envio no bloqueante
(`select` con `default`): si el canal esta lleno, **descarta el evento para ese suscriptor** y
continua. Nunca espera.

El trade-off es explicito: un observador lento pierde eventos en vez de frenar el relay. Es
aceptable porque el `seq` es monotonico y el replay permite detectar el hueco y recuperarlo al
reconectar — y porque la alternativa es que un navegador con una pestana congelada retenga una
metatx.

Alternativa descartada: llamada sincronica con `recover`, como Node. Protege del panico pero no de
la lentitud, que es el riesgo real en un modelo con goroutines.

### D5. Inicializacion explicita, no `init()`

`audit.InitStructured(cfg)` se llama desde `main` despues de leer `config.toml`. Hasta ese momento
el bus esta inerte y el emisor estructurado usa defaults conservadores.

`init()` no sirve porque corre antes de que exista la configuracion, y una inicializacion perezosa
con `sync.Once` esconde el orden justo donde importa: si un evento sale antes de `InitStructured`,
queremos que sea evidente, no que quede capturado con una capacidad equivocada.

### D6. El bus apagado se comprueba sin tomar el candado

Con `dashboard.enabled = false`, `Publish` consulta una bandera atomica y retorna antes de tocar el
mutex, el buffer o la lista de suscriptores. El requisito es que no cueste nada por linea de log,
no que cueste poco: tomar un mutex por evento en el camino de la metatx es exactamente lo que hay
que evitar.

### D7. Los numeros van como numeros JSON

Node convierte `bigint` a texto porque `JSON.stringify` no lo soporta. En Go no hace falta: un
`uint64` serializa como numero JSON y la pagina lo lee como `Number` de JavaScript.

Los valores en juego —nonce del hub, nonce del writer node, numero de bloque, gas, expiracion en
segundos unix— estan varios ordenes de magnitud por debajo de 2^53, donde un `double` deja de
representar enteros exactamente. No hay riesgo de perdida de precision, y emitir texto obligaria a
tocar la pagina portada, que es justo lo que el contrato de eventos evita.

### D8. Los identificadores se generan donde estan sus limites

- `reqId`: en el handler HTTP, una vez por peticion, antes de decodificar el cuerpo. Asi una
  peticion con un cuerpo ilegible tambien queda correlacionada.
- `metaTxId`: al entrar al camino de relay, no en el handler. Es lo que permite que una peticion
  que trae mas de una metatx —hoy no ocurre, pero el contrato de Node lo contempla— no mezcle los
  eventos de una con los de otra.
- `instanceId`: una vez por proceso, al arrancar.

`relay.received` se emite **antes** de decodificar, para que una raw tx malformada deje rastro con
su `metaTxId` en lugar de desaparecer.

### D9. El nivel de log y el volcado de la raw tx se configuran en `config.toml`

Node los toma de `LOG_LEVEL` y `LOG_RAW_TX`. Aca la configuracion vive en `config.toml`, asi que
se agrega un bloque `[log]` con `level` (default `info`) y `rawTx` (default `false`), en lugar de
introducir variables de entorno que serian el unico caso del servicio.

El filtrado por nivel ocurre **despues** de publicar en el bus, nunca antes: es la unica forma de
cumplir el requisito de que el nivel regule la consola y no la observabilidad.

### D10. Los bloques nuevos se leen clave por clave, fuera del `Unmarshal` compartido

`main.go` carga toda la configuracion con un unico `v.Unmarshal(&c)` y aborta con `os.Exit(1)` si
falla. Eso choca de frente con el requisito de que un valor invalido en una clave nueva no impida
el arranque. Medido contra viper v1.13.0, la version del repo:

| Intento | Resultado |
|---|---|
| `Unmarshal` global, `bufferSize = "muchos"` | falla el decode **completo**, no solo esa clave: aborta |
| `Unmarshal` global + `SetDefault` | identico; el default no interviene en un error de tipo |
| `v.GetInt(clave)` | devuelve `0` en silencio: ni el default ni el aviso |
| `cast.ToIntE(v.Get(clave))` | devuelve el cero **y** el error, con la clave nombrada |

Los bloques `[reorder]`, `[dashboard]` y `[log]` no entran al struct que pasa por `v.Unmarshal`.
Una funcion aparte los lee clave por clave:

```
   !v.IsSet(clave)              -> default, en silencio
   cast.To*E(v.Get(clave)) err  -> default + se registra la clave descartada   (error de TIPO)
   fuera de rango               -> default + se registra la clave descartada   (error de RANGO)
```

Tres consecuencias que importan:

- **Tipo y rango son mecanismos distintos.** `bufferSize = "muchos"` es un fallo de casteo;
  `windowMs = -1` viper lo acepta sin objetar y decodifica a `-1`. El segundo caso solo existe si
  se escribe la validacion de rango; no sale gratis del decodificador.
- **Las claves existentes no se tocan.** Siguen por el `Unmarshal` de siempre y siguen abortando
  como siempre. El requisito de compatibilidad exige exactamente eso.
- **No se usa `SetDefault` para estas claves.** Envenena `IsSet`: con un default declarado,
  `IsSet` devuelve `true` aunque la clave este ausente, y se pierde la unica forma de distinguir
  ausente de presente.

Esa distincion es la que permite respetar la semantica de Node para `dashboard.bufferSize`: ausente
toma el default de 500, y un `0` explicito apaga el bus, igual que `DASHBOARD_BUFFER=0` alla. Evita
repetir el "0 o ausente = default" que el repo ya arrastra en `nonceCacheTTL`, donde las dos cosas
son indistinguibles.

### D11. La correlacion del cierre cruza peticiones: un mapa `txHash` -> contexto de la metatx

En Node los eventos de cierre nacen dentro del `withLogContext` de la metatx, asi que heredan el
`metaTxId` sin que nadie lo pase. En Go no hay tal cosa: el receipt se procesa en una peticion HTTP
**distinta**, la que el cliente hace con `eth_getTransactionReceipt` o `relay_getMetaTxResult`, y
manana tambien en el watcher del change 04. Sin un puente, esos eventos salen sin `metaTxId` y la
pagina los descarta en silencio.

```
   POST /  (eth_sendRawTransaction)          reqId=A  metaTxId=X
     relay.received / relay.decoded              |
     se envia al hub -> txHash H                 |
     recordar(H, {reqId:A, metaTxId:X})          |
     relay.sent  (transactionHash = H)           |
     [responde el hash y el handler retorna]     |
                                                 v
   ...despues, por cualquiera de los tres caminos...
                                          +------------------+
   POST / (eth_getTransactionReceipt) --> |   recordar(H)    | --> {reqId:A, metaTxId:X}
   POST / (relay_getMetaTxResult)     --> |   TTL + tope     |            |
   watcher de bloques (change 04)     --> +------------------+            v
                                                        relay.settled / relay.hub_rejected
                                                        con el reqId y el metaTxId ORIGINALES
```

Se guarda al enviar y se consulta al ver un receipt. El contexto de esos eventos no se deriva de la
peticion en curso: se rehidrata desde el mapa, de modo que el evento conserva el `reqId` de la
peticion que relayo la metatx, como exige el spec, y no el de la que fue a buscar el receipt.

Es el mismo patron que el cache de nonces ya usa en `service/`: mapa en memoria del proceso, lock
propio, TTL por entrada. Tiene ademas un tope de entradas, para que una rafaga no lo haga crecer
sin limite. Una entrada que expira no rompe nada: cuesta un evento de cierre sin `metaTxId`, que
es el comportamiento que habria sin el mapa.

El TTL y el tope son constantes del codigo, no claves de configuracion. `reorder.receiptTimeoutMs`
pertenece al change 04 y el mapa tiene que funcionar con `reorder.enabled = false`.

Alternativa descartada: derivar el `metaTxId` del `rawTxHash` para no necesitar estado. No alcanza
—el receipt se busca por el hash de la tx del writer node, que no es el `rawTxHash` del usuario,
asi que el mapa haria falta igual— y de paso fusionaria los reintentos de una misma raw tx en una
sola metatx, divergiendo de Node sin necesidad.

### D12. Los campos de error del evento salen de donde sale la respuesta

El equivalente del `errorFields` de Node se escribe en `audit/` y produce tres campos:

| Campo | De donde sale | Presencia |
|---|---|---|
| `error` | `err.Error()`, aplanado y acotado | siempre |
| `code` | `err.(rpc.Error).ErrorCode()`, la misma fuente que `rpc/json.go` usa para armar la respuesta JSON-RPC | cuando el error lo trae |
| `errorType` | el nombre del enum `ErrorType` de `errors/` | cuando el error es un `customError` |

`code` sale de la misma fuente que la respuesta al cliente a proposito: el log y la respuesta no
pueden contar cosas distintas sobre el mismo rechazo. Es el mismo principio de "una sola verdad"
que rige la relacion entre el log y el bus (D3).

`errorType` existe porque el `code` de este servicio es un numero JSON-RPC (`-32010`, `-32602`),
mientras que el de Node es un nombre simbolico (`BAD_RAW_TX`). El enum de `errors/` es el analogo
real de esos nombres, y sin el se pierde la posibilidad de filtrar el log por motivo.

**Lo que este change NO hace:** `errors.Wrapf` y `errors.AddErrorContext` reconstruyen el error sin
copiar el `errorCode`, asi que un error envuelto pierde su codigo. Es un defecto real, pero ese
mismo valor es el que viaja en la respuesta JSON-RPC al cliente: corregirlo cambiaria codigos de
error que los integradores ya reciben, y este change no cambia comportamiento observable. El evento
va a mostrar el mismo codigo degradado que la respuesta, que es lo correcto mientras el defecto
exista. Corregirlo es un change aparte.

### D13. Los campos que Go todavia no calcula

Tres campos del contrato no tienen fuente en el codigo actual, y cada uno se resuelve distinto:

- **`writerNodeNonce`**: `blockchain/client.go` arma el `*types.Transaction` y devuelve solo el
  hash, descartando el nonce. Pasa a devolver la transaccion, y el caller toma `Hash()` y
  `Nonce()`. Es lo que menos ensucia la firma y de paso le da a D11 el hash en el mismo punto
  donde tiene que recordarlo. El caller debe seguir respondiendo el hash, no la transaccion: la
  respuesta de `eth_sendRawTransaction` no cambia.
- **`nodeAddress`, `expiration`, `expiresInSeconds` y `selector`**: salen del sufijo del gas model,
  los ultimos 64 bytes del `data` —`[12 ceros][20 address][32 uint256]`—, que este servicio nunca
  decodifica. Se decodifica **solo para registrar, nunca para validar**: un `data` mas corto que el
  sufijo emite los cuatro campos en `null` y la metatx sigue su curso. Node si rechaza en ese caso,
  pero tiene una validacion de forma que Go no tiene; convertir el parseo en un rechazo nuevo haria
  que este change cambie comportamiento observable. La validacion del sufijo es una brecha propia,
  para otra propuesta.
- **`pendingForUser`**: cuenta las metatx en vuelo por usuario, que es estado del tracker de
  reordenamiento del change 04. Se emite `null`. La pagina no lo consume.

## Risks / Trade-offs

| Riesgo | Mitigacion |
|---|---|
| Agregar `ctx` a las firmas toca mucho codigo y puede colar un cambio de comportamiento | El cambio es mecanico y no altera logica; los tests existentes sobre `service` son la red. Va en un change sin cambio observable, no junto al reordenamiento |
| Un observador lento pierde eventos | Aceptado a proposito (D4). El `seq` monotonico deja el hueco visible y el replay lo cierra al reconectar |
| Un panico en la goroutine de un suscriptor mata el proceso | `recover` en cada goroutine de entrega. Es requisito de spec, no una precaucion opcional |
| El contrato de eventos se desincroniza de la pagina portada | Test que valida, por evento, que estan los campos que la pagina consume. `metaTxId` es el caso critico: sin el, la pagina descarta el evento en silencio |
| El bus inerte igual cuesta | La bandera atomica se consulta antes de cualquier candado (D6); medible con un benchmark del camino de relay con el flag en ambos valores |
| Un evento emitido antes de `InitStructured` se pierde | Orden explicito en `main`: leer config, inicializar, y recien despues levantar el servidor |
| Duplicar el logging duplica la escritura a disco | El log estructurado va a la salida estandar, no al archivo; el archivo de texto conserva su volumen actual |
| El mapa de correlacion de D11 crece sin limite o retiene memoria | TTL por entrada y tope de entradas, como el cache de nonces. Una entrada vencida cuesta un evento de cierre sin `metaTxId`, no una fuga |
| Cambiar la firma de `SendMetatransaction` altera la respuesta de `eth_sendRawTransaction` | El caller pasa a responder `tx.Hash()`. Test de contrato sobre el cuerpo de la respuesta, en el caso exitoso y en el rechazado |
| Decodificar el sufijo del gas model se vuelve una validacion nueva y rechaza metatx que hoy pasan | Se decodifica solo para registrar (D13): sufijo ausente o corto emite `null`, nunca rechaza |
| Un evento de cierre sale sin `metaTxId` y la pagina lo descarta en silencio | Es el modo de fallo de D11 cuando la entrada vencio. El test de contrato cubre el camino completo enviar -> receipt en otra peticion |

## Migration Plan

No hay migracion de datos ni de formato. El despliegue es reemplazar el binario:

1. Con el `config.toml` existente, sin claves nuevas, el servicio arranca con ambas capacidades
   apagadas y se comporta como antes. El log de texto sigue en su archivo.
2. La unica diferencia observable es la aparicion de lineas JSON en la salida estandar, que
   journald captura. Un operador que hoy no mira la salida estandar no nota nada.
3. Rollback: reinstalar el binario anterior. No hay estado persistido que migrar ni revertir, y el
   `config.toml` con las claves nuevas sigue siendo valido para el binario viejo, que las ignora.

## Open Questions

- **Capacidad del canal por suscriptor.** El descarte del cliente lento (D4) depende de ese tamano,
  pero el valor solo se puede elegir bien midiendo contra el dashboard real, que llega en el change
  03. Se arranca con un valor razonable y se ajusta ahi; no afecta a ningun requisito.
