## Purpose

Saber de donde sale el contrato de reglas que dice quien puede operar, en lugar de creerle a una
direccion escrita a mano en la configuracion, y avisar cuando el nodo que relaya no esta permitido
-el fallo que hace que todas las metatx fallen on-chain sin ningun error visible del lado del
servicio.

## ADDED Requirements

### Requirement: De donde sale el contrato de reglas

El sistema SHALL resolver el contrato de reglas del permisionado con este orden: si hay una
direccion configurada, esa; si no, la que el registro de permisos de la red tenga publicada. SHALL
informar cual de las dos fuentes uso.

Hoy la direccion es fija en la configuracion. Si el contrato de reglas de la red cambia, el servicio
sigue preguntandole al viejo y contesta que si sobre un allowlist que ya no rige.

#### Scenario: Con una direccion configurada

- **WHEN** el servicio arranca con una direccion de contrato de reglas configurada
- **THEN** usa esa direccion
- **AND** informa que la fuente es la configuracion

#### Scenario: Sin direccion configurada

- **WHEN** el servicio arranca sin direccion configurada y la red publica un contrato de reglas
- **THEN** usa la direccion que publica la red
- **AND** informa que la fuente es el registro de la red

#### Scenario: Una direccion configurada que no existe en la red

- **WHEN** la direccion configurada no corresponde a ningun contrato de esa red
- **THEN** el servicio lo registra
- **AND** no da por permitida ninguna cuenta por no haber podido leer las reglas

### Requirement: Una red sin permisionado no es un error

Cuando la red no expone ningun contrato de reglas -no hay registro de permisos, o no tiene ninguno
publicado- el sistema SHALL operar sin permisionado de cuentas y MUST arrancar igual. Ese es el caso
de una red de desarrollo, donde el chequeo simplemente no aplica.

#### Scenario: Una red sin registro de permisos

- **WHEN** el servicio arranca contra una red que no expone contrato de reglas y sin direccion
  configurada
- **THEN** arranca sin error
- **AND** informa que no hay contrato de reglas
- **AND** relaya con normalidad

### Requirement: El resultado del permisionado se cachea con vigencia acotada

El sistema SHALL cachear por cuenta el resultado de la consulta de permisos durante un plazo
configurable, y SHALL volver a consultar la cadena una vez vencido. El plazo SHALL informarse entre
los parametros vigentes.

Sin cache, cada metatx con el chequeo encendido paga una consulta a la cadena. La contrapartida,
explicita: dar de alta una cuenta tarda hasta ese plazo en verse.

#### Scenario: Dos metatx seguidas del mismo sender

- **WHEN** llegan dos metatx del mismo sender dentro del plazo de vigencia
- **THEN** la cadena se consulta una sola vez

#### Scenario: Una cuenta dada de alta recien

- **WHEN** una cuenta se da de alta en el contrato de reglas y se consulta despues del plazo
- **THEN** el servicio refleja el alta

### Requirement: Un allowlist que no se puede leer no deja pasar

Cuando el chequeo de permisos sobre el sender esta habilitado y la consulta al contrato de reglas
falla, el sistema MUST rechazar la metatx indicando que no se pudo verificar el permiso, y MUST NOT
relayarla.

Un allowlist que no se puede leer no se puede aplicar: dejar pasar seria abrir la puerta creyendo lo
contrario.

#### Scenario: El contrato de reglas no responde

- **WHEN** el chequeo esta habilitado y la consulta al contrato de reglas falla
- **THEN** la metatx se rechaza indicando que no se pudo verificar el permiso
- **AND** no se envia nada a la cadena
- **AND** el proceso sigue en ejecucion

#### Scenario: El contrato de reglas no responde con el chequeo apagado

- **WHEN** el chequeo sobre el sender esta deshabilitado
- **THEN** la metatx se relaya sin consultar el contrato de reglas

### Requirement: Se comprueba si el nodo que relaya esta permitido

Al arrancar, y cuando haya contrato de reglas, el sistema SHALL comprobar si el nodo que relaya esta
permitido, y SHALL informar el resultado. Esa comprobacion MUST NOT impedir el arranque, ni siquiera
cuando el nodo no esta permitido o la consulta falla.

Un nodo no permitido relaya sin error aparente y todas sus metatx fallan on-chain: el sintoma es
opaco y el diagnostico se resuelve mirando este dato. Pero la invariante del servicio es que ningun
fallo externo tumba el proceso, y un binario que no levanta diagnostica peor que uno que informa.

#### Scenario: El nodo esta permitido

- **WHEN** el servicio arranca y el nodo que relaya esta permitido
- **THEN** lo informa entre su estado

#### Scenario: El nodo no esta permitido

- **WHEN** el servicio arranca y el nodo que relaya no esta permitido
- **THEN** arranca igual
- **AND** lo informa entre su estado
- **AND** lo registra de forma accionable

#### Scenario: La comprobacion no se puede hacer

- **WHEN** el servicio arranca y la comprobacion del nodo no se puede realizar
- **THEN** arranca igual
- **AND** informa ese dato sin valor, en lugar de afirmar que esta permitido
