# metatx-gas-model-validation Specification

## Purpose
Rechazar antes de gastar una transaccion del writer node la metatx que el RelayHub no va a poder
ejecutar por lo que dice su propio sufijo del modelo de gas: la que esta dirigida a otro nodo y la
que ya vencio o esta por vencer.

## Requirements

### Requirement: La metatx dirigida a otro nodo se rechaza

El sistema SHALL poder exigir que la direccion de nodo que la metatx lleva en su sufijo sea la de
este servicio. Cuando la exigencia esta habilitada y no coinciden, MUST rechazarla sin enviarla,
indicando a que nodo apunta y cual es este.

Una metatx dirigida a otro nodo no la puede ejecutar este: el hub la rechaza on-chain con la
transaccion de este nodo ya gastada.

#### Scenario: Una metatx dirigida a otro nodo

- **WHEN** llega una metatx cuyo sufijo apunta a un nodo distinto del de este servicio y la
  exigencia esta habilitada
- **THEN** se rechaza indicando a que nodo apunta y cual es este
- **AND** el codigo del rechazo identifica que la metatx apunta a otro nodo
- **AND** no se envia nada a la cadena

#### Scenario: Una metatx dirigida a este nodo

- **WHEN** llega una metatx cuyo sufijo apunta a este servicio
- **THEN** la validacion la deja pasar

### Requirement: La metatx vencida se rechaza

El sistema SHALL poder exigir que la metatx no haya vencido. Cuando la exigencia esta habilitada y
la expiracion del sufijo ya paso, MUST rechazarla sin enviarla, informando la expiracion que traia y
el momento en que se evaluo.

#### Scenario: Una metatx ya vencida

- **WHEN** llega una metatx cuya expiracion ya paso y la exigencia esta habilitada
- **THEN** se rechaza informando la expiracion y el momento de la evaluacion
- **AND** el codigo del rechazo identifica que la metatx vencio
- **AND** no se envia nada a la cadena

### Requirement: La ventana minima se exige con tolerancia

El sistema SHALL poder exigir que a la metatx le quede, al llegar, una ventana minima de vigencia.
Esa exigencia SHALL aplicarse **con una tolerancia** configurable, de modo que el limite efectivo
sea el minimo menos la tolerancia. El rechazo MUST informar cuanto le quedaba, cual es el minimo y
cual la tolerancia aplicada.

Una expiracion al filo no sirve: entre validar, esperar el turno del nonce y minar pasan segundos, y
si vence en el medio ya se gasto una transaccion del writer node. Pero exigir el valor exacto
rechazaria al cliente que hizo lo correcto: quien firma `ahora + 300` llega con 298, porque se pierde
la latencia de la peticion y el redondeo a segundos de cada lado.

#### Scenario: Una metatx con menos ventana que el minimo

- **WHEN** llega una metatx cuya vigencia restante es menor que el minimo menos la tolerancia
- **THEN** se rechaza informando lo que le quedaba, el minimo y la tolerancia
- **AND** el codigo del rechazo identifica que la ventana es insuficiente
- **AND** no se envia nada a la cadena

#### Scenario: Una metatx al filo, dentro de la tolerancia

- **WHEN** llega una metatx cuya vigencia restante es menor que el minimo pero cae dentro de la
  tolerancia
- **THEN** la validacion la deja pasar

#### Scenario: Una metatx con vigencia holgada

- **WHEN** llega una metatx cuya vigencia restante supera el minimo
- **THEN** la validacion la deja pasar

### Requirement: Un sufijo ausente o ilegible no cambia lo que hoy se relaya

Cuando el sufijo del modelo de gas no esta, viene incompleto o su expiracion no es representable,
el sistema MUST NOT rechazar la metatx por eso: SHALL relayarla como lo hace hoy y registrar que esos
datos no se pudieron obtener.

Rechazar aca cambiaria el conjunto de metatx que el servicio acepta por una razon que no es la que
esta capacidad quiere cubrir. Lo que se valida es un sufijo presente y legible que **dice** algo
inaceptable.

#### Scenario: Una metatx sin sufijo

- **WHEN** llega una metatx cuyo `data` es mas corto que el sufijo del modelo de gas
- **THEN** se relaya como antes de esta capacidad
- **AND** los campos del sufijo se registran sin valor

#### Scenario: Una expiracion que no se puede representar

- **WHEN** la expiracion del sufijo no se puede representar como un momento en el tiempo
- **THEN** no se rechaza por la ventana de vigencia
- **AND** se registra que no se pudo obtener

### Requirement: La validacion es la misma por las dos puertas

Las dos puertas de relay -el camino JSON-RPC y la ruta de relay sincronico- SHALL aplicar estas
validaciones de forma identica, y el motivo del rechazo SHALL ser el mismo por las dos. Cada una
responde con la forma que ya define su capacidad: el camino JSON-RPC con su error en el cuerpo, la
ruta de relay con su codigo de estado y su codigo del catalogo.

No puede haber dos criterios sobre que metatx es aceptable segun por donde entro.

#### Scenario: La misma metatx por las dos puertas

- **WHEN** la misma metatx invalida se envia por el camino JSON-RPC y por la ruta de relay
- **THEN** las dos la rechazan por el mismo motivo
- **AND** cada respuesta tiene la forma que define la capacidad de esa puerta

### Requirement: El rechazo deja rastro y no inventa codigos

Cada rechazo de esta capacidad SHALL registrarse con el mismo evento con el que se registra
cualquier otro rechazo, con su `metaTxId`, y su codigo SHALL pertenecer al catalogo estable que ya
usan las respuestas de relay.

#### Scenario: Una metatx rechazada por el sufijo

- **WHEN** se rechaza una metatx por su direccion de nodo o por su expiracion
- **THEN** se registra el rechazo con el motivo y el codigo
- **AND** ese evento comparte el identificador de los eventos previos de esa metatx

### Requirement: Con las exigencias apagadas no se rechaza nada

Cada exigencia de esta capacidad SHALL poder deshabilitarse por separado, y su estado por defecto
MUST ser deshabilitada. Con una exigencia apagada, una metatx que la incumple MUST relayarse
exactamente como antes de esta capacidad.

#### Scenario: Una metatx vencida con la exigencia apagada

- **WHEN** llega una metatx vencida y la exigencia de expiracion esta apagada
- **THEN** se relaya como antes de esta capacidad
- **AND** la respuesta es la misma que producia el servicio antes

#### Scenario: Una exigencia encendida y la otra apagada

- **WHEN** esta habilitada la exigencia de direccion de nodo y deshabilitada la de expiracion
- **THEN** una metatx dirigida a otro nodo se rechaza
- **AND** una metatx vencida dirigida a este nodo se relaya
