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
| `reorder.maxInflightPerUser` | `5` |
| `reorder.receiptTimeoutMs` | `60000` |
| `reorder.autoNonce` | `false` |
| `reorder.autoNonceTicketMs` | `2000` |
| `dashboard.enabled` | `false` |
| `dashboard.bufferSize` | `500` |
| `log.level` | `info` |
| `log.rawTx` | `false` |

El default de `reorder.maxInflightPerUser` iguala el techo que la red impone POR CUENTA: Besu acota
cuantas transacciones pendientes admite de una sola cuenta, y esa cuenta es la del writer node,
remitente de todas las envolventes. Un cupo por encima de ese techo no agrega ninguna metatx minada
-el que decide es Besu- y en cambio admite metatx que van a ocupar la ventana de reordenamiento
entera para terminar rechazadas por nonce equivocado, que no es el motivo real. Al ras del techo,
lo que no cabe se rechaza en la puerta indicando que se supero el limite.

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

#### Scenario: El tope por usuario no se fija

- **WHEN** `config.toml` no fija `reorder.maxInflightPerUser`
- **THEN** el tope por usuario es 5
- **AND** `GET /info` informa ese valor
