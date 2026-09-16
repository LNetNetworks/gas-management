# relay-runtime-configuration Specification

## Purpose
Gobernar desde `config.toml` las capacidades nuevas del RelaySigner —reordenamiento de nonces,
dashboard y log estructurado— con valores por defecto conservadores, de modo que una instalacion
existente actualice el binario y siga comportandose exactamente igual hasta que un operador decida
habilitarlas.

## Requirements

### Requirement: Bloques de configuracion para las capacidades nuevas

El sistema SHALL reconocer en `config.toml` un bloque `[reorder]` con las claves `enabled`,
`windowMs`, `maxInflightPerUser`, `receiptTimeoutMs`, `autoNonce` y `autoNonceTicketMs`; un bloque
`[dashboard]` con las claves `enabled` y `bufferSize`; y un bloque `[log]` con las claves `level` y
`rawTx`.

`log.level` acepta `debug`, `info`, `warn` o `error`, y determina el nivel minimo que se escribe en
la salida. `log.rawTx` habilita el volcado de la transaccion firmada completa descrito por
`relay-structured-logging`.

`dashboard.bufferSize` distingue la clave ausente de un `0` explicito: ausente toma su valor por
defecto, y `0` deja el bus sin capacidad, es decir inerte, igual que `dashboard.enabled = false`.
Es la unica clave nueva donde el cero tiene significado propio en lugar de ser el valor de una
clave que no se escribio.

Cuando una clave esta ausente, el sistema SHALL aplicar su valor por defecto:

| Clave | Default |
|---|---|
| `reorder.enabled` | `false` |
| `reorder.windowMs` | `3000` |
| `reorder.maxInflightPerUser` | `16` |
| `reorder.receiptTimeoutMs` | `60000` |
| `reorder.autoNonce` | `false` |
| `reorder.autoNonceTicketMs` | `2000` |
| `dashboard.enabled` | `false` |
| `dashboard.bufferSize` | `500` |
| `log.level` | `info` |
| `log.rawTx` | `false` |

#### Scenario: Los bloques estan ausentes

- **WHEN** el servicio arranca con un `config.toml` que no contiene `[reorder]`, `[dashboard]` ni
  `[log]`
- **THEN** el servicio arranca sin error
- **AND** cada parametro toma su valor por defecto
- **AND** ambas capacidades quedan deshabilitadas

#### Scenario: El buffer del dashboard se declara en cero

- **WHEN** el servicio arranca con `dashboard.enabled = true` y `dashboard.bufferSize = 0`
- **THEN** el bus no retiene ningun evento
- **AND** el servicio arranca sin error y no registra ninguna clave descartada
- **AND** el log estructurado sigue emitiendo sus lineas

#### Scenario: Los bloques traen valores explicitos

- **WHEN** el servicio arranca con `reorder.enabled = true`, `reorder.windowMs = 5000` y
  `dashboard.bufferSize = 200`
- **THEN** el servicio usa esos valores en lugar de los defectos
- **AND** las claves no especificadas conservan su valor por defecto

#### Scenario: Un `config.toml` de una instalacion previa

- **WHEN** el servicio arranca con un `config.toml` anterior a estas capacidades
- **THEN** arranca sin error y con el reordenamiento y el reparto de nonces apagados
- **AND** no hace falta agregar ninguna clave

### Requirement: Compatibilidad con la configuracion existente

El sistema MUST arrancar con un `config.toml` escrito antes de esta capacidad y MUST NOT exigir
ninguna clave nueva. Ninguna clave existente cambia de nombre, de tipo ni de valor por defecto.

#### Scenario: Configuracion anterior sin modificar

- **WHEN** el servicio arranca con el `config.toml` de una instalacion previa
- **THEN** el servicio arranca correctamente
- **AND** atiende las mismas rutas y metodos JSON-RPC que antes
- **AND** no emite error ni advertencia por las claves faltantes

### Requirement: Un valor invalido no impide el arranque

Cuando el valor de una clave nueva no es interpretable o esta fuera de rango —un numero negativo,
un texto donde se espera un numero, o un tipo que no corresponde— el sistema SHALL aplicar el valor
por defecto de esa clave, SHALL registrar el hecho indicando la clave afectada, y MUST NOT abortar
el arranque.

#### Scenario: Una ventana de reorden negativa

- **WHEN** `config.toml` declara `reorder.windowMs = -1`
- **THEN** el sistema usa el valor por defecto `3000`
- **AND** registra que la clave fue descartada por invalida
- **AND** el servicio arranca normalmente

#### Scenario: Un buffer de dashboard no numerico

- **WHEN** `config.toml` declara `dashboard.bufferSize = "muchos"`
- **THEN** el sistema usa el valor por defecto `500`
- **AND** el servicio arranca normalmente

#### Scenario: Un nivel de log no reconocido

- **WHEN** `config.toml` declara `log.level = "verboso"`
- **THEN** el sistema usa el valor por defecto `info`
- **AND** registra que la clave fue descartada por invalida
- **AND** el servicio arranca normalmente

### Requirement: Con las capacidades apagadas el comportamiento no cambia

Mientras `reorder.enabled` y `dashboard.enabled` sean `false`, el sistema MUST atender las
peticiones exactamente como lo hacia antes de esta capacidad: mismas rutas, mismos metodos, mismas
respuestas y mismos codigos de error.

#### Scenario: Relay con ambas capacidades deshabilitadas

- **WHEN** un cliente envia una metatx por el camino JSON-RPC existente y ambos flags estan en
  `false`
- **THEN** la respuesta es identica a la que producia el servicio antes de esta capacidad
- **AND** no se retiene ninguna metatx
- **AND** no se expone ninguna ruta adicional

### Requirement: El efecto de cada clave del bloque `[reorder]`

Cada clave del bloque `[reorder]` SHALL gobernar el comportamiento que se describe aqui, y el
servicio SHALL informar sus valores vigentes en `GET /info`:

| Clave | Efecto |
|---|---|
| `enabled` | habilita el tracker autoritativo de nonces, la retencion de metatx adelantadas y el watcher de resultados. Apagado, ninguna de las tres actua |
| `windowMs` | cuanto puede estar retenida una metatx sin que avance el nonce esperado de su usuario, antes de rechazarse. Es tambien la gracia antes de olvidar lo que se sabe de un usuario que se quedo sin metatx en vuelo |
| `maxInflightPerUser` | cuantas metatx de un mismo usuario pueden estar a la vez en vuelo o retenidas |
| `receiptTimeoutMs` | cuanto se espera el resultado de una metatx enviada antes de darla por indeterminada y liberar lo que retiene |
| `autoNonce` | habilita el reparto de nonces descrito por `relay-nonce-endpoint`. Apagado, consultar el nonce no reserva nada |
| `autoNonceTicketMs` | cuanto se espera la metatx que use un nonce entregado, antes de que ese mismo numero vuelva a entregarse |

Publicarlos en `GET /info` importa porque cambian la semantica de lo que el cliente recibe: quien
encadena metatx necesita saber si el nonce que le dieron es suyo o compartido, y cuantas puede tener
en vuelo.

#### Scenario: Consulta de la configuracion vigente

- **WHEN** un cliente pide `GET /info`
- **THEN** la respuesta informa el valor vigente de cada clave del bloque `[reorder]`

#### Scenario: Una clave fuera de rango

- **WHEN** una clave del bloque llega con un valor que no tiene sentido para lo que gobierna
- **THEN** se descarta esa clave y se usa su valor por defecto
- **AND** el servicio arranca igual, dejando registro de lo descartado
