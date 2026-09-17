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

## Configuration

The keys below were added by the `01`..`05` changes (event bus, HTTP endpoints, dashboard, nonce
reordering and metatx validation). Every one of them is **optional**: when a key is absent the
service applies the default listed here, and with all defaults in place it behaves exactly as it
did before those changes. That is why `config.toml` ships them commented out — a commented key
follows the binary's default and picks up a new one on upgrade, while an explicit key freezes the
value on every node the template is copied to.

Absence is meaningful: the blocks are read key by key, so an invalid value falls back to its
default and is logged instead of aborting startup, and a key written with its default value is not
the same as a missing key (see `dashboard.bufferSize`).

### `[reorder]` — nonce tracking, reordering and receipt watching

| Key | Default | What it does |
|---|---|---|
| `enabled` | `false` | Turns on the authoritative nonce tracker, the hold-and-reorder buffer and the receipt watcher. With it off none of the three runs. **Observable change when on:** `eth_sendRawTransaction` for an out-of-order metatx does not answer until its turn comes or the window expires. |
| `windowMs` | `3000` | How long a metatx may stay held **without the expected nonce advancing**. It measures stalling, not total wait: it is renewed every time the chain moves forward, so a long burst does not lose its tail to the clock. Also the grace period before forgetting a user with nothing in flight. |
| `maxInflightPerUser` | `16` | Cap of metatx of the same user in flight or held. Bounds the damage when a nonce chain breaks. **The network imposes a lower ceiling:** Besu limits how many pending transactions it accepts from a single account (`tx-pool-limit-by-account-percentage`, ~5 with defaults) and that account is the writer node, sender of every wrapper transaction. Measured on pro-testnet: bursts of 5 go through, the sixth is rejected by Besu. Raising this above that ceiling does nothing until the validators raise theirs. |
| `receiptTimeoutMs` | `60000` | How long the result of a sent metatx is awaited before declaring it undetermined and releasing its slot. |
| `autoNonce` | `false` | Hands out nonces: queries from the same user are serialised and each one gets a different number. Off, querying reserves nothing and two clients asking at once get the same value. |
| `autoNonceTicketMs` | `2000` | How long a handed-out nonce waits for the metatx that uses it. It is a **ticket, not a reservation**: if it expires unused the next caller gets that same number, so a client that asks and never sends does not block the queue. |

### `[validation]` — gas model suffix

Rejects, **before spending a writer node transaction**, what the RelayHub would reject on-chain.
Both switches default to `false`, the opposite of the reference relayer: turning them on changes
which metatx are accepted. A suffix that is absent, short or carries an unrepresentable expiration
never rejects anything — what is validated is a present, readable suffix that says something
unacceptable.

| Key | Default | What it does |
|---|---|---|
| `enforceNodeAddress` | `false` | Rejects a metatx whose embedded node address is not this service (`WRONG_NODE_ADDRESS`). |
| `enforceExpiration` | `false` | Rejects an expired metatx (`EXPIRED`) and one arriving without the minimum window (`EXPIRATION_TOO_LOW`). |
| `minExpirationSeconds` | `300` | Minimum validity a metatx must still have on arrival. An expiration on the edge is useless: validating, waiting for the nonce turn and mining take seconds. Only applies with `enforceExpiration`. |
| `expirationToleranceSeconds` | `2` | Tolerance the minimum is applied with — the real floor is minimum minus this. Whoever signs `now + 300` arrives with 298 (request latency plus second rounding on both sides), and demanding the exact value would reject precisely the client that did the right thing. `0` demands the exact value. |

While `enforceExpiration` is off, `GET /info` reports both numbers as **zero**: publishing a value
that is not applied would make a client sign to satisfy a rule that does not exist.

### `[security]` — where the account rules contract comes from

These two join the pre-existing `permissionsEnabled` and `accountContractAddress`.

| Key | Default | What it does |
|---|---|---|
| `accountIngressAddress` | *(empty)* | Permissioning registry the rules contract is resolved from **when `accountContractAddress` is not set**. The configured address takes precedence, so an existing deployment does not change behaviour on upgrade. Empty means no registry is queried. `GET /info` reports which of the two sources was used. |
| `accountRulesCacheMs` | `30000` | How long a per-account permission result stays valid. Without a cache every metatx pays a chain call; with it, granting an account takes up to this long to be seen. |

### `[dashboard]` — live monitor

| Key | Default | What it does |
|---|---|---|
| `enabled` | `false` | Publishes events to the in-memory bus and registers `GET /dashboard` and `GET /dashboard/stream`. With it off those routes are **not registered** at all. The monitor does not authenticate and exposes the `from`, hashes and gas of every metatx, which is why it is off by default. |
| `bufferSize` | `500` | Events retained for a late subscriber. An explicit `0` leaves the bus with no capacity — inert, same as `enabled = false`. This is the one key where zero has its own meaning rather than being an unwritten default. |

### `[log]` — structured log

