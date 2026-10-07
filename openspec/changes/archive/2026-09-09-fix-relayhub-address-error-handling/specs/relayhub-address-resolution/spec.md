## Purpose

Resolver la dirección del contrato RelayHub consultando su contrato proxy vía
`eth_call` durante el arranque del RelaySigner, garantizando que cualquier fallo se
reporte como error al llamador y nunca interrumpa el proceso de forma abrupta.

## ADDED Requirements

### Requirement: Resolución exitosa de la dirección del RelayHub

El sistema SHALL consultar el contrato proxy configurado mediante una llamada
JSON-RPC `eth_call` y devolver la dirección del RelayHub que ese proxy reporta.

#### Scenario: El proxy responde con una dirección válida

- **WHEN** el nodo responde con un resultado JSON-RPC que codifica una dirección
  Ethereum válida
- **THEN** el sistema devuelve esa dirección al llamador
- **AND** no devuelve error

### Requirement: Los fallos se reportan como error, nunca como panic

El sistema SHALL devolver un error al llamador ante cualquier fallo de red,
transporte, protocolo o decodificación, y MUST NOT interrumpir el proceso mediante
panic ni continuar la ejecución con datos inválidos.

#### Scenario: El nodo no está disponible

- **WHEN** la petición HTTP al nodo falla o excede el timeout configurado
- **THEN** el sistema devuelve un error no nulo
- **AND** devuelve una dirección nula

#### Scenario: El nodo responde con un error JSON-RPC

- **WHEN** la respuesta del nodo contiene un objeto `error` de JSON-RPC
- **THEN** el sistema devuelve un error que incluye el mensaje reportado por el nodo
- **AND** no intenta decodificar el campo `result`

#### Scenario: El resultado está vacío o malformado

- **WHEN** la respuesta del nodo trae un `result` ausente, vacío o que no representa
  una dirección hexadecimal válida
- **THEN** el sistema devuelve un error describiendo el resultado inválido
- **AND** no se produce ningún panic por índice o rango fuera de límites

#### Scenario: La decodificación ABI falla

- **WHEN** la construcción del tipo ABI o el desempaquetado del valor devuelto falla
- **THEN** el sistema devuelve ese error inmediatamente
- **AND** no continúa hacia la conversión del valor

### Requirement: El arranque del servicio degrada de forma controlada

El sistema SHALL permitir que el llamador traduzca un fallo de resolución en el error
de configuración correspondiente del RelaySigner, de modo que el arranque termine con
un mensaje accionable en lugar de un volcado de panic.

#### Scenario: Arranque con un nodo inalcanzable

- **WHEN** el RelaySigner arranca con una URL de nodo que no responde
- **THEN** la inicialización devuelve el error `FailedKeyConfig` con código `-32610`
- **AND** el proceso no emite un stack trace de panic

### Requirement: El payload de la consulta no se filtra a la salida estándar

El sistema MUST NOT escribir el payload de la petición JSON-RPC ni la respuesta cruda
en la salida estándar, evitando así el logger de auditoría del proyecto.

#### Scenario: Resolución de la dirección en condiciones normales

- **WHEN** el sistema ejecuta la consulta al proxy
- **THEN** no se imprime el cuerpo de la petición ni la respuesta en stdout
