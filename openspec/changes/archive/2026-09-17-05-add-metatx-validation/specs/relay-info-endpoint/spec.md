## MODIFIED Requirements

### Requirement: Estado del permisionado

`GET /info` SHALL informar si el chequeo de permisos sobre el sender esta habilitado, la direccion
del contrato de reglas cuando aplique, **de donde salio esa direccion** -de la configuracion o del
registro de permisos de la red-, y si el nodo que relaya esta permitido.

Esto ultimo importa porque un nodo no permitido relaya sin error aparente y todas sus metatx
fallan on-chain. La fuente de la direccion importa por lo mismo: dos operadores que miran el mismo
campo tienen que poder distinguir una direccion que alguien escribio de una que la red publica.

Cuando la red no expone ningun contrato de reglas, la direccion y su fuente SHALL informarse sin
valor, y eso MUST NOT impedir responder.

#### Scenario: Con el permisionado deshabilitado

- **WHEN** un cliente pide `GET /info` y el chequeo sobre el sender esta deshabilitado
- **THEN** la respuesta lo indica
- **AND** el servicio responde igualmente, sin consultar el contrato de reglas

#### Scenario: Con el permisionado habilitado

- **WHEN** un cliente pide `GET /info` y el chequeo sobre el sender esta habilitado
- **THEN** la respuesta incluye la direccion del contrato de reglas
- **AND** indica si el nodo que relaya esta permitido

#### Scenario: La direccion sale de la configuracion

- **WHEN** hay una direccion de contrato de reglas configurada
- **THEN** la respuesta informa que la fuente es la configuracion

#### Scenario: La direccion sale de la red

- **WHEN** no hay direccion configurada y la red publica un contrato de reglas
- **THEN** la respuesta informa que la fuente es el registro de la red

#### Scenario: Una red sin contrato de reglas

- **WHEN** la red no expone ningun contrato de reglas
- **THEN** la direccion y su fuente se informan sin valor
- **AND** el servicio responde igualmente

### Requirement: Parametros de operacion vigentes

`GET /info` SHALL informar los parametros con los que el servicio esta operando en este momento,
incluidos los del reordenamiento de nonces y los de la validacion del sufijo del modelo de gas. Un
operador tiene que poder confirmar que el servicio tomo la configuracion que cree haberle puesto,
sin entrar al nodo a leer el archivo.

Los parametros de la ventana de vigencia SHALL informarse como la exigencia realmente vigente: con
la exigencia apagada no hay minimo ni tolerancia que respetar, y publicar un numero que no se aplica
haria que un cliente firme para cumplir una regla que no existe.

#### Scenario: Consulta de los parametros

- **WHEN** un cliente pide `GET /info`
- **THEN** la respuesta incluye los parametros de reordenamiento vigentes
- **AND** los valores informados son los que el servicio esta usando, no los del archivo si alguno
  fue descartado por invalido

#### Scenario: Con la validacion de expiracion habilitada

- **WHEN** un cliente pide `GET /info` y la exigencia de ventana de vigencia esta habilitada
- **THEN** la respuesta informa el minimo y la tolerancia vigentes

#### Scenario: Con la validacion de expiracion deshabilitada

- **WHEN** un cliente pide `GET /info` y la exigencia de ventana de vigencia esta deshabilitada
- **THEN** el minimo y la tolerancia se informan como no vigentes
