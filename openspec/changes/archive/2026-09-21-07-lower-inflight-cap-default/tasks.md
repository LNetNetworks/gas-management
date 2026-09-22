## 1. El default

- [x] 1.1 Cambiar `DefaultReorderMaxInflightPerUser` de 16 a 5 en `model/RuntimeConfig.go`, y
  verificar con los tests de `model/RuntimeConfig_test.go` que la config vacia toma el nuevo valor
- [x] 1.2 Verificar que ningun test dependa del 16 como default -los que fijan el cupo a proposito
  siguen valiendo-, y correr `go build ./...` y `go test ./... -race`

## 2. Documentacion

- [x] 2.1 Actualizar la entrada de `maxInflightPerUser` en `config.toml` y en el README: el valor de
  ejemplo pasa a 5, y el texto explica el CRITERIO -igualar el cupo al techo de la red- y no el
  numero, que depende de la configuracion de Besu de cada red
- [x] 2.2 Dejar registrada la evidencia: un cupo por encima del techo no agrega minadas y convierte
  rechazos rapidos por tope en rechazos lentos por nonce. Verificar que el README no sugiera que
  subirlo mejora el rendimiento
