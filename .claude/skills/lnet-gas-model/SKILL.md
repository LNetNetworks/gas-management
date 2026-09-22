---
name: lnet-gas-model
description: Conocimiento del gas model LACChain de la red privada LNet. Usar SIEMPRE que se desarrollen, compilen, prueben o desplieguen smart contracts para LNet / LACChain — gas model, RelayHub, trustedForwarder, nodeAddress, expirationDate, firma pre-EIP155, PUSH0, evm_version paris, _msgSender, BaseRelayRecipient, @lacchain/gas-model-provider, Besu permisionada, UUPS sobre LNet.
---

# Gas model LACChain (red LNet)

En LNet el gas lo paga el **nodo relayer**, no el remitente. Cada transacción lleva
`nodeAddress` + `expirationDate`, se firma **legacy pre-EIP155** (`v=27/28`, `chainId=0`)
y la ejecuta un **RelayHub** (el `CREATE`/call real es una transacción interna).
Todo lo demás de esta skill se deriva de ese hecho.

## Datos de la red

El `trustedForwarder` se elige según la red; el resto del flujo es idéntico.

| Parámetro | **Testnet (open pro-testnet)** | **Mainnet (omega)** |
|---|---|---|
| Chain ID | `648540` | `648541` |
| RPC (gas model) | **puerto 80** de tu writer: `http://<IP>` (nginx → relay-signer) | ídem, contra tu nodo de mainnet |
| Cliente / EVM | Besu permisionada, gas `0`, **EVM Paris (sin `PUSH0`)** | igual |
| trustedForwarder (`BaseRelayRecipientProxy`) | `0xa4B5eE2906090ce2cDbf5dfff944db26f397037D` | `0xEAA5420AF59305c5ecacCB38fcDe70198001d147` |
| RelayHub | `0xB9e9C5C528C266f2A1C7Eeec1975595232C8E475` | `0x34B220Ef63dea567eDf6d540316B5a3f4831Cf6c` |

Direcciones on-chain completas: el catálogo de direcciones de tu red.
Docs oficiales: https://docs.l-net.io/en/Deploy-Contract

## Las 7 reglas no negociables

1. **`evm_version = "paris"` en `foundry.toml` (o `evmVersion` en Hardhat).** La EVM de
   LNet no conoce `PUSH0` (Shanghai/EIP-3855); con bytecode Shanghai el `CREATE` interno
   falla con `INVALID` aunque sobre gas. Ver `templates/foundry.toml`.

2. **Foundry/cast NO pueden hacer broadcast.** `forge`/`cast` firman estilo EIP-155
   (con `chainId=0` producen `v=36`); el RelayHub espera `v=27/28` y recupera una
   dirección equivocada. Flujo híbrido obligatorio: **Foundry compila → un script Node
   con `@lacchain/gas-model-provider` (`LacchainProvider` + `LacchainSigner`) hace el
   broadcast.** Ver `templates/deploy.js` y `templates/deploy.sh`.

3. **Usar `_msgSender()`, nunca `msg.sender`.** Las llamadas llegan vía RelayHub, así
   que `msg.sender` sería el RelayHub. Todo contrato hereda `BaseRelayRecipient`
   (no-upgradeable, forwarder hardcodeado) o `BaseRelayRecipientUpgradeable` (el
   forwarder se fija en `initialize` vía `__BaseRelayRecipient_init`; inline no
   persiste en proxy storage; reserva `__gap`). Con OpenZeppelin hay conflicto de
   `_msgSender()` con `Context` — se resuelve con
   `override(BaseRelayRecipient, Context)` usando `staticcall` (compatible `view`).
   Hay una tercera variante con el forwarder inyectado por constructor
   (`templates/BaseRelayRecipientConfigurable.sol`) para desplegar en varias redes.
   Tampoco usar `tx.origin` (es el writer node), ni `msg.value`/`payable`, ni `_msgData()`.
   Ver `templates/BaseRelayRecipient*.sol` y `references/desarrollo-contratos.md`.

