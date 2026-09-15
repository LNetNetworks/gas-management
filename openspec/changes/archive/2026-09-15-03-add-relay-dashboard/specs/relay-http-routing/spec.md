## MODIFIED Requirements

### Requirement: Rutas del servicio

El sistema SHALL atender, ademas del catch-all, las rutas `GET /info`, `GET /nonce/{address}` y
`POST /relay`, cada una con el comportamiento que definen sus capacidades.

SHALL atender ademas las rutas del monitor —la pagina y su stream— **solo cuando el dashboard esta
habilitado**. Con el dashboard deshabilitado esas rutas no existen, y una peticion a esos paths se
atiende como cualquier otro path sin manejador propio.

Una ruta definida que se invoca con un metodo HTTP que no le corresponde SHALL responder `405`, y
no caer al camino JSON-RPC: un `GET /relay` es un error del cliente, no una peticion JSON-RPC. Esto
aplica a una ruta del monitor solo mientras exista, o sea con el dashboard habilitado.

#### Scenario: Una ruta con el metodo equivocado

- **WHEN** un cliente pide `GET /relay` o `POST /info`
- **THEN** el servicio responde `405`
- **AND** no procesa la peticion como JSON-RPC

#### Scenario: Una direccion mal formada en la ruta de nonce

- **WHEN** un cliente pide `GET /nonce/` sin direccion, o con algo que no es una direccion
- **THEN** el servicio responde `400` con el motivo
- **AND** no cae al camino JSON-RPC

#### Scenario: Las rutas del monitor con el dashboard habilitado

- **WHEN** el dashboard esta habilitado y un cliente pide la pagina del monitor
- **THEN** la atiende el manejador del monitor
- **AND** pedirla con un metodo que no le corresponde responde `405`

#### Scenario: Las rutas del monitor con el dashboard deshabilitado

- **WHEN** el dashboard esta deshabilitado y un cliente pide la pagina del monitor
- **THEN** esa ruta no existe y la peticion la atiende el camino JSON-RPC, como cualquier path desconocido
- **AND** no se responde `405`, porque no hay ninguna ruta registrada para ese path
