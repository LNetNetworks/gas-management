## Context

Ver `proposal.md` para la motivacion y `specs/` para los requisitos. Lo que sigue son las
decisiones que impone el codigo actual.

Cuatro restricciones condicionan todo lo demas:

- **Hay una sola ruta y es un catch-all.** `main.go` registra `mux.HandleFunc("/", ...)`, que
  atiende cualquier path. Cualquier ruta nueva convive con eso, y el catch-all no puede perder
  ninguna peticion que hoy atiende.
- **El decodificado y las validaciones viven en el controller.** `processRawTransaction` decodifica
  la raw tx, comprueba la firma, exige pre-EIP155, chequea permisos y verifica el cupo de gas, todo
  entrelazado con escribir la respuesta JSON-RPC. `POST /relay` necesita exactamente esas
  validaciones pero otra respuesta.
- **El cupo de gas por bloque se reserva bajo un lock global.** `processRawTransaction` toma `lock`
  antes de `VerifyGasLimit` y lo libera con `defer` al terminar la funcion, o sea que lo retiene
  durante el envio. Es lo que hace atomica la reserva frente a otros envios.
- **Nadie espera receipts.** El servicio manda y responde el hash; quien quiera el resultado
  vuelve a preguntar. No hay ningun mecanismo de espera que reutilizar.

Go es 1.23.0 y `01-add-relay-event-bus` ya dejo `context.Context` en las firmas del camino de
relay, el bloque `[reorder]` en la configuracion y el `reqId` por peticion.

## Goals / Non-Goals

**Goals:**

- Que las rutas nuevas y el catch-all convivan sin que `POST /` pierda una sola peticion.
- Que las dos puertas compartan el mismo decodificado y las mismas validaciones, no dos copias.
- Que esperar un receipt no serialice el servicio ni retenga el cupo de gas.
- Que un cliente escrito contra el relayer de Node funcione contra estas rutas sin cambios.

**Non-Goals:**

- Autenticacion o control de acceso: el servicio no tiene y esto no lo introduce.
- Exponer las rutas por el puerto 80; eso exige tocar la plantilla de nginx y va aparte.
- El passthrough crudo, los batches JSON-RPC y `eth_subscribe`, que siguen fuera de alcance.
- Volver autoritativos `nextNonce` y `pending`: eso es `04-add-nonce-reordering`.

## Decisions

### D1. Las rutas se registran sin metodo, y el metodo se comprueba adentro

`http.ServeMux` acepta desde Go 1.22 patrones con metodo (`"GET /info"`) y comodines
(`"GET /nonce/{address}"`), que serian la forma natural. **Con un catch-all no sirven.** Medido:

| Peticion | Con patrones `"GET /info"` | Registrando el path y comprobando adentro |
|---|---|---|
| `POST /` | catch-all | catch-all |
| `POST /eth_call` | catch-all | catch-all |
| `GET /info` | `/info` | `/info` |
| `POST /info` | **catch-all, 200** | **405** |
| `GET /relay` | **catch-all, 200** | **405** |
| `GET /nonce/` | **catch-all, 200** | **400** |
| `POST /nonce/0xAbC` | **catch-all, 200** | **405** |

`ServeMux` devuelve 405 solo cuando ninguna otra ruta coincide. Como `/` coincide con todo, el
metodo equivocado nunca produce 405: se lo lleva el catch-all y el cliente recibe un 200 con una
respuesta JSON-RPC que no pidio. El requisito del spec —405, y no tratarlo como JSON-RPC— no se
cumple con la forma natural.

Asi que cada ruta se registra por su path (`"/info"`, `"/relay"`, `"/nonce/"`) y el handler
comprueba el metodo. Es mas codigo que el patron, pero es el unico que cumple el requisito sin
sacar el catch-all del medio.

Alternativa descartada: un router de terceros. Resuelve el 405 de fabrica, pero agrega una
dependencia al servicio para un problema que se resuelve con una funcion de cuatro lineas.

### D2. `/nonce/` se registra como subarbol y la direccion se lee del path

El comodin `{address}` no coincide con `/nonce/` a secas, asi que una peticion sin direccion caeria
al catch-all en lugar de dar el 400 que pide el spec. Registrando el subarbol `"/nonce/"`, todas
las variantes llegan al handler y la direccion se valida ahi: ausente o mal formada es `400`.

