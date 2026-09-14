## Purpose

Emitir una linea JSON por evento, correlacionada por peticion y por metatx, para que la operacion
del RelaySigner se pueda filtrar y reconstruir con herramientas estandar, sin sustituir el log de
texto que los operadores actuales ya parsean.

## ADDED Requirements

### Requirement: Una linea JSON por evento

El sistema SHALL emitir cada evento de operacion como un unico objeto JSON en una sola linea. Cada
linea SHALL incluir al menos los campos `ts` (marca de tiempo ISO 8601 en UTC), `level`, `event` e
`instanceId`.

Los eventos de nivel `warn` y `error` SHALL salir por la salida de error estandar; los de nivel
`debug` e `info`, por la salida estandar.

#### Scenario: Se emite un evento de operacion

- **WHEN** el sistema procesa una peticion que produce un evento
- **THEN** escribe exactamente una linea
- **AND** esa linea es un objeto JSON valido
- **AND** contiene `ts`, `level`, `event` e `instanceId`

#### Scenario: Un evento de error

- **WHEN** el sistema emite un evento de nivel `error`
- **THEN** la linea sale por la salida de error estandar

### Requirement: El contenido nunca parte la linea

El sistema MUST garantizar que un evento ocupe una sola linea, cualquiera sea su contenido. Un
texto multilinea incluido en un campo —como la traza de un error— SHALL aplanarse a una sola linea
y SHALL acotarse en longitud antes de emitirse.

#### Scenario: Un error con traza de varias lineas

- **WHEN** el sistema emite un evento que incluye la traza de un error de varias lineas
- **THEN** la traza se emite como un unico campo de texto en una sola linea
- **AND** la traza se acota a un maximo acotado de fotogramas
- **AND** la linea sigue siendo un objeto JSON valido

### Requirement: Identificador de peticion y de metatx

El sistema SHALL asignar un identificador `reqId` a cada peticion HTTP entrante y SHALL incluirlo
en todos los eventos originados por esa peticion, incluidos los que se emiten despues de haber
respondido al cliente.

El sistema SHALL asignar ademas un identificador `metaTxId` a cada metatx, distinto del `reqId`, y
SHALL incluirlo en todo evento referido a esa metatx.

#### Scenario: Eventos de una misma peticion

- **WHEN** una peticion HTTP produce varios eventos a lo largo de su procesamiento
- **THEN** todos esos eventos comparten el mismo `reqId`

#### Scenario: Un evento posterior a la respuesta

- **WHEN** el sistema emite un evento referido a una metatx despues de haber respondido al cliente
- **THEN** ese evento conserva el `reqId` de la peticion que lo origino
- **AND** conserva el `metaTxId` de esa metatx

#### Scenario: Dos metatx distintas

- **WHEN** el sistema procesa dos metatx distintas
- **THEN** cada una tiene un `metaTxId` propio
- **AND** los eventos de una no se pueden confundir con los de la otra

### Requirement: Identificador de instancia

El sistema SHALL incluir en cada linea un `instanceId` estable durante toda la vida del proceso y
distinto entre procesos, de modo que al leer el log se pueda distinguir si dos instancias
estuvieron atendiendo a la vez. Detectarlo importa porque el estado de nonces vive en memoria del
proceso y asume una unica instancia usando la clave del writer node.

#### Scenario: Dos instancias en el mismo log

- **WHEN** dos procesos del servicio emiten eventos hacia el mismo destino
- **THEN** las lineas de cada proceso llevan un `instanceId` distinto

#### Scenario: Una instancia a lo largo del tiempo

- **WHEN** un mismo proceso emite eventos separados en el tiempo
- **THEN** todas sus lineas llevan el mismo `instanceId`

### Requirement: El nivel de log regula la consola, no la observabilidad

El sistema SHALL permitir configurar un nivel minimo de log que determina que eventos se escriben
en la salida. Ese nivel MUST NOT afectar que eventos se publican para observacion en vivo: un
evento por debajo del nivel configurado SHALL publicarse igualmente en el bus de eventos descrito
por `relay-event-stream`.

#### Scenario: Un evento por debajo del nivel configurado

- **WHEN** el nivel configurado es `info` y el sistema emite un evento de nivel `debug`
- **THEN** ese evento no se escribe en la salida
- **AND** ese evento si se publica en el bus de eventos

### Requirement: La transaccion firmada no se registra por defecto

El sistema MUST NOT incluir la transaccion firmada completa en los eventos salvo que se habilite
explicitamente. En su lugar SHALL incluir siempre su hash y su tamano en bytes, que la identifican
sin volcar su contenido.

#### Scenario: Registro de una metatx con el ajuste por defecto

- **WHEN** el sistema recibe una metatx y emite su evento de recepcion
- **THEN** el evento incluye el hash y el tamano en bytes de la transaccion firmada
- **AND** no incluye la transaccion firmada completa

#### Scenario: Registro con el volcado habilitado explicitamente

- **WHEN** un operador habilita el volcado de la transaccion firmada
- **THEN** el evento de recepcion incluye ademas la transaccion firmada completa

### Requirement: El log de texto existente se conserva

El sistema MUST seguir emitiendo el log de texto actual hacia su destino actual, con el mismo
formato, en paralelo al log estructurado. Las herramientas que hoy parsean ese archivo MUST seguir
funcionando sin cambios.

#### Scenario: Ambos logs conviven

- **WHEN** el sistema procesa una peticion
- **THEN** el log de texto recibe las mismas entradas que recibia antes de esta capacidad
- **AND** el log estructurado recibe la linea JSON correspondiente
- **AND** ninguno de los dos altera el contenido del otro

### Requirement: Un fallo al registrar no afecta a la peticion

El sistema MUST NOT permitir que un fallo al construir, serializar o escribir un evento interrumpa
el procesamiento de la peticion que lo origino. Ante un evento que no se puede serializar, el
sistema SHALL emitir en su lugar una linea reducida que conserve `ts`, `level` y `event` e indique
el fallo.

#### Scenario: Un campo no serializable

- **WHEN** un evento contiene un valor que no se puede serializar a JSON
- **THEN** el sistema emite una linea reducida que indica el fallo de serializacion
- **AND** la peticion se procesa y se responde normalmente