4. **Constructores sin argumentos.** El gas model añade 64 bytes
   (`abi.encode(nodeAddress, expirationDate)`) al final del calldata y corrompe la
   decodificación de constructor args. Derivar owner/estado inicial de `_msgSender()`
   o mover todo a `initialize()`. Excepción: el `ERC1967Proxy(impl, initData)` SÍ
   acepta args de constructor sin problema.

5. **La dirección del contrato sale del trace, no de `keccak(deployer, nonce)`.**
   El receipt suele traer `contractAddress = 0x0` porque el CREATE lo hace el RelayHub.
   Resolver leyendo el resultado del opcode `CREATE` en `debug_traceTransaction`
   (función `findCreatedAddress` en `templates/deploy.js`).

6. **El nodo relaya y mina síncrono: un deploy tarda 1–3 minutos.** No cancelar; los
   scripts imprimen heartbeat. Pasar `gasLimit` explícito y `gasPrice: 0`
   (`eth_estimateGas` no funciona para creación bajo el gas model).

7. **El RPC del gas model es el puerto 80, nunca el 4545.** En el writer, nginx/openresty
   escucha en **:80** y enruta *por método*: `eth_sendRawTransaction`, `eth_getTransactionCount`,
   `eth_getTransactionReceipt`, `eea_sendRawTransaction` y `relay_getMetaTxResult` van al
   **relay-signer `:9001`**; todo lo demás a besu `:4545`. **Si el cliente solo te da una IP, el
   RPC es `http://<IP>` (puerto 80, sin puerto explícito).** El `:4545` es besu directo: sirve
   para leer, pero no relaya — las escrituras quedan como *future* y nunca se minan, y
   `eth_getTransactionCount` devuelve el nonce EVM en vez del nonce de metatx.

## Cómo montar un proyecto nuevo para LNet

1. Copiar `templates/foundry.toml` (ajustar remappings) y `templates/deploy.sh`.
2. Crear `lacchain-deploy/` con `templates/package.json` + `deploy.js`
   (+ `deploy-upgradeable.js` y `update-upgradeable.js` si hay UUPS).
3. Copiar `templates/BaseRelayRecipient.sol` (y/o `BaseRelayRecipientUpgradeable.sol`)
   a `src/` y heredar de él en los contratos, sobreescribiendo `_msgSender()`.
4. `.env` a partir de `templates/.env.example`: `RPC_URL`, `PRIVATE_KEY`,
   `NODE_ADDRESS` (en el servidor del nodo: `cat nodeAddress`), `CHAIN_ID`,
   `GAS_PRICE=0`. Opcionales: `EXPIRATION_MS` (¡milisegundos!), `GAS_LIMIT`, `CONTRACT`.
5. Desplegar: `./deploy.sh` (simple) o `./deploy.sh MiContratoUpgradeable` (UUPS:
   implementación + `ERC1967Proxy`; **interactuar siempre con el PROXY**).
6. Upgrade UUPS: `forge build` y luego
   `cd lacchain-deploy && PROXY=0x<proxy> node update-upgradeable.js`
   (despliega nueva impl, llama `upgradeToAndCall(impl, "0x")` como owner y verifica el
   slot ERC1967). La nueva impl debe ser storage-compatible (variables nuevas al final,
   respetando `__gap`); reinicializar solo con `reinitializer`.

## Cómo escribir un contrato para LNet

Guía completa en `references/desarrollo-contratos.md` (variantes de herencia, conflicto con
`Context`, anti-patrones, checklist). Resumen:

1. Elegir variante de base según el contrato:
   - no-upgradeable con forwarder fijo → `templates/BaseRelayRecipient.sol`
     (ejemplo: `templates/GasModelToken.sol`, ERC20 de OZ v5).
   - forwarder por constructor (varias redes / relayer propio) →
     `templates/BaseRelayRecipientConfigurable.sol` (ejemplo: `templates/GasModelStorage.sol`).
   - proxy UUPS → `templates/BaseRelayRecipientUpgradeable.sol` + `__BaseRelayRecipient_init`
     en `initialize` (el forwarder inline NO persiste en el storage del proxy).
