## 1. Configuracion

- [ ] 1.1 Agregar a `model/Config.go` los bloques `[reorder]`, `[dashboard]` y `[log]` con sus
  campos, y verificar con un test que un `config.toml` sin ninguno de los tres carga y deja cada
  parametro en el default de la tabla del spec
- [ ] 1.2 Aplicar el default y registrar el descarte cuando una clave nueva trae un valor invalido,
  y verificar con tests por caso: `windowMs` negativo, `bufferSize` no numerico y `level` no
  reconocido; en los tres el arranque no aborta
- [ ] 1.3 Sumar las claves nuevas comentadas a `config.toml` como plantilla, y verificar que el
  servicio arranca con el `config.toml` de una instalacion previa sin emitir error ni advertencia

## 2. Bus de eventos

- [ ] 2.1 Crear el paquete `events/` con el ring buffer de capacidad fija y el contador `seq`, y
  verificar con tests que `seq` es estrictamente creciente y que al superar la capacidad se
  conservan los mas recientes sin alterar su `seq`
- [ ] 2.2 Implementar el replay posterior a un `seq` y verificar con tests los tres casos del spec:
  desde un `seq` intermedio, desde uno ya descartado (devuelve todo lo retenido, sin error) y sin
  indicar punto de partida
- [ ] 2.3 Implementar la suscripcion con un canal con buffer por suscriptor y envio no bloqueante
  segun D4, y verificar con un test que un suscriptor que no lee no bloquea la publicacion y que
  los demas siguen recibiendo
- [ ] 2.4 Recuperar el panico dentro de la goroutine de entrega de cada suscriptor, y verificar con
  un test que un suscriptor que entra en panico no tumba el proceso ni impide que los demas
  reciban el mismo evento
- [ ] 2.5 Cortocircuitar `Publish` con una bandera atomica cuando `dashboard.enabled` es `false`,
  y verificar con un benchmark que en ese caso no se toma ningun candado y el costo por evento es
  despreciable frente al camino habilitado

## 3. Log estructurado

- [ ] 3.1 Agregar a `audit/` el emisor de una linea JSON por evento hacia la salida estandar, con
  `warn` y `error` por la de error, y verificar con un test que cada emision produce exactamente
  una linea que parsea como objeto JSON con `ts`, `level`, `event` e `instanceId`
- [ ] 3.2 Generar el `instanceId` una vez por proceso y verificar con un test que todas las lineas
  de un mismo proceso lo comparten
- [ ] 3.3 Aplanar y acotar el texto multilinea de un error antes de emitirlo, y verificar con un
  test que un error con traza de varias lineas sigue produciendo una sola linea JSON valida
- [ ] 3.4 Publicar en el bus antes de aplicar el filtro por `log.level`, y verificar con un test que
  un evento `debug` con el nivel en `info` no se escribe en la salida pero si queda en el bus
- [ ] 3.5 Llamar la inicializacion del emisor desde `main` despues de leer la configuracion, segun
  D5, y verificar que el orden de arranque es leer config, inicializar y recien despues levantar el
  servidor
- [ ] 3.6 Emitir una linea reducida con `ts`, `level`, `event` y la marca del fallo cuando un evento
  no se puede serializar, y verificar con un test que la peticion que lo origino se responde
  normalmente
- [ ] 3.7 Verificar con un test que el log de texto existente conserva su formato, su destino y sus
  entradas, y que ninguno de los dos logs altera el contenido del otro

## 4. Correlacion por peticion y por metatx

- [ ] 4.1 Propagar `context.Context` por las firmas de `controller` y `service` que participan del
  camino de relay, y verificar que `go build ./...` y `go test ./...` siguen en verde sin cambios
  en las aserciones existentes
- [ ] 4.2 Generar el `reqId` en el handler HTTP antes de decodificar el cuerpo, y verificar con un
  test que una peticion con cuerpo ilegible igual emite su evento correlacionado
- [ ] 4.3 Generar el `metaTxId` al entrar al camino de relay y verificar con un test que dos metatx
  distintas no comparten identificador y que sus eventos no se mezclan
- [ ] 4.4 Derivar con `context.WithoutCancel` el contexto de los eventos posteriores a la respuesta
  segun D2, y verificar con un test que un evento emitido tras retornar el handler conserva `reqId`
  y `metaTxId`

## 5. Emision en el camino de relay

- [ ] 5.1 Emitir `relay.received` antes de decodificar, con `rawTxHash` y `rawTxBytes`, y verificar
  con un test que una raw tx malformada tambien deja el evento con su `metaTxId`
- [ ] 5.2 Emitir `relay.decoded` con los campos del contrato y verificar con un test que estan
  todos los que la pagina consume
- [ ] 5.3 Emitir `relay.sent` con los campos del contrato, incluidos los que este servicio aun no
  calcula —`simulated` y `simulatedErrorCodeName`— con un valor interpretable en lugar de omitirlos
- [ ] 5.4 Emitir `relay.rejected` con el `code` del motivo, y verificar con un test que comparte el
  `metaTxId` de los eventos previos de esa metatx
- [ ] 5.5 Agregar un test de contrato que recorra los eventos emitidos y falle si a alguno le falta
  `metaTxId` o cualquier campo de la tabla del spec, para que un renombrado no rompa la pagina en
  silencio

## 6. Verificacion de cierre

- [ ] 6.1 Correr `go test ./... -race` y verificar que pasa, con atencion a las carreras entre la
  publicacion y la suscripcion concurrentes
- [ ] 6.2 Lanzar una rafaga por `POST /` y verificar que el bus contiene, por cada metatx, su
  `relay.received`, su `relay.decoded` y luego su `relay.sent` o su `relay.rejected`, y que no
  aparece ningun `relay.held` ni `relay.turn`
- [ ] 6.3 Verificar con `reorder.enabled = false` y `dashboard.enabled = false` que las respuestas
  del camino JSON-RPC son identicas a las del binario anterior, comparando cuerpo y codigo de error
  para un caso exitoso y uno rechazado
- [ ] 6.4 Verificar que el servicio arranca y opera con el `config.toml` de produccion sin las
  claves nuevas, y que la unica diferencia observable son las lineas JSON en la salida estandar
