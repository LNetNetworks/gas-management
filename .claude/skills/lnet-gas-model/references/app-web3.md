# Apps web3 sobre LNet: ethers v6 + `@lacchain/gas-model-provider`

Cómo construir el **lado cliente** (deploy, envío de tx y manejo de errores) de una app sobre el gas
model LACChain. El contrato está en `desarrollo-contratos.md`; el detalle de firma/red en
`gas-model.md`.

## 1. Stack y qué hace la librería

```bash
npm i ethers@^6.13 @lacchain/gas-model-provider@^1.2.1 dotenv
```

| Clase | Extiende | Qué hace |
|---|---|---|
| `LacchainProvider` | `JsonRpcProvider` | fuerza `batchMaxSize: 1`, limpia el campo `root` del receipt (ethers se queja) y fija `confirmations = 1` para que `tx.wait()` no se cuelgue |
| `LacchainSigner` | `Wallet` | firma la metatx: `chainId = 0` (**legacy pre-EIP155**, `v = 27/28`) y añade `abi.encode(nodeAddress, expiration)` — 64 bytes — al final del `data` |

```js
const { ethers } = require("ethers");
const { LacchainProvider, LacchainSigner } = require("@lacchain/gas-model-provider");

const provider = new LacchainProvider(process.env.RPC_URL);
// expiration en MILISEGUNDOS (Date.now() + margen)
const signer = new LacchainSigner(PRIVATE_KEY, provider, NODE_ADDRESS, Date.now() + 10 * 60 * 1000);
```

Como el signer extiende `Wallet`, todo lo normal de ethers funciona: `signer.address`,
`new ethers.Contract(addr, abi, signer)`, `ContractFactory`, `tx.wait()`.

## 2. Arquitectura: el navegador no puede firmar

**MetaMask y cualquier wallet de navegador no sirven** para escribir en LNet: no firman con
`chainId = 0` (lo rechazan) ni añaden `nodeAddress`/`expiration`. La firma la tiene que hacer
`LacchainSigner` con una clave privada, es decir **en el servidor** (o un KMS/HSM):

```
navegador (React/Vue)  --HTTP-->  backend con LacchainSigner  --eth_sendRawTransaction-->  relay-signer --> RelayHub --> contrato
        ^                                                                                                    |
        └── lecturas (view) pueden ir directas al RPC con un provider normal ────────────────────────────────┘
```

- **Lecturas** (`view`/`pure`): sin relay, sin gas, sin firma. Se pueden hacer desde el front.
- **Escrituras**: siempre por el backend. `templates/server.js` es un backend mínimo con este
  patrón (endpoints de lectura y de transferencia relayada, errores del gas model tipados).
- Es el problema que resuelve un relay multi-tenant con autenticación, para no tener que operar
  un nodo por cliente.

## 3. El RPC tiene que ser el endpoint QUE RELAYA

| Endpoint | Sirve para |
|---|---|
| `http://<writer>` (nginx :80 → relay-signer :9001) | **escribir** — enruta `eth_sendRawTransaction` al relay-signer, que llama `RelayHub.relayMetaTx` |
| `http://<writer>:4545` (besu directo) | solo **leer**: no relaya, las escrituras no se ejecutan |

**Regla práctica: si te pasan solo una IP, el RPC es `http://<IP>` — puerto 80, sin puerto
explícito.** El nginx/openresty del writer enruta *por método* (`eth_sendRawTransaction`,
`eth_getTransactionCount`, `eth_getTransactionReceipt`, `eea_sendRawTransaction`,
`relay_getMetaTxResult` → relay-signer `:9001`; el resto → besu `:4545`), así que el mismo
endpoint sirve para leer y para escribir.

Es el error más frecuente al empezar: con el `:4545` la tx "se manda" y nunca pasa nada. Y cuando
el desfase entre el nonce de metatx y el nonce EVM de la cuenta supera
`--tx-pool-max-future-by-sender` (200 por defecto), besu lo rechaza con
`Transaction nonce is too distant from current sender nonce`.
Algunos despliegues ponen delante un **router RPC** que enruta por método, y los métodos `relay_*`
pueden no estar expuestos (responden `-32601`) — ver §6.