| Key | Default | What it does |
|---|---|---|
| `level` | `"info"` | Minimum level written to standard output: `debug`, `info`, `warn` or `error`. |
| `rawTx` | `false` | Dumps the full signed transaction in `relay.received`. Off by default: a deploy is several KB of initcode and long lines get truncated, losing the rest of the event. The raw tx is still identified by its hash and size. |

### `[cors]` — cross-origin headers

| Key | Default | What it does |
|---|---|---|
| `allowedOrigins` | *(empty)* | Origins allowed to call the service from a browser. Empty emits **no** header at all, which is how the service has always behaved. Closed by default matters: the service relays with the node's gas quota and asks for no authentication, so opening it is the operator's decision and not the binary's. `"*"` allows anyone. |

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
| `relayHubSource` (`/info`) | `config` o `proxy` | siempre `proxy` | la direccion siempre se resuelve del proxy |
| `reorderEnabled`, `receiptTimeoutMs` (`/info`) | no existen | presentes | parametros propios de este servicio |
| `simulated` (`/relay`) | segun hubo pre-chequeo | siempre `false` | no hay pre-chequeo por simulacion con `eth_call` |
| `errorCode` (`/relay`) | de la simulacion o del hub | solo del hub | idem |
| `output` (`/relay`) | return data, o el motivo del revert | el motivo del revert, o sin valor | el return data de una llamada exitosa todavia no se expone |

De los trece codigos de error del catalogo de Node, este servicio produce doce: `BAD_RAW_TX`,
`BAD_META_TX`, `BAD_NONCE`, `WRONG_NODE_ADDRESS`, `EXPIRED`, `EXPIRATION_TOO_LOW`,
`TOO_MANY_INFLIGHT`, `SENDER_NOT_PERMITTED`, `PERMISSIONING_UNAVAILABLE`, `SEND_FAILED`,
`RECEIPT_TIMEOUT` y `RELAY_ERROR`. El que falta es `SIMULATION_FAILED`, que corresponde al
pre-chequeo por simulacion, todavia inexistente. Lo que no tiene codigo propio usa `RELAY_ERROR`:
no se inventan codigos fuera del catalogo.

### Validacion del sufijo del modelo de gas

El modelo de gas agrega al final del `data` de la metatx la direccion del nodo que tiene que
relayarla y su expiracion. Con `validation.enforceNodeAddress` el servicio rechaza la metatx
dirigida a **otro** writer node, y con `validation.enforceExpiration` la vencida y la que llega sin
la ventana minima. Los dos rechazos ocurren **antes** de gastar una transaccion del writer node: sin
ellos, la descubre el hub on-chain con la transaccion ya consumida.

El minimo se exige con tolerancia (`expirationToleranceSeconds`, 2 s por defecto). Quien firma
`ahora + 300` llega con 298 -se pierde la latencia de la peticion y el redondeo a segundos de cada
lado-, asi que exigir el valor exacto rechazaria justo al cliente que hizo lo correcto.

Las dos exigencias arrancan **apagadas**, al reves que en el relayer de Node: encenderlas cambia que
metatx se aceptan. Con las dos en `false`, el servicio acepta exactamente lo mismo que antes. Por lo
mismo, `GET /info` informa `minExpirationSeconds` y `expirationToleranceSeconds` en cero mientras la
exigencia este apagada: publicar un numero que no se aplica haria que un cliente firme para cumplir
una regla que no existe.

Un sufijo ausente, corto o con una expiracion que no entra en un entero **no** rechaza nada: lo que
se valida es un sufijo presente y legible que dice algo inaceptable.

### De donde sale el contrato de reglas

Si `security.accountContractAddress` esta configurada, se usa esa -es lo que hace un despliegue
actual, y por eso actualizar el binario no le cambia el comportamiento-. Si no lo esta, se resuelve
del registro de permisos de la red (`security.accountIngressAddress`). `GET /info` informa cual de
las dos fuentes se uso: una direccion equivocada es indistinguible de una correcta si no se sabe de
donde salio.

Una red que no expone contrato de reglas -sin registro, o sin contrato publicado- simplemente no
tiene permisionado de cuentas, y el servicio arranca y relaya igual. Pero si `permissionsEnabled`
esta en `true` y **no** se pudo resolver ninguno, la metatx se rechaza con
`PERMISSIONING_UNAVAILABLE`: si alguien pidio el chequeo y no hay nada contra que chequear, dejar
pasar seria abrir la puerta creyendo lo contrario.

El resultado del chequeo se cachea por cuenta (`security.accountRulesCacheMs`, 30 s por defecto).
El intercambio es explicito: sin cache, cada metatx paga una consulta a la cadena; con cache, dar de
alta una cuenta tarda hasta ese plazo en verse.

Al arrancar se comprueba ademas si **este** nodo esta permitido y se informa en `GET /info`. No
impide arrancar: un nodo no permitido relaya sin error aparente y todas sus metatx fallan on-chain,
y eso se diagnostica mejor viendolo en `/info` que con un binario que no levanta.

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
