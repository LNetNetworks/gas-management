## 1. Corregir `getRelayHubContractAddress`

- [x] 1.1 Mover `request.Header.Set` después del chequeo `if err != nil` de `http.NewRequest` — verificar: `go vet ./service` limpio y lectura del diff
- [x] 1.2 Comprobar `rpcMessage.Error` tras decodificar y devolver un error con el mensaje del nodo — verificar: test `TestGetRelayHubContractAddress/error_jsonrpc` pasa
- [x] 1.3 Reemplazar el slicing `resultData[3 : len(resultData)-1]` por `json.Unmarshal` a `string` + validación de dirección hexadecimal — verificar: tests de resultado vacío y malformado retornan error sin panic
- [x] 1.4 Cambiar los dos `fmt.Println(err)` de ABI por `return nil, err` — verificar: `grep -n "fmt.Println" service/httpCall.go` no arroja resultados
- [x] 1.5 Verificar `len(addressUnpacked) > 0` y usar la type assertion comprobada `addr, ok := ...` — verificar: el caso malformado devuelve error en vez de panic
- [x] 1.6 Eliminar el `fmt.Println(data)` de depuración de la línea 21 — verificar: mismo `grep` de 1.4 sin resultados

## 2. Tests

- [x] 2.1 Crear `service/httpCall_test.go` con un test de tabla sobre `httptest.Server`, un caso por escenario de la spec: éxito, error JSON-RPC, `result` vacío, `result` malformado y nodo caído — verificar: `go test ./service -run TestGetRelayHubContractAddress -v` pasa los 5 casos

## 3. Verificación de integración

- [x] 3.1 Confirmar que la ruta de arranque completa degrada de forma controlada: con un nodo inalcanzable, `relaySignerService.go:88` devuelve `FailedKeyConfig`/`-32610` sin stack trace de panic — verificar: `go build ./... && go test ./service` en verde y revisión de la ruta del llamador
