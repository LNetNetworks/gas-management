## ADDED Requirements

### Requirement: Claves de la validacion de la metatx y del permisionado

El sistema SHALL reconocer en `config.toml` las claves que gobiernan la validacion del sufijo del
modelo de gas y la resolucion del permisionado, y SHALL aplicar su valor por defecto cuando esten
ausentes:

| Clave | Default | Efecto |
|---|---|---|
| `validation.enforceNodeAddress` | `false` | rechaza la metatx cuyo sufijo apunta a otro nodo |
| `validation.enforceExpiration` | `false` | rechaza la metatx vencida y la que llega sin la ventana minima |
| `validation.minExpirationSeconds` | `300` | ventana de vigencia minima al llegar. Solo aplica con la exigencia habilitada |
| `validation.expirationToleranceSeconds` | `2` | tolerancia con la que se aplica ese minimo, por la latencia de la peticion y el redondeo a segundos |
| `security.accountIngressAddress` | vacio | registro de permisos de la red del que se resuelve el contrato de reglas cuando no hay direccion configurada |
| `security.accountRulesCacheMs` | `30000` | cuanto vale lo cacheado de una cuenta antes de volver a consultar la cadena |

Los dos defaults de exigencia son `false`, al reves que en el relayer de referencia, por la
invariante del repositorio: ninguna funcionalidad portada se activa sin opt-in explicito. Con los
dos apagados, el conjunto de metatx que el servicio acepta es exactamente el de hoy.

La direccion de contrato de reglas ya existente conserva su precedencia: mientras este configurada,
el registro de la red no se consulta. Es lo que hace que un despliegue actual no cambie de
comportamiento al actualizar el binario.

#### Scenario: Las claves estan ausentes

- **WHEN** el servicio arranca con un `config.toml` que no contiene ninguna de estas claves
- **THEN** arranca sin error
- **AND** las dos exigencias quedan deshabilitadas
- **AND** el permisionado se resuelve como antes de esta capacidad

#### Scenario: Un valor invalido

- **WHEN** una de estas claves llega con un valor que no tiene sentido para lo que gobierna
- **THEN** se descarta esa clave y se usa su valor por defecto
- **AND** el servicio arranca igual, dejando registro de lo descartado
- **AND** ninguna exigencia queda habilitada por un valor que se descarto

#### Scenario: Una tolerancia mayor que el minimo

- **WHEN** la tolerancia configurada supera al minimo de vigencia
- **THEN** el limite efectivo es cero, no un valor negativo
- **AND** el servicio arranca sin error
