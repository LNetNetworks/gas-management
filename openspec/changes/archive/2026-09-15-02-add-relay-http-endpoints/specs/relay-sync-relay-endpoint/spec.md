## Purpose

Relayar una metatx y devolver como termino, en una sola llamada. Hoy eso exige dos pasos —mandarla
y despues pollear el receipt— y obliga a cada integrador a escribir su propio bucle de espera con
su propio criterio de cuando rendirse.

## ADDED Requirements

### Requirement: Relayar y esperar el resultado

`POST /relay` SHALL aceptar una transaccion firmada, relayarla y responder recien cuando se sepa
como termino en la cadena. La transaccion SHALL aceptarse bajo el nombre de campo principal y
tambien bajo su alias, para que un cliente escrito contra el relayer de referencia funcione sin
cambios.

La metatx que entra por esta ruta SHALL recorrer exactamente las mismas validaciones y el mismo
camino de relay que la que entra por `POST /`. No puede haber dos criterios sobre que metatx es
aceptable segun por que puerta entro.

#### Scenario: Una metatx que se ejecuta

- **WHEN** un cliente relaya una metatx valida por esta ruta
- **THEN** el servicio responde una vez resuelta en la cadena
- **AND** la respuesta indica que se ejecuto

#### Scenario: La transaccion llega bajo el alias

- **WHEN** un cliente envia la transaccion firmada bajo el nombre alternativo
- **THEN** el servicio la procesa igual que si hubiera usado el principal

#### Scenario: Las dos puertas validan lo mismo

- **WHEN** una metatx que `POST /` rechaza se envia por esta ruta
- **THEN** tambien se rechaza, por el mismo motivo

### Requirement: Resultado decodificado de la metatx

La respuesta SHALL describir como termino la metatx: su hash, si fue un deploy y la direccion
desplegada, en que bloque quedo, el gas consumido, si el hub la ejecuto, el resultado o el motivo
del revert, quien la origino, a donde iba, su nonce y el limite de gas con el que se envio.

En un deploy, la direccion del contrato creado SHALL salir del evento que emite el hub y no del
campo del receipt: el receipt trae la direccion de la transaccion envolvente, que es del nodo, no
la del contrato del usuario.

#### Scenario: Una llamada a un contrato

- **WHEN** se relaya una metatx dirigida a un contrato y se ejecuta
- **THEN** la respuesta incluye el hash, el bloque, el gas consumido y que se ejecuto
- **AND** incluye quien la origino, a donde iba y su nonce

#### Scenario: Un deploy

- **WHEN** se relaya una metatx sin destino y el contrato se crea
- **THEN** la respuesta indica que fue un deploy
- **AND** la direccion informada es la del contrato creado por el usuario

#### Scenario: Una metatx que revierte en el contrato destino

- **WHEN** el hub relaya la metatx pero el contrato destino revierte
- **THEN** la respuesta indica que no se ejecuto
- **AND** informa el motivo del revert
- **AND** el codigo de estado es el de una respuesta correcta: la metatx se relayo, lo que fallo
  fue el contrato destino

#### Scenario: El hub rechaza la metatx

- **WHEN** el hub rechaza la metatx al ejecutarla
- **THEN** la respuesta informa el codigo de error del hub y su nombre

### Requirement: Un rechazo se responde con su motivo y su codigo

Cuando la metatx no llega a relayarse, `POST /relay` SHALL responder `400` con el motivo, un codigo
que lo identifique y el detalle cuando exista.

El codigo SHALL pertenecer a un catalogo definido y estable, el mismo que usa el relayer de
referencia, para que un cliente pueda decidir en base al codigo y no parseando el texto del
mensaje. Un rechazo cuyo motivo no corresponda a ningun codigo del catalogo SHALL usar el codigo
generico previsto para ese caso, nunca un codigo inventado.

#### Scenario: Una transaccion que no se puede decodificar

- **WHEN** un cliente envia algo que no es una transaccion firmada
- **THEN** el servicio responde `400`
- **AND** el codigo identifica que la transaccion no se pudo decodificar

#### Scenario: Un sender sin permiso

- **WHEN** el chequeo de permisos esta habilitado y el sender no esta permitido
- **THEN** el servicio responde `400`
- **AND** el codigo identifica que el sender no esta permitido

#### Scenario: Falta la transaccion en el cuerpo

- **WHEN** el cuerpo no trae la transaccion firmada, o no es hexadecimal
- **THEN** el servicio responde `400` indicando que se esperaba
- **AND** no consulta la cadena

#### Scenario: Un motivo sin codigo propio

- **WHEN** la metatx se rechaza por un motivo que no tiene codigo propio en el catalogo
- **THEN** la respuesta usa el codigo generico
- **AND** el motivo sigue viniendo en el texto

### Requirement: La espera esta acotada

`POST /relay` MUST NOT esperar indefinidamente. La espera SHALL estar acotada por el parametro de
configuracion previsto para eso, y al agotarse SHALL responder informando que la metatx se envio
pero su resultado no se conocio a tiempo, con un codigo propio que lo distinga de un rechazo.

La distincion importa: una metatx que vencio la espera **fue enviada** y puede minarse despues. Un
cliente que la trate como rechazada y la reenvie produce un nonce repetido.

#### Scenario: El resultado no llega a tiempo

- **WHEN** se relaya una metatx y no se resuelve dentro del tiempo configurado
- **THEN** el servicio responde informando que se envio pero no se conocio el resultado
- **AND** el codigo la distingue de una metatx rechazada
- **AND** la respuesta incluye el hash, para poder consultarla despues

#### Scenario: Una espera vencida no cancela el envio

- **WHEN** vence la espera de una metatx ya enviada
- **THEN** la metatx sigue su curso en la cadena
- **AND** su resultado se puede consultar despues por el camino JSON-RPC

### Requirement: Esperar no bloquea a los demas

Una peticion a `POST /relay` que esta esperando su receipt MUST NOT impedir que el servicio atienda
otras peticiones, ni retener el cupo de gas por bloque mas alla de lo que lo retiene el camino
JSON-RPC.

#### Scenario: Varias esperas simultaneas

- **WHEN** varios clientes relayan por esta ruta al mismo tiempo
- **THEN** cada uno recibe el resultado de su propia metatx
- **AND** el camino JSON-RPC sigue atendiendo con normalidad mientras tanto

### Requirement: Los eventos de la metatx se emiten igual

Una metatx relayada por esta ruta SHALL producir los mismos eventos de operacion que si hubiera
entrado por el camino JSON-RPC, con la misma correlacion por peticion y por metatx.

#### Scenario: Traza de una metatx relayada por esta ruta

- **WHEN** un cliente relaya una metatx por `POST /relay`
- **THEN** se emiten los eventos de recepcion, decodificacion y envio de esa metatx
- **AND** todos comparten el mismo identificador de metatx