`GET /nonce` sin barra final lo redirige `ServeMux` con un 301 a `/nonce/`. Es el comportamiento
estandar y no hace falta tocarlo.

### D3. La espera del receipt es por sondeo, y fuera del lock del cupo de gas

Dos formas de esperar el resultado:

| Opcion | Veredicto |
|---|---|
| Sondear el receipt con un plazo | **elegida** |
| Colgarse de la suscripcion a bloques que ya existe | descartada para este change |

La suscripcion de `ProcessNewBlocks` parece el lugar natural, pero hoy solo resetea el cupo de gas
por bloque: no sabe que metatx hay esperando ni tiene a quien avisarle. Construir ese registro es
el watcher de receipts de `04-add-nonce-reordering`, y hacerlo aca seria adelantar su diseño con
menos informacion. El sondeo es simple, se corta solo con el `ctx`, y cuando exista el watcher esta
ruta puede pasar a usarlo sin cambiar su contrato.

**Lo que no es negociable es donde se espera.** `processRawTransaction` toma el lock del cupo de gas
antes de verificarlo y lo suelta con `defer` al terminar la funcion. Si la espera del receipt queda
dentro de ese alcance, un solo `POST /relay` bloquea a todos los demas envios durante el plazo
completo —hasta un minuto con el default— y convierte el servicio en secuencial.

```
   reservar cupo    enviar      [ liberar lock ]      esperar receipt      responder
   -------------------------------------------->  |  ------------------------------->
          bajo el lock global                      |     fuera del lock
```

La espera va despues de soltar el lock, sobre el hash ya obtenido. Eso obliga a que la funcion
extraida en D4 acote el lock explicitamente en vez de heredarlo de un `defer` a nivel de funcion.

### D4. Las validaciones se extraen a `service/` con una frontera clara

`POST /` y `POST /relay` tienen que aceptar y rechazar exactamente las mismas metatx. La unica
forma de garantizarlo es que compartan el codigo, no que lo repitan.

Se extrae de `processRawTransaction` todo lo que va desde la raw tx hasta tener la metatx lista
para enviar: decodificar, comprobar la firma, exigir pre-EIP155, resolver el emisor, chequear
permisos y calcular el limite de gas. La frontera es deliberada: lo extraido **no escribe
respuestas ni conoce HTTP**, devuelve un resultado o un error tipado, y cada handler decide como
presentarlo —JSON-RPC en un caso, JSON REST en el otro—.

El envio y la reserva del cupo quedan tambien del lado de `service/`, porque son los que necesitan
el lock acotado de D3.

Para `POST /` esto es un refactor sin cambio de comportamiento, y asi hay que verificarlo: las
respuestas de ese camino no cambian ni en su cuerpo ni en su codigo de error. El beneficio aguas
abajo es que `04-add-nonce-reordering` se interpone en un solo lugar en vez de dos.

### D5. Los codigos de `POST /relay` son los del catalogo de Node, y no reemplazan a los numericos

`POST /` responde errores JSON-RPC con codigo numerico (`-32012`, `-32602`). El relayer de Node
responde en `POST /relay` un codigo simbolico (`BAD_RAW_TX`, `SENDER_NOT_PERMITTED`). Son dos
contratos distintos y **los dos se conservan**: cada puerta responde en el vocabulario que su
cliente espera, a partir del mismo error interno.

El catalogo de Node tiene trece codigos. Este servicio solo puede producir los que corresponden a
validaciones que tiene:

| Codigo | Cuando |
|---|---|
| `BAD_RAW_TX` | la transaccion no se puede decodificar |
| `BAD_META_TX` | la firma no sirve, o no es pre-EIP155 |
| `SENDER_NOT_PERMITTED` | el chequeo de permisos esta habilitado y el sender no pasa |
| `PERMISSIONING_UNAVAILABLE` | no se pudo consultar el contrato de reglas |
| `SEND_FAILED` | el envio al hub fallo |
| `RECEIPT_TIMEOUT` | se envio y el resultado no llego dentro del plazo |
| `RELAY_ERROR` | cualquier otro motivo, incluido el cupo de gas excedido |

