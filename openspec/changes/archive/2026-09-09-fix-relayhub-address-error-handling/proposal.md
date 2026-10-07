## Why

`getRelayHubContractAddress` resuelve la dirección del contrato RelayHub durante el
arranque del servicio, pero maneja mal los errores: valida el error de
`http.NewRequest` después de usar el `*http.Request`, ignora el campo `error` de la
respuesta JSON-RPC, y ante fallos de ABI imprime el error y continúa la ejecución.
Como consecuencia, un nodo caído o una respuesta RPC inválida producen un **panic en
el arranque** en lugar del error limpio (`FailedKeyConfig`, código `-32610`) que el
llamador en `service/relaySignerService.go:88` ya sabe manejar.

## What Changes

- Validar el error de `http.NewRequest` **antes** de invocar `request.Header.Set`
  (hoy se usa el request potencialmente `nil` antes de comprobar el error).
- Inspeccionar `rpcMessage.Error` y devolver un error descriptivo cuando el nodo
  responde con un error JSON-RPC, en vez de continuar con un `Result` vacío.
- Decodificar `Result` con `json.Unmarshal` a `string` en lugar del slicing
  posicional `resultData[3 : len(resultData)-1]`, que hace panic cuando el resultado
  viene vacío o corto.
- Validar que el resultado sea una dirección hexadecimal válida antes de convertirla.
- Reemplazar los dos `fmt.Println(err)` de las operaciones ABI por `return nil, err`,
  para que el error corte el flujo en vez de arrastrarse hasta un panic.
- Verificar la longitud de `addressUnpacked` y hacer la type assertion a
  `common.Address` con la forma comprobada (`v, ok := ...`).
- Eliminar el `fmt.Println(data)` de depuración, que filtra el payload JSON-RPC a
  stdout fuera del logger de auditoría del proyecto.

## Capabilities

### New Capabilities
- `relayhub-address-resolution`: resolución de la dirección del contrato RelayHub a
  partir de su proxy vía `eth_call`, con manejo de errores que siempre devuelve un
  error al llamador y nunca hace panic durante el arranque.

### Modified Capabilities
<!-- Ninguna: no existen specs previas bajo openspec/specs/ -->

## Impact

- `service/httpCall.go`: reescritura del manejo de errores de
  `getRelayHubContractAddress` (~25 líneas). Sin cambios de firma.
- `service/relaySignerService.go:88`: sin cambios de código. El llamador ya envuelve
  el error en `FailedKeyConfig`; con este cambio efectivamente lo recibirá en lugar
  de morir por panic.
- Sin cambios en dependencias ni en la API JSON-RPC pública.