## 4. Reglas de cada llamada

```js
// SIEMPRE: gasPrice 0 y gasLimit explícito (no hay eth_estimateGas bajo el gas model)
await token.transfer(to, amount, { gasLimit: 1_500_000, gasPrice: 0 });
```

- **`gasPrice: 0`** — la red es gasless para el usuario.
- **`gasLimit` explícito** — `eth_estimateGas` no funciona en creación; en llamadas conviene fijarlo
  igual. Ojo: el `gasLimit` firmado se compara contra la **ventana de gas del nodo** (`basic` 500k,
  `standard` 1M, `premium` 10M…) → pedir 20M en un nodo `standard` da `NotEnoughGas`, no un fallo de
  estimación.
- **`data` obligatorio si mandas una tx "pelada"**: el provider construye
  `data + abi.encode(node, expiration)`; con `{to, value}` y sin `data` el resultado es la cadena
  `"undefined…"` y ethers lanza `invalid BytesLike value` (verificado con 1.2.1). Pasa `data: "0x"`.
  De todas formas el gas model no transporta `value`: mueve valor con un ERC20.
- **Signer fresco por operación**: la expiración se congela **al construir** el `LacchainSigner`
  (`_aExpirationTime`), así que un signer de larga vida en un servidor acaba firmando metatxs ya
  vencidas. Crea uno por tx (o cada pocos minutos) — es lo que hace `templates/lacchain.js`.
- **Minado síncrono**: cada escritura tarda **1–3 min**. No bloquees una petición HTTP con eso: encola
  y devuelve `202` + un id que el front consulte.
- **Una metatx a la vez por cuenta firmante.** El nonce que valida el RelayHub es **por par
  (nodo, usuario)**; dos envíos en paralelo leen el mismo `eth_getTransactionCount` "pending" y el
  segundo sale `BadNonce`. Serializa por clave firmante (ver la cola de `templates/server.js`).

## 5. Deploy desde la app

Compila con Foundry (`evm_version = "paris"`) y despliega con el signer de LACChain:

```js
const artifact = require("../out/MiToken.sol/MiToken.json");
const factory = new ethers.ContractFactory(artifact.abi, artifact.bytecode.object, signer);
const contract = await factory.deploy({ gasLimit: 8_000_000, gasPrice: 0 }); // constructor SIN args
const receipt = await contract.deploymentTransaction().wait();
```

El receipt trae `contractAddress = 0x0` (el `CREATE` lo hace el RelayHub) → la dirección se lee del
trace con `findCreatedAddress` (`templates/lacchain.js`, `templates/deploy.js`). Si el nodo no expone
`debug_traceTransaction`, el relay puede darla en `relay_getMetaTxResult.deployedAddress`.

## 6. Manejo de errores: los tres niveles

Un `status = 1` en el receipt **no** garantiza que la operación del usuario se ejecutara: la tx
externa es la del nodo al RelayHub. Hay que mirar tres niveles:

> ⚠️ **`tx.wait()` rechaza también en los casos (b) y (c)**, con un genérico
> `transaction execution reverted`, porque el receipt trae `status = 0`. Un `try/catch` que trate esa
> excepción como "no se pudo enviar" **pierde el `ErrorCode`**: en el `catch`, si ya tienes `txHash`,
> consulta el resultado de la metatx antes de diagnosticar (es lo que hace `sendRelayed`).
> Verificado en el nodo dev con un `BadNonce` provocado: `wait()` lanza
> `transaction execution reverted`, y el receipt/relay dicen
> `status = 0`, `revertReason = "BadTransactionSent: BadNonce"`, `errorCode = "BadNonce"`,
> `executed = false`.

**(a) Rechazo del relay-signer** — antes de llegar al RelayHub, llega como error JSON-RPC en el
`send`/`wait` (`e.info.error.code`):

