# Gas Management

This solution is in charge of distributing gas to the different LACChain Besu writer nodes, it is composed of backend components such as smart contracts. Gas distribution is automatic, whose logic is written in smart contracts. 

## Package overview

1. **audit** contains ways to log.
2. **blockchain** contains connections to Ethereum.
3. **controller** controller layer that receives all external requests and redirects requests to the service layer
4. **service** contains main logic
5. **model** contains data models of requests and responses of APIs
6. **errors** contains different errors types
7. **relayhub** contains all smart contract 
8. **rpc** contains models and ways to interact with RPC request and response
9. **docs** contains documentation about architecture and developer interaction with this 
solution

## Prerequisites

* Being a validator node in LACChain network
* Go 1.13+ installation or later
* **GOPATH** environment variable is set correctly

## Install

```
$ git clone https://github.com/lacchain/gas-management

$ cd gas-management
$ make build      # compila con la versión inyectada desde el tag git (ver "Versión")
```

> `go build` a secas también funciona, pero deja la versión en `dev`. Usa `make build`
> (o los `-ldflags`) para que el binario reporte la versión real.

## Run

Execute the executable file generated previously in a Validator node

```
$ ./gas-relay-signer
```

## Versión

El binario reporta su versión:

```
$ ./gas-relay-signer --version
gas-relay-signer v1.1.0 (commit 5b7a3a7, built 2026-07-01T22:48:36Z, go1.23.0)
```

La versión es el **tag git** (`git describe --tags`), inyectado en compilación vía
`-ldflags "-X main.version=... -X main.commit=... -X main.date=..."` (lo hace `make build`).
Compilado en el tag `v1.1.0` reporta `v1.1.0`; en `develop` sin tag, algo como
`v1.0.1-9-g5b7a3a7`. Sin `ldflags` reporta `dev`.

### Publicar un release (manual)

1. Mergear `develop` → `master` (PR) y situarse en `master` actualizado.
2. Crear el tag anotado y empujarlo:
   ```
   git tag -a v1.1.0 -m "gas-relay-signer v1.1.0"
   git push origin v1.1.0
   ```
3. Compilar el artefacto con la versión inyectada y publicar el release:
   ```
   make build VERSION=v1.1.0
   gh release create v1.1.0 gas-relay-signer --title "v1.1.0" --notes "..."
   ```

## Rutas HTTP

Ademas del catch-all JSON-RPC de `POST /`, que no cambia, el servicio expone tres rutas REST.

**No son alcanzables por el puerto 80**: el nginx del writer node enruta por metodo leyendo el
cuerpo, asi que quedan en el puerto del servicio (`:9001`), accesible por la red interna o por un
tunel SSH. Abrirlas exige tocar la plantilla de `besu-networks`. Como el resto del servicio, no
piden autenticacion: `GET /info` revela direcciones y el balance del nodo, y `POST /relay` consume
su cupo de gas igual que el camino JSON-RPC.

```bash
curl -s http://localhost:9001/info
curl -s http://localhost:9001/nonce/0xAbC...
curl -s "http://localhost:9001/nonce/0xAbC...?peek=true"
curl -s -X POST http://localhost:9001/relay \
  -H 'content-type: application/json' -d '{"rawTx":"0xf8aa..."}'   # tambien acepta "signedTransaction"
```

### `GET /info`

Devuelve que direcciones esta usando este nodo, de donde salio cada una y con que parametros esta
operando. De aca sale el `relayHubProxyAddress` que va como `trustedForwarder` de los contratos.

Un dato que no se puede obtener se informa **sin valor** en lugar de omitirse, y la ruta responde
igual: es a la que se acude cuando algo anda mal, asi que no puede ser la primera en caerse.

### `GET /nonce/{address}`

`{address, nonce, nonceHex, nextNonce, nextNonceHex, pending}`. `nonce` es lo que dice el RelayHub;
`nextNonce` es con lo que hay que firmar ahora, contando las metatx ya relayadas y todavia sin
minarse. Encadenar sin `nextNonce` produce nonces repetidos.

`?peek=true` pide consultar sin tomar posicion en la cola. Con `reorder.autoNonce` apagado -el
default- no cambia la respuesta, porque no hay reparto que evitar. Con el reparto encendido, las
consultas del mismo usuario se serializan y cada una se lleva un numero distinto; `peek` mira sin
entrar en esa cola.

Lo que se entrega es un TURNO y no una reserva: consultar no adelanta por si solo el proximo nonce.
Un numero entregado y nunca usado no traba a nadie -al vencer `autoNonceTicketMs` el siguiente que
pregunte se lleva ese mismo numero-, y lo que hace que dos clientes se lleven numeros distintos es
que la metatx del primero llegue.

### `POST /relay`

Relaya la metatx y responde recien cuando se sabe como termino. Recorre exactamente las mismas
validaciones que `POST /`.

