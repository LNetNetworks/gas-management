## MODIFIED Requirements

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

## ADDED Requirements

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
