## 1. Configuracion

- [ ] 1.1 Agregar el bloque `[validation]` con `enforceNodeAddress`, `enforceExpiration`,
  `minExpirationSeconds` y `expirationToleranceSeconds`, leido clave por clave como el resto de los
  bloques nuevos, y verificar con tests que ausente toma los defaults de D3 y que un valor invalido
  se descarta sin habilitar ninguna exigencia
- [ ] 1.2 Agregar `security.accountIngressAddress` y `security.accountRulesCacheMs` sin default para
  la primera, y verificar con un test que un `config.toml` de una instalacion previa arranca igual y
  con las dos exigencias apagadas
- [ ] 1.3 Verificar con un test que una tolerancia mayor que el minimo produce un limite efectivo de
  cero y no uno negativo, segun D8
- [ ] 1.4 Documentar las claves nuevas comentadas en `config.toml`, como plantilla

## 2. Validacion del sufijo del modelo de gas

- [ ] 2.1 Separar en `service/metatx.go` el camino de validacion del de registro, sin tocar lo que
  hoy se emite en `relay.decoded`, y verificar con un test que los campos emitidos no cambian
- [ ] 2.2 Rechazar la metatx cuyo `nodeAddress` del sufijo no es el de este servicio, con
  `WRONG_NODE_ADDRESS`, y verificar con un test que el mensaje indica los dos nodos y que no se
  envia nada a la cadena
- [ ] 2.3 Rechazar la metatx vencida con `EXPIRED`, evaluando contra un solo instante segun D8, y
  verificar con un test que el mensaje informa la expiracion y el instante evaluado
- [ ] 2.4 Rechazar la metatx sin la ventana minima con `EXPIRATION_TOO_LOW`, aplicando el minimo
  menos la tolerancia, y verificar con tests que una al filo dentro de la tolerancia pasa, que una
  por debajo del limite efectivo se rechaza, y que el mensaje informa lo que le quedaba, el minimo y
  la tolerancia
- [ ] 2.5 Agregar los tres codigos al catalogo de `service/prepare.go` y verificar con un test que
  siguen siendo los del catalogo de referencia, sin inventar ninguno
- [ ] 2.6 Ubicar las dos validaciones en `PrepareMetaTx` despues de `relay.decoded` y antes del
  chequeo de permisos, segun D1, y verificar con un test que una metatx rechazada por el sufijo deja
  primero su `relay.decoded` y despues su `relay.rejected` con el mismo `metaTxId`, y que no se
  consulta el contrato de reglas
- [ ] 2.7 Verificar con tests que un sufijo ausente, incompleto o con una expiracion no representable
  NO rechaza y se relaya como antes, segun D2
- [ ] 2.8 Verificar con un test que la misma metatx invalida enviada por `POST /` y por `POST /relay`
  se rechaza por el mismo motivo, cada una con la forma de respuesta de su puerta

## 3. Resolucion del permisionado

- [ ] 3.1 Leer el contrato de reglas del registro de permisos de la red con una llamada de contrato
  simple, sin bindings nuevos, y verificar con un test que se resuelve la direccion publicada
- [ ] 3.2 Aplicar la precedencia de D4 -direccion configurada primero, registro despues- y verificar
  con tests que con direccion configurada el registro no se consulta, que sin ella se usa la
  publicada, y que la fuente informada distingue las dos
- [ ] 3.3 Tratar la red sin permisionado -sin registro, o sin contrato publicado- como ausencia de
  reglas, y verificar con un test que el servicio arranca y relaya con normalidad
- [ ] 3.4 Verificar con un test que una direccion configurada que no corresponde a ningun contrato
  de esa red se registra y no da por permitida ninguna cuenta
- [ ] 3.5 Resolver el registro una sola vez al arrancar, segun D5, y verificar con un test que
  atender varias metatx no repite esa resolucion

## 4. Cache del permisionado

- [ ] 4.1 Cachear por cuenta el resultado del permiso con la vigencia configurada, y verificar con un
  test que dos metatx del mismo sender dentro de la vigencia consultan la cadena una sola vez
- [ ] 4.2 Verificar con un test que pasada la vigencia se vuelve a consultar y que un alta reciente
  se refleja
- [ ] 4.3 Verificar con un test que un fallo al consultar el contrato de reglas rechaza la metatx
  con `PERMISSIONING_UNAVAILABLE` y NO sirve un valor cacheado vencido, segun D7
- [ ] 4.4 Verificar con un test `-race` que la cache soporta consultas concurrentes de varias metatx
  del mismo sender y de senders distintos

## 5. Estado del nodo que relaya

- [ ] 5.1 Comprobar al arrancar si el nodo que relaya esta permitido, cuando hay contrato de reglas,
  y verificar con un test que el resultado queda disponible para `GET /info`
- [ ] 5.2 Verificar con tests que un nodo no permitido y una comprobacion que falla NO impiden el
  arranque, segun D6, y que en el segundo caso el dato se informa sin valor en lugar de afirmar que
  esta permitido
- [ ] 5.3 Registrar el caso del nodo no permitido de forma accionable -que hace falta para darlo de
  alta- y verificar que el mensaje lo dice

## 6. Superficie informada

- [ ] 6.1 Informar en `GET /info` la fuente de la direccion del contrato de reglas y el chequeo real
  del nodo, y verificar con tests los tres casos: configurada, resuelta por el registro, y red sin
  contrato de reglas
- [ ] 6.2 Informar el minimo y la tolerancia como la exigencia vigente -no vigentes con la exigencia
  apagada, segun D3- y verificar con tests los dos estados
- [ ] 6.3 Verificar con un test que la forma del cuerpo de `GET /info` no cambia: los mismos campos,
  con valores que dejan de ser fijos
- [ ] 6.4 Actualizar en el README la tabla de diferencias con el relayer de Node, quitando las filas
  que este cambio cierra, y la lista de codigos producidos

## 7. Verificacion de cierre

- [ ] 7.1 Correr `go test ./... -race` y verificar que pasa
- [ ] 7.2 Verificar contra el binario anterior que, con las dos exigencias apagadas y sin registro
  configurado, las respuestas de `POST /`, `POST /relay`, `GET /info` y `GET /nonce/{address}` son
  identicas, incluidas las de una metatx vencida y una dirigida a otro nodo
- [ ] 7.3 Verificar que el servicio arranca con el `config.toml` de una instalacion previa, con la
  direccion de reglas fija, y que se comporta como hoy sin configurar nada nuevo
- [ ] 7.4 Con las exigencias encendidas contra el servicio en ejecucion, comprobar los cuatro
  rechazos -otro nodo, vencida, ventana insuficiente, allowlist ilegible- y verificar que ninguno
  gasto una transaccion del writer node
- [ ] 7.5 Verificar que los codigos y los mensajes de esos rechazos coinciden campo a campo con los
  del relayer de Node para los mismos casos, segun `ENDPOINTS-GO-VS-NODE.md`