| Código | Significado |
|---|---|
| `-32000 transaction gas limit exceeds block gas limit` | `gasLimit` > ventana del nodo |
| `-32007 sender not authorized` | cuenta o nodo no permisionado |
| `-32601` | método no expuesto por ese endpoint (p. ej. `relay_*` detrás de un rpc-router) |
| `nonce too low` / `known transaction` | colisión de nonce en el pool |

**(b) El RelayHub rechazó la metatx** — emite `BadTransactionSent(node, from, errorCode)` y la
llamada nunca llega al contrato. Los relay-signer modernos (≥ `v1.1.0-RC1`) ya lo reflejan como
`status = 0` + `revertReason = "BadTransactionSent: <errorCode>"`. El enum:

| # | Código | Cuándo |
|---|---|---|
| 0 | `MaxBlockGasLimit` | `gasLimit` firmado > `maxGasBlockLimit` (120M). El relay lo corta antes → casi inalcanzable |
| 1 | `BadOriginalSender` | el sender recuperado de la firma no es válido |
| 2 | `BadNonce` | nonce ≠ `nonces[node][user]` del RelayHub → **el caso realista en apps** (concurrencia) |
| 3 | `NotEnoughGas` | `gasLimit` > ventana de gas del nodo relayer |
| 4 | `IsNotContract` | el `to` no tiene bytecode (mandaste a una EOA) |
| 5 | `EmptyCode` | el `CREATE` interno no dejó bytecode |
| 6 | `InvalidSignature` | `ECDSA.recover` falló (s alto, `v ∉ {27,28}`) |
| 7 | `InvalidDestination` | destino inválido (`trustedAccountIngress` / `address(0)`) |
| **8** | **`OK`** | **éxito** — no es un error |

**(c) El contrato revirtió** — la metatx llegó al destino pero la lógica falló: sale por
`TransactionRelayed(..., executed = false, output)`, con el `output` del revert (OpenZeppelin v5 usa
*custom errors*: hay que decodificarlos del `output`).

Cómo consultarlo (`templates/lacchain.js` → `getMetaTxResult`):

```js
const rcpt = await provider.send("eth_getTransactionReceipt", [txHash]);
const status = rcpt?.status != null ? parseInt(rcpt.status, 16) : null;
const revertReason = rcpt?.revertReason ?? null;
// detalle parseado del relay: { mined, success, executed, errorCode, revertReason, deployedAddress }
const meta = await provider.send("relay_getMetaTxResult", [txHash]); // puede dar -32601
```

El `errorCode` de `relay_getMetaTxResult` llega como **nombre** (`"BadNonce"`), no como índice, en
los relay-signer actuales; `ERROR_CODES` / `ERROR_CODE_BY_NAME` de `templates/lacchain.js` traducen en
los dos sentidos. En una tx correcta ese campo es `null` (no `8`) y el éxito se ve como
`status = 1`, `success = true`, `executed = true`.

`templates/lacchain.js` envuelve esto en `sendRelayed`, que **solo resuelve si la metatx se ejecutó
de verdad** y si no lanza un `RelayError` con `{ stage, errorName, errorCode, revertReason, rpcCode,
txHash, meta }` — `stage: "send"` = rechazo del relay, `stage: "relay"` = fallo del RelayHub.

## 7. Plantillas

| Archivo | Qué es |
|---|---|
| `templates/lacchain.js` | módulo reutilizable: `makeProvider`, `makeSigner` (expiración fresca), `readContract`, `sendRelayed` (espera + errores tipados), `deployRelayed`, `getMetaTxResult`, `findCreatedAddress`, `ERROR_CODES` |
| `templates/send-tx.js` | CLI de una transferencia relayada con manejo de los tres niveles de error |
| `templates/server.js` | backend HTTP mínimo (lectura directa + escritura relayada, cola por clave, errores tipados en JSON) |
| `templates/deploy.js`, `deploy-upgradeable.js`, `update-upgradeable.js` | deploy y upgrade UUPS |
| `templates/package.json`, `.env.example` | dependencias y variables (`RPC_URL`, `PRIVATE_KEY`, `NODE_ADDRESS`, `CHAIN_ID`, `GAS_PRICE=0`, `EXPIRATION_MS`) |

