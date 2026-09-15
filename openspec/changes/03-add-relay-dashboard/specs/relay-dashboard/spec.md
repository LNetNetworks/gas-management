## Purpose

Ver en vivo lo que le pasa a cada metatx mientras pasa, en lugar de reconstruirlo despues leyendo
un archivo de log. Es la herramienta con la que se diagnostica el reordenamiento de nonces: sin
ella, validar por que una metatx se retuvo y cuanto es leer lineas a mano.

## ADDED Requirements

### Requirement: El monitor esta apagado por defecto

Las rutas del monitor SHALL existir solo cuando el dashboard esta habilitado en la configuracion.
Con el dashboard deshabilitado el sistema MUST NOT atenderlas, y una peticion a esos paths se
comporta como cualquier otro path sin manejador propio.

Es deliberado: el monitor publica quien mando cada metatx, con que nonce y contra que contrato, y
como el resto del servicio no pide autenticacion.

#### Scenario: Con el dashboard deshabilitado

- **WHEN** un cliente pide la pagina del monitor o su stream, y el dashboard esta deshabilitado
- **THEN** esas rutas no existen y la peticion se atiende como cualquier path desconocido
- **AND** el servicio no retiene ni publica eventos

#### Scenario: Con el dashboard habilitado

- **WHEN** el dashboard esta habilitado
- **THEN** las dos rutas del monitor responden

### Requirement: Entrega de la pagina del monitor

El sistema SHALL servir la pagina del monitor como contenido HTML, desde el propio binario, sin
depender de archivos externos ni de un paso de construccion.

Que viaje dentro del binario importa en operacion: un despliegue es reemplazar un ejecutable, y una
pagina que dependiera de archivos al lado se rompe en cuanto alguien copia solo el binario.

#### Scenario: Se pide la pagina

- **WHEN** un cliente pide la pagina del monitor
- **THEN** el servicio responde el HTML de la pagina
- **AND** lo hace sin leer ningun archivo del sistema de archivos

### Requirement: Stream de eventos en vivo

El sistema SHALL exponer los eventos del bus como un flujo de eventos enviados por el servidor, de
modo que un navegador reciba cada evento nuevo sin volver a preguntar.

El flujo SHALL identificar cada evento con su numero de secuencia, para que el cliente sepa desde
donde continuar si se corta.

#### Scenario: Un observador se conecta

- **WHEN** un cliente abre el stream
- **THEN** el servicio responde un flujo de eventos enviados por el servidor
- **AND** cada evento transmitido lleva su numero de secuencia

#### Scenario: Se publica un evento con el stream abierto

- **WHEN** se registra un evento de operacion mientras hay un observador conectado
- **THEN** ese observador lo recibe sin volver a pedirlo

### Requirement: Reanudacion sin huecos ni repetidos

El stream SHALL aceptar un punto de partida, indicado por el cliente en la peticion o por el
mecanismo de reintento automatico del navegador. Al conectarse, el sistema SHALL entregar primero
los eventos retenidos posteriores a ese punto y despues los nuevos.

Entre lo que se entrega del historial y el comienzo de lo nuevo MUST NOT perderse ni duplicarse
ningun evento: un evento publicado justo en ese instante tiene que llegar exactamente una vez.

Es la garantia que hace util la reanudacion. Sin ella, una reconexion en medio de una rafaga deja
la vista con un hueco silencioso, que es justo cuando el monitor importa.

#### Scenario: Reconexion desde un punto conocido

- **WHEN** un observador se reconecta indicando el ultimo numero de secuencia que recibio
- **THEN** recibe los eventos retenidos posteriores a ese numero, en orden
- **AND** despues sigue recibiendo los nuevos

#### Scenario: Un evento publicado durante la conexion

- **WHEN** se publica un evento en el instante en que un observador se esta conectando
- **THEN** ese observador lo recibe exactamente una vez

#### Scenario: Reconexion sin indicar punto de partida

- **WHEN** un observador se conecta sin indicar desde donde continuar
- **THEN** recibe todos los eventos retenidos y luego los nuevos

#### Scenario: Reconexion desde un punto ya descartado

- **WHEN** un observador se reconecta desde un punto anterior a lo que el bus conserva
- **THEN** recibe todo lo retenido, sin error
- **AND** el salto en la numeracion deja visible que hubo un hueco

### Requirement: La conexion sobrevive a los intermediarios

El stream SHALL emitir periodicamente una senal que mantenga viva la conexion, y SHALL pedir a los
intermediarios que no la almacenen en memoria intermedia.

Sin lo primero, un proxy que corta por inactividad tira la conexion cuando no hay metatx. Sin lo
segundo, un proxy con almacenamiento intermedio retiene el flujo y a la pagina no llega nada,
aunque todo lo demas funcione.

#### Scenario: Un periodo sin eventos

- **WHEN** pasa un periodo prolongado sin que se publique ningun evento
- **THEN** el stream sigue abierto
- **AND** el cliente sigue recibiendo la senal de que la conexion esta viva

#### Scenario: Un intermediario con almacenamiento intermedio

- **WHEN** el stream atraviesa un proxy que almacena respuestas
- **THEN** la respuesta indica que no debe almacenarse ni transformarse

### Requirement: Cantidad de observadores acotada

El sistema SHALL limitar cuantos observadores puede haber conectados a la vez. Una conexion que
supere ese limite SHALL rechazarse indicando el motivo, y MUST NOT desplazar a una existente.

Cada observador es una conexion abierta sostenida por el proceso: sin tope, una pagina que
reconecta en un bucle agota los recursos del servicio que esta observando.

#### Scenario: Se alcanza el limite

- **WHEN** ya hay tantos observadores conectados como permite el limite y llega otro
- **THEN** el servicio rechaza la conexion nueva indicando el motivo
- **AND** los observadores ya conectados siguen recibiendo eventos

#### Scenario: Se libera un lugar

- **WHEN** un observador se desconecta estando el limite alcanzado
- **THEN** una conexion nueva se acepta

### Requirement: Observar nunca afecta al camino de la metatx

Ni la pagina ni el stream MUST alterar el procesamiento de las metatx. El stream SHALL limitarse a
leer el bus, y un observador lento, caido o que se desconecta de golpe MUST NOT frenar un relay ni
tumbar el proceso.

Al cerrarse una conexion el sistema SHALL liberar lo que esa conexion ocupaba, de modo que abrir y
cerrar el monitor repetidamente no acumule recursos.

#### Scenario: Un observador se desconecta de golpe

- **WHEN** un observador corta la conexion sin avisar
- **THEN** el servicio libera lo que esa conexion ocupaba
- **AND** los demas observadores siguen recibiendo eventos
- **AND** el camino de la metatx no se ve afectado

#### Scenario: Relay con el monitor abierto

- **WHEN** se relaya una metatx mientras hay observadores conectados
- **THEN** la respuesta al cliente es la misma que sin observadores
- **AND** el tiempo de respuesta no depende de cuantos observadores haya

#### Scenario: Abrir y cerrar el monitor repetidamente

- **WHEN** un cliente abre y cierra el stream muchas veces
- **THEN** el servicio no acumula observadores ni conexiones
- **AND** el limite de observadores vuelve a estar disponible