2. El `trustedForwarder` es el **BaseRelayRecipientProxy**, nunca el RelayHub: el hub no tiene
   `getRelayHub()`, el `abi.decode` revierte y el constructor se cae (deploy a `0x0`).
3. Sustituir todo `msg.sender` por `_msgSender()` y emitirlo en los eventos (el `from` del
   receipt es el writer node). Con OZ, sobreescribir
   `_msgSender() internal view override(BaseRelayRecipient, Context)` — así `transfer`,
   `approve`, `burn`, `onlyOwner` y `AccessControl` ya ven al usuario relayado.
4. Constructor sin args (o solo estáticos) por los 64 bytes extra; en upgradeable, todo el
   estado en `initialize`.
5. Test con `templates/GasModelToken.t.sol` como esqueleto: `vm.etch` del mock y los dos
   caminos (directo y relayado).

Ejemplos de referencia: pide al equipo de LNet los repos de muestra (token ERC20 simple y
upgradeable, catálogo de `ErrorCode`, relayer propio) si necesitas ver todo esto montado.

### Consideraciones al desarrollar un contrato (checklist)

| ✅ Hacer | ❌ No hacer |
|---|---|
| heredar `BaseRelayRecipient` con el **proxy** (`BaseRelayRecipientProxy`) de tu red | poner el RelayHub como `trustedForwarder` (no tiene `getRelayHub()` → deploy a `0x0`) |
| `_msgSender()` en auth, mappings, `require` y **eventos** | `msg.sender` (es el RelayHub) o `tx.origin` (es el writer node) |
| con OZ: `override(BaseRelayRecipient, Context)` y **`view`** (por eso `staticcall`) | dejar `Context._msgSender()` sin sobreescribir: `Ownable`/`ERC20` verían al RelayHub |
| constructor **sin argumentos**; estado inicial desde `_msgSender()` | args de constructor (los 64 bytes extra interfieren con el decode) |
| upgradeable: todo en `initialize` + `__BaseRelayRecipient_init` + `__gap` | fijar el forwarder inline en un upgradeable (no persiste en el storage del proxy) |
| mover valor con un ERC20 | `payable` / `msg.value` (la metatx no transporta valor) |
| `evm_version = "paris"` | compilar a Shanghai/Cancun (`PUSH0` → `CREATE` interno `INVALID`) |
| tests con `MockRelayForwarder` + `vm.etch`, probando camino directo y relayado | dar por bueno solo el camino directo |
| compilar con Foundry y **desplegar con Node** | `forge script` / `cast send` (firman EIP-155) |

Comprobación rápida: `grep -rn "msg.sender\|tx.origin" src/` no debe devolver nada fuera de la
propia implementación de `_msgSender()`.

## Cómo construir una app web3 (ethers v6 + `@lacchain/gas-model-provider`)

Guía completa en `references/app-web3.md`. Lo esencial:

1. `npm i ethers@^6.13 @lacchain/gas-model-provider@^1.2.1 dotenv`. `LacchainProvider` extiende
   `JsonRpcProvider` (arregla el receipt de Besu); `LacchainSigner` extiende `Wallet` y firma la
   metatx (`chainId = 0`, `v = 27/28`, + 64 bytes de `nodeAddress`/`expiration`).
2. **El navegador no puede firmar**: MetaMask rechaza `chainId 0` y no añade esos campos. Las
   lecturas pueden ir directas desde el front; **las escrituras van por un backend** que tiene la
   clave y usa `LacchainSigner` (`templates/server.js`). Es lo que resuelve un relay multi-tenant con autenticación.
3. **El `RPC_URL` debe ser el endpoint que RELAYA**: el **puerto 80** del writer (nginx →
   relay-signer :9001). Dada una IP a secas, `http://<IP>`. Con el besu directo `:4545` la tx se
   manda y no se ejecuta nunca (ver regla 7).
