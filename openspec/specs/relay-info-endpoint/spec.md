# relay-info-endpoint Specification

## Purpose
Exponer en una sola llamada los datos que hoy solo viven en `config.toml` y en el log de arranque:
que direcciones esta usando este nodo, de donde salio cada una, y con que parametros esta operando.
Es de aca de donde un integrador saca el `relayHubProxyAddress` que va como `trustedForwarder` de
sus contratos.

## Requirements

### Requirement: Identidad y direcciones del nodo

`GET /info` SHALL informar la direccion del nodo que relaya, la direccion del RelayHub en uso y la
del proxy desde el que se resolvio, la cadena a la que esta conectado y la URL del nodo.

Para cada direccion que se resuelve en lugar de configurarse, el sistema SHALL informar ademas su
**origen**, de modo que al leer la respuesta se sepa si el valor vino de `config.toml` o se leyo de
la cadena. Sin eso, una direccion equivocada es indistinguible de una correcta.

#### Scenario: Consulta de la informacion del servicio

- **WHEN** un cliente pide `GET /info`
- **THEN** la respuesta incluye la direccion del nodo, la del RelayHub, la del proxy del RelayHub,
  el identificador de la cadena y la URL del nodo
- **AND** incluye el origen de la direccion del RelayHub

#### Scenario: El RelayHub se resolvio desde el proxy

- **WHEN** la direccion del RelayHub se obtuvo consultando el proxy
- **THEN** el origen informado lo refleja
- **AND** la direccion del proxy consultado tambien aparece en la respuesta

### Requirement: Estado operativo del nodo

`GET /info` SHALL informar el balance de la cuenta del nodo y el cupo de gas vigente, que son los
dos valores con los que se diagnostica por que el servicio dejo de relayar.

#### Scenario: Consulta del estado operativo

- **WHEN** un cliente pide `GET /info`
- **THEN** la respuesta incluye el balance de la cuenta del nodo
- **AND** incluye el cupo de gas vigente

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

### Requirement: Un dato que no se puede obtener no impide responder

Cuando un dato de la respuesta no se puede obtener —porque exige consultar la cadena y el nodo no
responde, o porque no aplica a este servicio— `GET /info` SHALL informarlo **sin valor** en lugar
de omitir el campo, y MUST responder igualmente con el resto.

Omitir el campo obligaria a quien consume la respuesta a distinguir entre "no se pudo" y "esta
version no lo informa", que son cosas distintas.

#### Scenario: El nodo no responde una consulta

- **WHEN** un cliente pide `GET /info` y una consulta a la cadena falla
- **THEN** ese campo se informa sin valor
- **AND** el resto de la respuesta llega completa
- **AND** el codigo de estado sigue siendo el de una respuesta correcta

#### Scenario: Un campo que no aplica a este servicio

- **WHEN** la respuesta incluye un campo que corresponde a una capacidad que este servicio todavia
  no tiene
- **THEN** el campo se informa sin valor, o con el valor que corresponde a esa capacidad apagada
- **AND** no se omite de la respuesta
