## Why

El servicio **pasa el `data` de la metatx sin mirarlo**. El modelo de gas agrega al final dos
palabras -`nodeAddress` y `expiration`- que `service/metatx.go:55` ya decodifica, pero solo para
registrarlas: el comentario del decodificador dice, textual, que validarlas "es una brecha propia,
de otra propuesta". Esta es esa propuesta. Las consecuencias de no validarlas:

- Una metatx dirigida a **otro writer node** se relaya igual y la rechaza el hub, con la
  transaccion de este nodo ya gastada.
- Una metatx **vencida, o al filo de vencer**, se relaya y se descarta despues de gastar una
  transaccion: entre validar, esperar el turno del nonce y minar pasan segundos.

En paralelo, el permisionado del usuario sale de una **direccion fija en `config.toml`**
(`security.accountContractAddress`), no de la cadena. Si el `AccountRules` de la red cambia, el
servicio sigue preguntandole al contrato viejo y contesta que si sobre un allowlist que ya no rige.
Node lo resuelve por `AccountIngress.getContractAddress("rules")` (`src/account-rules.ts:68`), y
ademas chequea al **writer node** al arrancar: un nodo no permitido relaya sin error aparente y
**todas** sus metatx fallan on-chain.

Corresponde a las brechas "validacion del sufijo del gas model" y "`AccountRules` via
`AccountIngress`" de `COMPARACION-GO-NODE.md`, seccion 6. No estan en las fases del
`docs/PLAN-reorden-dashboard.md` -que cubre reordenamiento, endpoints y dashboard- y son las que
quedan del lado de la puerta de entrada una vez cerrada la serie 01-04.

## What Changes

- **Validacion del `nodeAddress` del sufijo**: una metatx cuyo `nodeAddress` embebido no sea el de
  este servicio se rechaza antes de enviarla, con el codigo `WRONG_NODE_ADDRESS` del catalogo.
- **Validacion de la `expiration`**: una metatx ya vencida se rechaza con `EXPIRED`. Una que llega
  con menos ventana que el minimo configurado se rechaza con `EXPIRATION_TOO_LOW`. El minimo se
  aplica **con tolerancia**, porque quien firma `ahora + 300` llega con 298 -se pierde la latencia
  del `POST` y el redondeo a segundos de cada lado- y exigir el valor exacto rechazaria justo al
  cliente que hizo lo correcto.
- **Resolucion del contrato de reglas desde la cadena**: si hay una direccion configurada se usa
  esa -es lo que pasa hoy-, y si no, se resuelve por el `AccountIngress`. Una red sin permisionado
  -sin ingress desplegado, o sin contrato de reglas registrado- simplemente no tiene reglas, y eso
  no es un error.
- **Cache del resultado del permisionado** con vida configurable: hoy cada metatx con el chequeo
  encendido paga una consulta a la cadena. Con cache, dar de alta una cuenta tarda hasta ese plazo
  en verse, que es el intercambio que hace Node.
- **Chequeo del writer node al arrancar**: se consulta si el nodo que relaya esta permitido y se
  informa en `GET /info`. **No aborta el arranque**: la invariante del servicio es que ningun fallo
  externo tumba el proceso, y un nodo no permitido se diagnostica mejor viendolo en `/info` que con
  un binario que no levanta.
- **`GET /info` deja de mentir**: `minExpirationSeconds` y `expirationToleranceSeconds` dejan de ser
  `0` fijo, `accountRulesSource` distingue `config` de `ingress`, y `nodePermitted` informa el
  chequeo real. Las tres filas correspondientes salen de la tabla de diferencias del README.
- **Todo apagado por defecto.** Node valida el sufijo por defecto (`ENFORCE_NODE_ADDRESS` y
  `ENFORCE_EXPIRATION` en `true`); aca el default es `false`, por la invariante del repo: ninguna
  funcionalidad portada se activa sin opt-in explicito. Con los flags apagados, una metatx que hoy
  se relaya se sigue relayando, byte a byte.

Fuera de alcance: el pre-chequeo por simulacion `eth_call` -que produce `SIMULATION_FAILED` y los
campos `simulated` / `errorCode` / `output` de `/relay`-, el passthrough RPC completo con batches y
`eth_subscribe`, y el cliente RPC compartido. Son las tres brechas que quedan despues de esta, cada
una con su propio cambio.

## Capabilities

### New Capabilities

- `metatx-gas-model-validation`: la validacion del sufijo -que se exige del `nodeAddress`, cuando
  una `expiration` es aceptable, como se aplica la tolerancia, y que se responde en cada caso-,
  incluida la garantia de que nada se envia a la cadena cuando la validacion rechaza.
- `relay-account-permissioning`: de donde sale el contrato de reglas -configurado o resuelto por el
  ingress-, que pasa cuando la red no expone ninguno, la vigencia de lo cacheado, el comportamiento
  cerrado ante un allowlist ilegible, y el chequeo del nodo que relaya.

### Modified Capabilities

- `relay-info-endpoint`: `Estado del permisionado` pasa a distinguir de donde salio la direccion del
  contrato de reglas e informar el chequeo real del nodo; `Parametros de operacion vigentes` suma
  los parametros de validacion.
- `relay-runtime-configuration`: se agregan las claves que gobiernan la validacion y la resolucion
  del permisionado, con sus defaults y su efecto.

## Impact

- **Codigo**: `service/metatx.go` (el decodificador del sufijo pasa a tener un camino de
  validacion), `service/prepare.go` (`PrepareMetaTx`, que es la puerta compartida por `POST /` y
  `POST /relay`), `service/relaySignerService.go` (`VerifySender` y la resolucion del contrato de
  reglas), `blockchain/client.go` (lectura del `AccountIngress`), `service/info.go`, `model/Config.go`
  y `config.toml`.
- **APIs**: sin rutas nuevas. Con los flags encendidos cambia lo que se rechaza: metatx que hoy
  llegan a la cadena y fallan alli pasan a rechazarse antes, con su codigo. El cuerpo de `GET /info`
  no cambia de forma; cambian los valores de cuatro campos que hoy son fijos.
- **Compatibilidad**: los codigos nuevos son del catalogo de Node, no inventados. `POST /` sigue
  respondiendo `200` con el error en el cuerpo JSON-RPC, y `POST /relay` `400` con codigo, como ya
  define `relay-sync-relay-endpoint`.
- **Riesgo**: bajo comparado con 04. No toca el camino critico del envio ni la concurrencia; el
  riesgo real es rechazar de mas, y esta acotado por el default apagado y por la tolerancia del
  minimo de expiracion.
- **Depende de**: `02-add-relay-http-endpoints` (la puerta compartida y el catalogo de codigos) y
  `01-add-relay-event-bus` (los campos del sufijo ya se emiten en `relay.decoded`). Es independiente
  de `04-add-nonce-reordering`: puede ir antes o despues.
- **Restriccion heredada**: `go-ethereum v1.9.15`; el `AccountIngress` se lee con una llamada de
  contrato simple, sin bindings nuevos.
