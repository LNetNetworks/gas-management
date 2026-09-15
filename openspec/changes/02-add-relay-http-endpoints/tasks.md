## 1. Ruteo por path

- [x] 1.1 Registrar las rutas nuevas por su path y comprobar el metodo dentro del handler segun D1,
  y verificar con un test la tabla de esa decision: `POST /info`, `GET /relay` y
  `POST /nonce/{address}` responden `405` y no los atiende el camino JSON-RPC
- [x] 1.2 Registrar `/nonce/` como subarbol y verificar con tests que una peticion sin direccion
  responde `400` y que `/nonce` sin barra final redirige a `/nonce/`
- [x] 1.3 Verificar con un test que ningun path que hoy llega al catch-all dejo de llegar: la raiz,
  un metodo JSON-RPC cualquiera y un path desconocido siguen atendiendose como antes
- [x] 1.4 Verificar que `POST /` responde igual que antes de esta capacidad, comparando cuerpo y
  codigo de error para un caso exitoso y uno rechazado

## 2. Extraccion del decodificado y las validaciones

- [x] 2.1 Extraer a `service/` el camino que va de la raw tx a la metatx lista para enviar
  —decodificar, comprobar la firma, exigir pre-EIP155, resolver el emisor, chequear permisos y
  calcular el limite de gas—, sin que lo extraido escriba respuestas ni conozca HTTP, y verificar
  que `go build ./...` y `go test ./...` siguen en verde
- [x] 2.2 Devolver desde lo extraido un error tipado que cada handler pueda presentar en su propio
  vocabulario, y verificar con un test que del mismo error salen la respuesta JSON-RPC con codigo
  numerico y la respuesta REST con codigo simbolico
- [x] 2.3 Acotar explicitamente el lock del cupo de gas a la reserva y el envio segun D3, en lugar
  de heredarlo de un `defer` a nivel de funcion, y verificar con un test que dos envios concurrentes
  siguen reservando de forma atomica
- [x] 2.4 Hacer que `processRawTransaction` use lo extraido y verificar contra el binario anterior
  que las respuestas del camino JSON-RPC no cambiaron

## 3. `GET /info`

- [x] 3.1 Agregar al cliente la consulta del identificador de cadena y del balance de la cuenta del
  nodo, y verificar con tests contra un nodo simulado que ambos se leen
- [x] 3.2 Responder la identidad y las direcciones —nodo, RelayHub, proxy, cadena y URL del nodo—
  incluido el origen de la direccion del RelayHub, y verificar con un test que aparecen todos
- [x] 3.3 Responder el balance de la cuenta del nodo y el cupo de gas vigente, y verificar con un
  test que salen del nodo y no de la configuracion
- [x] 3.4 Responder el estado del permisionado y verificar con tests los dos casos: deshabilitado
  no consulta el contrato de reglas, y habilitado informa su direccion y si el nodo esta permitido
- [x] 3.5 Responder los parametros de operacion vigentes y verificar con un test que son los que el
  servicio esta usando, y no los del archivo cuando alguna clave fue descartada por invalida
- [x] 3.6 Informar sin valor el campo que no se pudo obtener, en lugar de omitirlo, y verificar con
  un test que una consulta a la cadena que falla deja el resto de la respuesta completa y con el
  codigo de estado de una respuesta correcta

## 4. `GET /nonce/{address}`

- [x] 4.1 Responder el nonce en la cadena y el proximo a usar, cada uno en decimal y hexadecimal, y
  verificar con tests que difieren cuando hay metatx en vuelo y coinciden cuando no las hay
- [x] 4.2 Responder cuantas metatx de esa direccion estan en vuelo, y verificar con un test que el
  valor refleja lo que el servicio relayo y todavia no resolvio
- [x] 4.3 Aceptar el parametro de consulta sin reserva segun D7, y verificar con un test que la
  respuesta es la misma con y sin el, y que un valor no reconocido se trata como no pedido
- [x] 4.4 Rechazar con `400` una direccion ausente o mal formada sin consultar la cadena, y
  verificar con tests que la misma direccion en minusculas y con mayusculas de checksum devuelve
  los mismos nonces
- [x] 4.5 Verificar con un test que el proximo nonce que informa esta ruta es el mismo que responde
  `eth_getTransactionCount` en estado pendiente para la misma direccion

## 5. `POST /relay`

- [x] 5.1 Aceptar la transaccion firmada bajo su nombre principal y bajo el alias, y verificar con
  tests que un cuerpo sin transaccion o con algo que no es hexadecimal responde `400` indicando lo
  que se esperaba, sin consultar la cadena
- [x] 5.2 Relayar usando lo extraido en la seccion 2, y verificar con un test que una metatx que
  `POST /` rechaza se rechaza aqui por el mismo motivo
- [x] 5.3 Esperar el resultado por sondeo, fuera del lock del cupo de gas segun D3, y verificar con
  un test que varias esperas simultaneas no se bloquean entre si
- [x] 5.4 Responder el resultado decodificado de la metatx con los campos del spec, tomando la
  direccion desplegada del evento del hub y no del receipt, y verificar con tests una llamada a un
  contrato y un deploy
- [x] 5.5 Responder con el codigo de una respuesta correcta cuando el contrato destino revierte,
  informando que no se ejecuto y el motivo, y verificar con un test que no se confunde con un
  rechazo del relay
- [x] 5.6 Informar el codigo de error del hub y su nombre cuando el hub rechaza la metatx, y
  verificar con un test que usa la misma traduccion del enum que ya existe
- [x] 5.7 Responder `400` con motivo, codigo y detalle usando el catalogo de D5, y verificar con
  tests un rechazo por transaccion indecodificable, uno por sender no permitido y uno sin codigo
  propio que caiga en el generico
- [x] 5.8 Responder el vencimiento de la espera con su codigo propio y el hash de la metatx, y
  verificar con un test que se distingue de un rechazo y que la metatx sigue su curso
- [x] 5.9 Verificar con un test que una metatx relayada por esta ruta emite los mismos eventos de
  operacion que por el camino JSON-RPC, con la misma correlacion por peticion y por metatx

## 6. Verificacion de cierre

- [x] 6.1 Correr `go test ./... -race` y verificar que pasa, con atencion a las esperas concurrentes
  de `POST /relay` y al lock del cupo de gas
- [x] 6.2 Verificar que mientras varias peticiones a `POST /relay` esperan su receipt, el camino
  JSON-RPC sigue respondiendo con normalidad
- [ ] 6.3 Verificar contra el binario anterior que las respuestas de `POST /` son identicas,
  comparando cuerpo y codigo de error para un caso exitoso y uno rechazado
- [ ] 6.4 Verificar que el servicio arranca con el `config.toml` de una instalacion previa y que las
  rutas nuevas responden sin que haga falta configurar nada
- [ ] 6.5 Comparar campo a campo la respuesta de las tres rutas contra la del relayer de Node, y
  documentar en el README las diferencias que queden y por que