4. En cada escritura: `gasPrice: 0` + `gasLimit` explícito; si mandas una tx sin `data`, pasa
   `data: "0x"` (el provider concatena y `undefined` rompe la firma); **signer fresco por
   operación** (la expiración se congela al construirlo); **serializa por cuenta firmante** (dos
   envíos en paralelo → `BadNonce`); cada tx tarda 1–3 min (encola, no bloquees el HTTP).
5. **Un `status = 1` no basta**: hay tres niveles de error — rechazo del relay-signer (`-32000`,
   `-32007`, `-32601`), rechazo del RelayHub (`BadTransactionSent` + `ErrorCode`, `OK = 8` es éxito)
   y revert del contrato (`TransactionRelayed executed=false`). Se leen del receipt
   (`status`/`revertReason`) y de `relay_getMetaTxResult`.
6. Usar `templates/lacchain.js`: `sendRelayed` solo resuelve si la metatx se ejecutó y si no lanza
   `RelayError` con `{ stage, errorName, errorCode, revertReason, rpcCode, txHash }`;
   `deployRelayed` resuelve la dirección del trace. CLI de ejemplo: `templates/send-tx.js`.


### Consideraciones al enviar TX con ethers v6 (checklist)

```js
const { ethers } = require("ethers");
const { LacchainProvider, LacchainSigner } = require("@lacchain/gas-model-provider");

const provider = new LacchainProvider("http://<IP-del-nodo>");   // PUERTO 80 (regla 7)
// signer FRESCO por operación: la expiración se congela al construirlo (ms, no segundos)
const signer   = new LacchainSigner(PRIVATE_KEY, provider, NODE_ADDRESS, Date.now() + 10 * 60 * 1000);

const token = new ethers.Contract(addr, abi, signer);
const tx = await token.transfer(to, amount, { gasLimit: 1_500_000, gasPrice: 0 });
await tx.wait();   // ojo: si el receipt trae status=0, wait() LANZA "transaction execution reverted"
```

| ✅ Hacer | ❌ No hacer |
|---|---|
| `RPC_URL = http://<IP-del-nodo>` (puerto 80) | el `:4545` de besu: no relaya y devuelve el nonce EVM |
| `gasPrice: 0` **y** `gasLimit` explícito en cada llamada | confiar en `eth_estimateGas` (no soportado en creación) |
| crear el signer justo antes de cada operación | reutilizar un signer de larga vida (firma metatxs vencidas) |
| pasar `data: "0x"` si la tx no lleva calldata | `{to, value}` a secas → `invalid BytesLike value` al firmar |
| **serializar** los envíos por cuenta firmante (cola) | dos `transfer` en paralelo → `BadNonce` en el segundo |
| encolar y responder `202`; cada tx tarda 1–3 min | bloquear una petición HTTP esperando el minado |
| verificar el resultado real con `relay_getMetaTxResult` (y `status`/`revertReason`) | dar por buena la tx porque `status = 1` o porque `wait()` no lanzó |
| lecturas (`view`) con el provider, sin signer ni gas | relayar lecturas |
| en el `catch`, consultar la metatx antes de diagnosticar | tratar el throw de `wait()` como "no se pudo enviar" |

Los tres niveles de error (relay-signer `-32000`/`-32007`, `ErrorCode` del RelayHub con `OK = 8`,
y revert del contrato) y el helper que los tipa están en `references/app-web3.md` y
`templates/lacchain.js`.

## Tests locales (Foundry)

Los tests no corren en LNet: desplegar `templates/MockRelayForwarder.sol` y colocarlo
con `vm.etch` en la dirección hardcodeada del forwarder. Probar ambos caminos:
directo (`msg.sender != relayHub`) y relayado (`vm.prank(relayHub)` tras
`mock.set(relayHub, remitenteOriginal)` → `_msgSender()` devuelve el original).

## Diagnóstico rápido