Los que quedan sin producir —`BAD_NONCE`, `EXPIRED`, `EXPIRATION_TOO_LOW`, `WRONG_NODE_ADDRESS`,
`TOO_MANY_INFLIGHT`, `SIMULATION_FAILED`, `NO_RECEIPT`— corresponden a validaciones que este
servicio no tiene (expiracion, direccion del nodo, simulacion previa) o al tracker de
`04-add-nonce-reordering`. No se inventan codigos nuevos: lo que no tiene codigo propio usa
`RELAY_ERROR` y explica el motivo en el texto.

El cupo de gas excedido es el caso incomodo: es un rechazo propio de este servicio que Node no
tiene. Va como `RELAY_ERROR` en vez de estrenar un codigo, porque agregar uno al catalogo divergiria
de la fuente justo donde el objetivo es converger.

### D6. `GET /info` informa sin valor lo que no puede obtener

Dos campos de la respuesta de Node no existen hoy en este servicio y son consultas triviales al
nodo: el identificador de la cadena y el balance de la cuenta del nodo. Se agregan al cliente.

Otros no aplican y se informan sin valor o con el valor de esa capacidad apagada, nunca omitidos:
los de expiracion —este servicio no la valida— y los de reserva de nonces, que no existe. Omitirlos
obligaria a quien consume la respuesta a distinguir "no se pudo" de "esta version no lo informa".

Una consulta a la cadena que falla no rompe la respuesta: ese campo va sin valor y el resto llega.
`GET /info` es la ruta a la que se acude **cuando algo anda mal**, asi que no puede ser la primera
en caerse.

### D7. `peek` se acepta desde ahora aunque todavia no haga diferencia

Este servicio no reserva nonces: consultar el proximo no tiene efecto sobre nadie. El parametro se
acepta igual y se documenta que hoy no cambia la respuesta, porque el contrato de la ruta se fija
en este change y un cliente escrito contra Node lo manda. Cuando `04-add-nonce-reordering` traiga
la reserva, el parametro cobra efecto sin cambiar la forma de la respuesta ni romper a nadie.

## Risks / Trade-offs

| Riesgo | Mitigacion |
|---|---|
| Extraer las validaciones cambia el comportamiento de `POST /` sin que se note | Los tests de contrato de `01` ya fijan las respuestas del camino JSON-RPC; se agrega la comparacion contra el binario anterior para un caso exitoso y uno rechazado, como en `01` |
| Una ruta nueva se traga peticiones que hoy atiende el catch-all | Test que recorre los paths que hoy llegan al JSON-RPC y verifica que siguen llegando, incluido `/eth_call` y cualquier path desconocido |
| `POST /relay` retiene el cupo de gas mientras espera y serializa el servicio | La espera va fuera del lock (D3), verificado con un test de varias esperas simultaneas mientras `POST /` sigue respondiendo |
| El sondeo del receipt castiga al nodo con una consulta por metatx en vuelo | Intervalo acotado y plazo maximo por configuracion; el numero de metatx en vuelo lo acota el cupo de gas por bloque |
| Un cliente trata un `RECEIPT_TIMEOUT` como rechazo y reenvia, produciendo un nonce repetido | Codigo propio, distinto del rechazo, y el hash en la respuesta para que pueda consultarla en vez de reenviarla |
| `/info` expone direcciones y balance sin autenticacion | No cambia respecto del resto del servicio, pero se documenta en el despliegue: estas rutas no son alcanzables por el puerto 80 |

## Migration Plan

No hay migracion de datos ni de formato. El despliegue es reemplazar el binario:

1. `POST /` se comporta igual, con la misma configuracion. Un cliente existente no nota nada.
2. Las rutas nuevas quedan disponibles en el puerto del servicio. El nginx del writer node enruta
   por metodo leyendo el cuerpo, asi que **no son alcanzables por el puerto 80** hasta que se toque
   esa plantilla, que va como PR aparte en `besu-networks`.
3. Rollback: reinstalar el binario anterior. No hay estado persistido que revertir.

## Open Questions

- **Intervalo de sondeo del receipt.** El plazo maximo ya es configurable; cada cuanto se pregunta
  dentro de ese plazo se puede elegir bien recien midiendo contra un nodo real con carga. Se arranca
  con un valor conservador y no afecta a ningun requisito.
