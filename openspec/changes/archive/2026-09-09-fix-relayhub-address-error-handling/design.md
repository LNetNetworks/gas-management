## Context

`getRelayHubContractAddress` (`service/httpCall.go:18`) es una función privada del
paquete `service` con un único llamador: `relaySignerService.go:88`, dentro de la
inicialización del servicio. Su firma
`(rpcURL, id, relayHubProxyAddress string, _timeout int) (*common.Address, error)`
ya expone un `error`, y el llamador ya lo traduce a
`errors.FailedKeyConfig.New("Can't get relayHub smart contract address from Proxy", -32610)`.

El contrato externo, por tanto, ya es el correcto: el problema es puramente que la
implementación no lo respeta. Hoy imprime errores con `fmt.Println` y sigue adelante,
o hace slicing posicional sobre un `json.RawMessage` que puede venir vacío.

El proyecto usa `log.GeneralLogger` (alias del paquete `audit`) como canal de logging
en todo el paquete `service`; los `fmt.Println` de este archivo son residuos de
depuración.

## Goals / Non-Goals

**Goals:**
- Que toda ruta de fallo termine en `return nil, err`.
- Eliminar las tres fuentes de panic: uso de `request` antes de validar su error,
  slicing posicional de `Result`, e indexado y type assertion sobre el slice
  desempaquetado.
- Producir mensajes de error que digan qué falló y con qué dato.
- No cambiar la firma de la función ni el código del llamador.

**Non-Goals:**
- Reintentos, backoff o failover a un nodo alterno — es una consulta de arranque; si
  el nodo no responde, el fallo debe ser visible e inmediato.
- Migrar la llamada al cliente `ethclient` de go-ethereum en lugar del HTTP crudo.
  Es una mejora legítima pero de mayor alcance.
- Tocar los `fmt.Println` de depuración de otros archivos (`transaction.go`,
  `blockchain/client.go`, `relaySignerService.go`); son un cambio aparte.

## Decisions

### Decisión 1: Decodificar `Result` con `json.Unmarshal`, no por posición

El código actual hace `resultData[3 : len(resultData)-1]` sobre
`string(rpcMessage.Result)`, asumiendo que el `json.RawMessage` es literalmente
`"0x…"` con comillas incluidas: recorta la comilla de apertura más `0x`, y la comilla
de cierre. Funciona en el camino feliz, pero con un `Result` ausente (longitud 0)
hace panic por límites, y no valida nada del contenido.

Se reemplaza por `json.Unmarshal(rpcMessage.Result, &resultHex)` hacia un `string`,
que es la forma correcta de leer una cadena JSON, seguido de `common.Hex2Bytes`
sobre el valor sin el prefijo `0x`. Es más corto, más claro y no puede hacer panic.

### Decisión 2: Comprobar `rpcMessage.Error` antes que `Result`

`rpc.JsonrpcMessage` ya tiene el campo `Error *jsonError`, pero nunca se consulta.
Un error JSON-RPC deja `Result` nulo, así que revisarlo primero convierte la causa
raíz (por ejemplo, "execution reverted" o un proxy mal configurado) en el mensaje de
error, en vez de un opaco fallo de decodificación aguas abajo.

### Decisión 3: Validar la dirección antes de convertirla

Tras el desempaquetado ABI se verifica `len(addressUnpacked) > 0` y se usa la type
assertion comprobada `addr, ok := addressUnpacked[0].(common.Address)`. Ambas
condiciones son defensivas frente a un ABI o una respuesta que no encajen con lo
esperado, y ambas son hoy fuentes directas de panic.

### Decisión 4: Reordenar la validación de `http.NewRequest`

Mover `request.Header.Set("Content-type", "application/json")` a después del bloque
`if err != nil { return nil, err }`. Es la corrección de un simple orden invertido,
pero es la que evita el nil-pointer dereference.

### Decisión 5: Verificación mediante tests de tabla con `httptest`

La función recibe la URL del nodo como parámetro, así que se puede apuntar a un
`httptest.Server` sin refactorizar nada. Se añade `service/httpCall_test.go` con un
caso por escenario de la spec: éxito, error JSON-RPC, resultado vacío, resultado
malformado y nodo caído. Los casos de fallo afirman `err != nil` y, sobre todo, que
la llamada retorna en lugar de hacer panic.

## Risks / Trade-offs

- **Riesgo bajo de cambio de comportamiento en producción:** casos que antes hacían
  panic ahora devuelven error. Es exactamente lo buscado, pero significa que un
  despliegue con un proxy mal configurado fallará ahora con un mensaje de
  configuración en vez de un stack trace. Es una mejora, no una regresión.
- **`Hex2Bytes` sigue siendo tolerante:** ignora caracteres no hexadecimales en vez
  de fallar. Por eso la validación previa con `common.IsHexAddress` o una
  comprobación de longitud es parte del cambio y no un extra opcional.
- **El timeout sigue siendo un parámetro sin unidad (`_timeout int` en segundos).**
  No se toca en este cambio para mantener la firma estable; queda anotado como
  posible limpieza futura.