Un revert del contrato destino se responde con codigo de exito y `executed: false`: la metatx **si**
se relayo, lo que fallo fue el destino. Un rechazo responde `400` con `{error, code, details}`.

El vencimiento de la espera responde `RECEIPT_TIMEOUT` con el hash: la metatx **se envio** y puede
minarse despues. Tratarlo como un rechazo y reenviarla produce un nonce repetido.

### Diferencias con el relayer de Node

Las rutas son compatibles campo a campo salvo por lo siguiente, que viene de capacidades que este
servicio todavia no tiene:

| Campo | Node | Aca | Por que |
|---|---|---|---|
| `minExpirationSeconds`, `expirationToleranceSeconds` (`/info`) | la ventana exigida | siempre `0` | este servicio no valida la expiracion del modelo de gas |
| `relayHubSource` (`/info`) | `config` o `proxy` | siempre `proxy` | la direccion siempre se resuelve del proxy |
| `reorderEnabled`, `receiptTimeoutMs` (`/info`) | no existen | presentes | parametros propios de este servicio |
| `simulated` (`/relay`) | segun hubo pre-chequeo | siempre `false` | no hay pre-chequeo por simulacion con `eth_call` |
| `errorCode` (`/relay`) | de la simulacion o del hub | solo del hub | idem |
| `output` (`/relay`) | return data, o el motivo del revert | el motivo del revert, o sin valor | el return data de una llamada exitosa todavia no se expone |

De los trece codigos de error del catalogo de Node, este servicio produce nueve: `BAD_RAW_TX`,
`BAD_META_TX`, `BAD_NONCE`, `TOO_MANY_INFLIGHT`, `SENDER_NOT_PERMITTED`,
`PERMISSIONING_UNAVAILABLE`, `SEND_FAILED`, `RECEIPT_TIMEOUT` y `RELAY_ERROR`. Los cuatro que
faltan corresponden a validaciones que todavia no existen (expiracion, direccion del nodo,
simulacion). Lo que no tiene codigo propio usa `RELAY_ERROR`: no se inventan codigos fuera del
catalogo.

### Reordenamiento de nonces

Con `reorder.enabled = true` el servicio deja de mandar y esperar a ver que dice el hub:

- **Valida el nonce antes de enviar.** Uno ya consumido se rechaza con `BAD_NONCE` sin gastar una
  transaccion del writer node.
- **Retiene la metatx adelantada** hasta que se cierre el hueco, en vez de gastarla para descubrir
  que llego fuera de orden. La ventana mide **estancamiento**: se renueva cada vez que el nonce
  esperado avanza, asi que una rafaga larga no pierde la cola por reloj. Al vencer se responde el
  mismo `BAD_NONCE`, sin haber enviado nada.
- **Acota la rafaga por usuario** con `maxInflightPerUser`: al superarlo, `TOO_MANY_INFLIGHT`.
- **Detecta como termino cada metatx** sin que el cliente pregunte, y libera su lugar.

Semantica observable que cambia con el flag encendido: `eth_sendRawTransaction` de una metatx
adelantada **no responde** hasta que le toca el turno o vence la ventana. Es inherente a reordenar
sobre HTTP y es lo que hace el relayer de Node.

Con el flag apagado -el default- nada de esto corre y el comportamiento es el de siempre.

## Know More

* [In depth overview of the GAS distribution mechanism](https://github.com/LACNetNetworks/gas-management/blob/master/docs/OVERVIEW.md)
* [How to adapt you solution to the GAS distribution mechanism](https://github.com/LACNetNetworks/gas-management/blob/master/docs/How_adapt_your_Dapp.md)
* [Deploy your first ERC20 and time-stamping (notarization) smart contracts](https://github.com/LACNetNetworks/gas-management/blob/master/docs/tutorial/Deploy_SmartContract.md)
* [Deploy and interact with the LACChain ID verifiable credential registry smart contract](https://github.com/LACNetNetworks/gas-management/blob/master/docs/tutorial/VC_en.md)
* [Stress testing and performance of the network with the GAS distribution mechanism](https://github.com/LACNetNetworks/gas-management/blob/master/docs/STRESS_TESTING.md)
* [Comparison with Ethereum](https://github.com/LACNetNetworks/gas-management/blob/master/docs/COMPARISON_WITH_ETHEREUM.md)
* [FAQ](https://github.com/LACNet-Networks/gas-management/blob/master/docs/FAQ.md)
* [Reporte del fallo de la llamada interna (status=1 → fallida)](docs/RECEIPT-FALLO-INTERNO.md) — cómo el RelaySigner reescribe el receipt a `status=0`+`revertReason` y expone `relay_getMetaTxResult` (rama `develop`).
* [Manejo del nonce (caché por sender y anti-bloqueo)](docs/NONCE-CACHE.md) — caché en memoria del próximo nonce por sender, y los 4 mecanismos que evitan que una address quede atascada tras una colisión `BadNonce` (rama `develop`).

## Copyright 2022 LACNet

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
