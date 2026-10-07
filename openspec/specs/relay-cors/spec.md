# relay-cors Specification

## Purpose
Permitir que un navegador llame al servicio desde una pagina servida en otro origen. Hoy el
servicio no emite ninguna cabecera de intercambio entre origenes, asi que un dapp de browser no
puede hablarle directo: tiene que interponer un backend propio solo para eso.

## Requirements

### Requirement: El intercambio entre origenes se configura y esta cerrado por defecto

El sistema SHALL permitir configurar que origenes pueden llamarlo desde un navegador. Mientras no
se configure ninguno, MUST NOT emitir cabeceras de intercambio entre origenes, que es como se
comporta hoy.

Que el default sea cerrado importa: el servicio relaya con el cupo de gas del nodo y no pide
autenticacion, asi que abrirlo a cualquier origen es una decision del operador y no del binario.

#### Scenario: Sin configurar ningun origen

- **WHEN** llega una peticion desde un navegador y no hay origenes configurados
- **THEN** la respuesta no lleva cabeceras de intercambio entre origenes
- **AND** la peticion se atiende igual que antes de esta capacidad

#### Scenario: Con un origen configurado

- **WHEN** llega una peticion desde un origen configurado
- **THEN** la respuesta autoriza a ese origen
- **AND** declara que metodos y que cabeceras se aceptan

#### Scenario: Desde un origen no configurado

- **WHEN** llega una peticion desde un origen que no esta configurado
- **THEN** la respuesta no lo autoriza
- **AND** la peticion se procesa igual: la restriccion la aplica el navegador, no el servicio

### Requirement: La consulta previa del navegador se responde sin procesar la peticion

Cuando un navegador consulta por adelantado si puede hacer una llamada, el sistema SHALL responder
esa consulta con las cabeceras que correspondan y MUST NOT ejecutar la operacion consultada.

Sin esto, una consulta previa sobre un relay terminaria relayando: el navegador pregunta antes de
mandar, y la pregunta no es la metatx.

#### Scenario: Consulta previa sobre una ruta

- **WHEN** un navegador consulta por adelantado si puede llamar a una ruta
- **THEN** el servicio responde la consulta con las cabeceras correspondientes
- **AND** no procesa la operacion consultada
- **AND** no relaya ninguna metatx

### Requirement: El intercambio entre origenes no altera las respuestas

Habilitar el intercambio entre origenes MUST NOT cambiar el cuerpo ni el codigo de estado de
ninguna respuesta existente. Lo unico que cambia son las cabeceras que se agregan.

#### Scenario: El camino JSON-RPC con origenes configurados

- **WHEN** un cliente llama al camino JSON-RPC con origenes configurados
- **THEN** el cuerpo y el codigo de la respuesta son los mismos que sin configurar ninguno