| Síntoma | Causa | Solución |
|---|---|---|
| `CREATE` interno `INVALID` con gas de sobra | Bytecode con `PUSH0` | `evm_version = "paris"` |
| El RelayHub recupera un sender equivocado | Firma EIP-155 (`v=36`) | Broadcast con `LacchainSigner`, no `cast`/`forge` |
| Comando "colgado" | Minado síncrono | Esperar 1–3 min |
| `eth_estimateGas` falla en creación | No soportado por el gas model | `gasLimit` explícito |
| `contractAddress = 0x0` en receipt | CREATE interno del RelayHub | Leer dirección del trace |
| `-32007 Sender not authorized` | Cuenta/nodo no permisionado | Permisionamiento LNet |
| Tx relayada con `returnValue = 8` | — | Es el estado **normal de éxito**, no un error |
| La tx "se envía" y nunca pasa nada | `RPC_URL` apunta al besu `:4545`, que no relaya | usar el **puerto 80** del writer: `http://<IP>` |
| `Transaction nonce is too distant from current sender nonce` | se está mandando la metatx a besu `:4545`: compara el nonce de metatx contra el nonce EVM de la cuenta (límite `--tx-pool-max-future-by-sender`, 200) | apuntar al puerto 80 y purgar las tx *future* encoladas |
| `invalid BytesLike value` al firmar | tx sin `data` (el provider concatena `undefined` + los 64 bytes) | pasar `data: "0x"` (y mover valor con un ERC20, no con `value`) |
| `BadNonce` esporádico en una app | dos envíos concurrentes leen el mismo nonce | serializar por cuenta firmante (cola) |
| Metatx vencida / `expiration` en el pasado | el `LacchainSigner` congela la expiración al construirse | crear un signer fresco por operación |
| `tx.wait()` lanza `transaction execution reverted` | receipt con `status=0`: así reporta el relay un `ErrorCode` o un revert | en el `catch`, consultar `relay_getMetaTxResult` antes de diagnosticar |
| `relay_getMetaTxResult` → `-32601` | endpoint sin los métodos `relay_*` (rpc-router) | decidir con `status`/`revertReason` del receipt |

> `getMsgSender()` del forwarder es estado global que cualquier tx sobrescribe; no
> sirve para auditar tu propia transacción a posteriori.

## Referencias del skill

- `references/gas-model.md` — guía técnica completa (firma pre-EIP155 en detalle,
  codificación de los 64 bytes, flujo de deploy y upgrade UUPS).
- `references/desarrollo-contratos.md` — cómo escribir los contratos: las tres variantes de
  `BaseRelayRecipient`, conflicto con OZ `Context`, qué es seguro en el constructor,
  tabla de anti-patrones, tests con mock, checklist previo al despliegue.
- `references/app-web3.md` — app web3 con ethers v6 + `@lacchain/gas-model-provider`: provider/signer,
  por qué el navegador no firma, endpoint que relaya, reglas de cada tx, deploy, concurrencia/nonce
  y los tres niveles de manejo de errores (relay-signer / RelayHub `ErrorCode` / revert).
- `templates/` — archivos listos para copiar:
  - contratos base: `BaseRelayRecipient.sol` (forwarder fijo),
    `BaseRelayRecipientConfigurable.sol` (por constructor),
    `BaseRelayRecipientUpgradeable.sol` (UUPS).
  - ejemplos: `GasModelToken.sol` (ERC20 OZ v5), `GasModelStorage.sol` (contrato mínimo).
  - tests: `MockRelayForwarder.sol` + `GasModelToken.t.sol` (esqueleto; copiar ambos a `test/`).
  - despliegue: `deploy.js`, `deploy-upgradeable.js`, `update-upgradeable.js`, `package.json`,
    `foundry.toml`, `deploy.sh`, `.env.example`.
  - app web3: `lacchain.js` (módulo: provider/signer, `sendRelayed` con errores tipados,
    `deployRelayed`, `getMetaTxResult`, `ERROR_CODES`), `send-tx.js` (CLI de envío),
    `server.js` (backend HTTP que firma y relaya).
