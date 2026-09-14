## Purpose

Gobernar desde `config.toml` las capacidades nuevas del RelaySigner —reordenamiento de nonces y
dashboard— con valores por defecto conservadores, de modo que una instalacion existente actualice
el binario y siga comportandose exactamente igual hasta que un operador decida habilitarlas.

## ADDED Requirements

### Requirement: Bloques de configuracion para las capacidades nuevas

El sistema SHALL reconocer en `config.toml` un bloque `[reorder]` con las claves `enabled`,
`windowMs`, `maxInflightPerUser` y `receiptTimeoutMs`, y un bloque `[dashboard]` con las claves
`enabled` y `bufferSize`.

Cuando una clave esta ausente, el sistema SHALL aplicar su valor por defecto:

| Clave | Default |
|---|---|
| `reorder.enabled` | `false` |
| `reorder.windowMs` | `3000` |
| `reorder.maxInflightPerUser` | `16` |
| `reorder.receiptTimeoutMs` | `60000` |
| `dashboard.enabled` | `false` |
| `dashboard.bufferSize` | `500` |

#### Scenario: Los bloques estan ausentes

- **WHEN** el servicio arranca con un `config.toml` que no contiene `[reorder]` ni `[dashboard]`
- **THEN** el servicio arranca sin error
- **AND** cada parametro toma su valor por defecto
- **AND** ambas capacidades quedan deshabilitadas

#### Scenario: Los bloques traen valores explicitos

- **WHEN** el servicio arranca con `reorder.enabled = true`, `reorder.windowMs = 5000` y
  `dashboard.bufferSize = 200`
- **THEN** el servicio usa esos valores en lugar de los defectos
- **AND** las claves no especificadas conservan su valor por defecto

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
