# Despliegue en LNet (gas model LACChain) con Foundry

Guía completa de cómo se despliegan los contratos de este proyecto en la red **LNet**,
por qué Foundry por sí solo no basta, y todo lo aprendido durante la integración.

---

## 1. Datos de la red

LNet tiene dos redes. **El `trustedForwarder` se elige según la red** (es una dirección en el
contrato, ver §3); el resto del flujo es idéntico.

| Parámetro | **Testnet (open pro-testnet)** | **Mainnet (omega)** |
|---|---|---|
| Chain ID | `648540` | `648541` |
| RPC (gas model) | **puerto 80** de tu writer: `http://<IP>` (nginx -> relay-signer :9001) | ídem, tu nodo de mainnet |
| Cliente | Besu (permisionada), gas price `0` | Besu (permisionada), gas price `0` |
| **EVM version** | **Paris** (¡no soporta `PUSH0`!) | **Paris** (¡no soporta `PUSH0`!) |
| `trustedForwarder` (`BaseRelayRecipientProxy`) | `0xa4B5eE2906090ce2cDbf5dfff944db26f397037D` | `0xEAA5420AF59305c5ecacCB38fcDe70198001d147` |
| RelayHub | `0xB9e9C5C528C266f2A1C7Eeec1975595232C8E475` (impl; el proxy es el trustedForwarder) | `0x34B220Ef63dea567eDf6d540316B5a3f4831Cf6c` |

> Comprueba el chain id de tu red con `eth_chainId` contra tu RPC, y las direcciones on-chain
> con el operador de la red. Para la firma el chain id es indiferente (LACChain firma con
> `chainId = 0`, ver §4), pero el `provider` de ethers lo usa para detectar la red.

---

## 1.1 Diferencias frente a una red tradicional (Ethereum)

Estos son los puntos que **cambian respecto a desplegar/usar contratos en Ethereum** y que explican
todo lo demás de esta guía:

| Tema | Red tradicional (Ethereum) | **LNet (gas model LACChain)** |
|---|---|---|
| ¿Quién paga el gas? | El remitente (EOA), gas price > 0 | El **nodo relayer**; gas price `0` para el usuario |
| Envío de la tx | El EOA firma y manda directo al contrato | El nodo **relaya** la tx al **RelayHub**; tu deploy/call es una **tx interna** |
| Firma | EIP-155 (incluye `chainId`, `v = chainId·2+35±`) | **Legacy pre-EIP155** (`v = 27/28`, hash sin `chainId`) |
| Herramienta de broadcast | `forge script` / `cast` / ethers normal | **No** sirven; broadcast con `@lacchain/gas-model-provider` (`LacchainSigner`) |
| Campos de la tx | estándar | + `nodeAddress` y `expirationDate` (64 bytes al final del calldata) |
| `msg.sender` dentro del contrato | el remitente real | el **RelayHub**; hay que usar `_msgSender()` vía `BaseRelayRecipient` |
| `tx.origin` | el remitente real | la cuenta del **writer node** |
| Versión de EVM | Cancun/Shanghai (con `PUSH0`) | **Paris** (sin `PUSH0`) → `evm_version = "paris"` |
| Constructor con argumentos | normal | **evitar** (los 64 bytes interfieren con el decode); usar `initialize()` / `_msgSender()` |
| Dirección del contrato | `keccak(deployer, nonce)` | sale del **trace** del `CREATE` interno del RelayHub |
| Confirmación | segundos, asíncrono | **síncrono**, 1–3 min por deploy |
| `eth_estimateGas` en creación | funciona | no soportado → pasar `gasLimit` explícito |
| Permisos para escribir | cualquiera con fondos | la **cuenta/nodo** debe estar **permisionado** en la red |

> Todo lo que sigue (secciones 2–8) detalla cada una de estas filas. Cómo escribir el contrato en sí
> está en `desarrollo-contratos.md`.

---

## 2. Qué es el "gas model" de LACChain

En una red Ethereum normal, el que envía la transacción paga el gas. LNet usa el
**gas model de LACChain**: el gas price es 0 para el usuario, y es un **nodo** el que
patrocina (relaya) la transacción y asume su coste según un modelo de consumo controlado.

Para que esto funcione, cada transacción lleva **dos campos extra**:

- **`nodeAddress`** — la dirección del nodo que patrocina la transacción (tu nodo LNet;
  se obtiene con `cat nodeAddress` en el servidor del nodo).
- **`expirationDate`** — marca de tiempo (en **milisegundos**) hasta la que la tx es válida.

### Cómo se codifican esos campos

La librería oficial `@lacchain/gas-model-provider` **no** crea un tipo de transacción nuevo.
Hace dos cosas sobre una transacción normal:

1. **Añade 64 bytes al final del `data`** (calldata):

   ```
   data = data_real  ++  abi.encode(address nodeAddress, uint256 expirationDate)
   ```

2. **Firma como transacción legacy con `chainId = 0`** (ver sección 4).

El **nodo RPC** recibe esa transacción, la **envuelve y la relaya al RelayHub** (firmando
él con su propia clave y pagando el gas). El RelayHub:

- Recorta los últimos 64 bytes para leer `nodeAddress` + `expirationDate`.
- Verifica tu firma para saber quién eres realmente.
- Ejecuta tu transacción real (el `data` sin los 64 bytes) en tu nombre.

> Por eso, en el explorador, la transacción principal aparece **del nodo → al RelayHub**,
> y el despliegue real ocurre como una **transacción interna** (un `CREATE` dentro del RelayHub).

---

## 3. `_msgSender()` y `BaseRelayRecipient`

Como la transacción la manda físicamente el nodo (no tú), dentro del contrato `msg.sender`
sería el RelayHub, no tú. Para recuperar tu dirección real, **todo contrato en LNet debe
heredar `BaseRelayRecipient`** y usar `_msgSender()` en lugar de `msg.sender`.

`_msgSender()` consulta al `trustedForwarder`:
- Si la llamada vino por el RelayHub → devuelve el remitente original (tú).
- Si no → devuelve `msg.sender`.

> El `trustedForwarder` (`BaseRelayRecipientProxy`) es **distinto por red** (ver §1) y tiene que ser
> el **proxy**, no el RelayHub: el hub no expone `getRelayHub()`, el `abi.decode` revertiría y el
> constructor se caería (deploy a dirección `0x0`). En los contratos **no-upgradeable** va
> hardcodeado como variable de estado (`templates/BaseRelayRecipient.sol`) o inyectado por
> constructor (`templates/BaseRelayRecipientConfigurable.sol`); en los **upgradeable** NO puede ir
> inline (en un proxy no persistiría), se fija en `initialize` vía
> `__BaseRelayRecipient_init(TRUSTED_FORWARDER)` (`templates/BaseRelayRecipientUpgradeable.sol`, que
> además reserva un `__gap` para futuras variables). Cambiar de red = cambiar esa dirección.

En `LnetToken` esto obliga a resolver un conflicto: tanto `Context` (de OpenZeppelin, vía
ERC20/Ownable) como `BaseRelayRecipient` definen `_msgSender()`. Se resuelve con un override:

```solidity
function _msgSender()
    internal
    view
    override(BaseRelayRecipient, Context)
    returns (address sender)
{
    (, bytes memory bytesRelayHub) =
        trustedForwarder.staticcall(abi.encodeWithSignature("getRelayHub()"));
    if (msg.sender == abi.decode(bytesRelayHub, (address))) {
        (, bytes memory bytesSender) =
            trustedForwarder.staticcall(abi.encodeWithSignature("getMsgSender()"));
        return abi.decode(bytesSender, (address));
    } else {
        return msg.sender;
    }
}
```

> Nota: el override es `view` (lo exige `Context._msgSender`), por eso usa `staticcall`
> (compatible con `view`) en vez de `call`.

### Constructor sin argumentos

Los argumentos del constructor son problemáticos en LNet (los 64 bytes añadidos al calldata
interfieren con su decodificación). Por eso `LnetToken` usa un **constructor sin argumentos**
y toma el owner de `_msgSender()`, igual que el ejemplo de la documentación oficial:

```solidity
constructor() ERC20("LNET COIN", "LNET") Ownable(_msgSender()) {
    _mint(_msgSender(), INITIAL_SUPPLY);
}
```

Durante el despliegue relayado, `_msgSender()` devuelve tu dirección real → tú quedas como
owner y recibes el suministro inicial.

---

## 4. Firma LACChain "pre-EIP155"

Este es el punto técnico clave por el que **Foundry no puede hacer el broadcast**.

### Firma normal vs EIP-155 vs pre-EIP155

Al firmar una tx no se firma la tx entera, sino el **hash** de sus campos. La firma son
`r`, `s`, `v`. El `v` permite recuperar al firmante (`ecrecover`).

| Esquema | ¿El hash incluye `chainId`? | `v` | Replay protection |
|---|---|---|---|
| **EIP-155** (moderno) | Sí | `chainId·2 + 35 + {0,1}` | Sí |
| **pre-EIP155** (legacy) | No | `27` / `28` | No |

### Qué hace LACChain

El `LacchainSigner` firma con **`chainId = 0`**:

```js
get chainId() { return 0; }
```

Con `chainId = 0`, ethers genera una firma **legacy pre-EIP155** (`v = 27/28`, hash sin
chainId). El RelayHub está hecho para recuperar al firmante **con ese esquema**.

### Por qué `forge` / `cast` fallan

`cast`/`forge`, al firmar con chainId 0, lo hacen estilo **EIP-155** → `v = 0·2+35+1 = 36`
(`0x24`). El RelayHub esperaba `v = 27/28`, así que con `v = 36` **recupera una dirección
equivocada** → la transacción no se ejecuta como tú.

**Conclusión:** el broadcast debe ir por `deploy.js` (que usa `LacchainSigner`, el cual sí
produce la firma pre-EIP155 correcta). Foundry solo compila.

---

## 5. El opcode `PUSH0` y `evm_version = paris`

El bug más difícil de encontrar. Síntoma: la transacción principal pasaba (status del relay
normal), pero el `CREATE` interno fallaba con `INVALID` **teniendo gas de sobra**.

Causa: el bytecode compilado contenía el opcode **`PUSH0`** (`0x5f`), introducido en el
hardfork **Shanghai** (EIP-3855). **La EVM de LNet es Paris** y no lo conoce → lo trata como
opcode inválido → el `CREATE` aborta.

La documentación oficial ya lo insinuaba: *"Compiled successfully (evm target: paris)"*.

**Solución** — en `foundry.toml`:

```toml
[profile.default]
solc = "0.8.30"
evm_version = "paris"   # imprescindible: evita PUSH0
optimizer = true
optimizer_runs = 200
```

Para comprobar que el bytecode no tiene `PUSH0`, no debe aparecer ningún byte `0x5f`
fuera de operandos de PUSH.

---

## 6. Cómo desplegar

Requisitos previos:

1. `foundry.toml` con `evm_version = "paris"`.
2. `.env` (copiado de `.env.example`) con:
   ```
   PRIVATE_KEY=0x...        # cuenta del desplegador (debe estar permisionada)
   RPC_URL=http://<IP-del-nodo>            # PUERTO 80 (nginx→relay-signer). NO el :4545 de besu
   CHAIN_ID=648540          # testnet (ver §1) · mainnet: 648541
   GAS_PRICE=0              # la red usa gas price 0
   NODE_ADDRESS=0x...       # dirección de tu nodo LNet (cat nodeAddress en el servidor del nodo)
   ```
   Opcionales (los leen los scripts JS): `EXPIRATION_MS` (¡milisegundos!, def. ahora+10 min),
   `GAS_LIMIT`, `CONTRACT`, `CONTRACT_V2`.
   > La **red** la define el `trustedForwarder` del contrato, no el `.env`. Para cambiar de testnet
   > a mainnet, ajusta esa dirección (ver §1) **y** el `RPC_URL`/`CHAIN_ID`.
3. Node.js instalado.

Comando único (flujo híbrido: Foundry compila → script Node relaya):

```bash
./deploy.sh                        # token simple (LnetToken)
./deploy.sh LnetTokenUpgradeable   # UUPS: implementación + ERC1967Proxy
```

`deploy.sh` enruta por el nombre del contrato:

- **token simple** → `lacchain-deploy/deploy.js` (un deploy).
- **`*Upgradeable`** → `lacchain-deploy/deploy-upgradeable.js` (dos deploys, ver abajo).

Equivalente manual del token simple:

```bash
forge build
cd lacchain-deploy && npm install && CONTRACT=LnetToken node deploy.js
```

### Flujo UUPS upgradeable (`deploy-upgradeable.js`)

Un contrato UUPS son **dos despliegues relayados**:

1. **Implementación** (constructor sin args, solo lógica).
2. **`ERC1967Proxy(impl, initData)`** con `initData = initialize(owner)`.

**Se interactúa siempre con la dirección del PROXY** (la implementación es solo la lógica).
El proxy SÍ lleva argumentos de constructor (`impl`, `initData`); con `evm_version = paris`
funcionan sin problema (los 64 bytes del gas model se añaden después y el decoder los ignora).
Para futuras actualizaciones: el owner llama `upgradeToAndCall(nuevaImpl, "")` sobre el proxy.

> ⚠️ El nodo relaya y mina de forma **síncrona**: el comando puede tardar **1-3 minutos**
> mientras procesa. **No lo canceles** (hay un contador en pantalla).

### Dirección del contrato

La dirección NO es `keccak(deployer, nonce)` (eso es lo que calcula ethers, y es incorrecto
aquí porque el contrato lo crea el RelayHub). El `deploy.js` la resuelve leyendo el resultado
del opcode `CREATE` en el trace (`debug_traceTransaction`).

### Cómo usar los contratos una vez desplegados (interacción)

Con el contrato ya en la red, interactúas igual que con cualquier ERC20 **pero respetando dos cosas**:
siempre contra la **dirección del PROXY** (en upgradeables) y todo *envío* (write) debe ir **relayado**
por el gas model.

**Lecturas (`view`/`pure`)** — NO consumen gas ni necesitan relay; basta un provider normal o el
`LacchainProvider`:

```js
const { ethers } = require("ethers");
const { LacchainProvider } = require("@lacchain/gas-model-provider");
const artifact = require("../out/MiToken.sol/MiToken.json");

const provider = new LacchainProvider(process.env.RPC_URL);
const token = new ethers.Contract(PROXY, artifact.abi, provider); // PROXY, no la implementación
await token.name();
await token.decimals();
await token.totalSupply();
await token.balanceOf(addr);
await token.owner();
```

**Escrituras (`transfer`, `approve`, `burn`, `mint`…)** — SÍ se relayan: hay que firmar con
`LacchainSigner` (firma pre-EIP155), pasar `gasPrice: 0` y un `gasLimit` explícito (no hay
`eth_estimateGas` en creación; para llamadas normales conviene fijarlo igual). Dentro del contrato,
el owner/remitente que ve el ERC20 es tu `_msgSender()` real, no el RelayHub:

```js
const { LacchainSigner } = require("@lacchain/gas-model-provider");
// expiration en MILISEGUNDOS; NODE_ADDRESS = dirección de tu nodo LNet (cat nodeAddress)
const signer = new LacchainSigner(PRIVATE_KEY, provider, NODE_ADDRESS, Date.now() + 10*60*1000);
const token = new ethers.Contract(PROXY, artifact.abi, signer);

const amount = ethers.parseUnits("100", 6);          // ajusta a los decimales del token
await (await token.transfer(dest, amount, { gasLimit: 8_000_000, gasPrice: 0 })).wait();
await (await token.burn(amount,        { gasLimit: 8_000_000, gasPrice: 0 })).wait();
```

> `templates/send-tx.js` es un ejemplo completo de escritura relayada y sirve de plantilla para
> cualquier otra llamada.

Diferencias clave frente a una red tradicional al **usar** el contrato (no solo desplegar):
- **No mandes la tx con `cast send` ni con un signer ethers normal**: el RelayHub recuperaría un
  sender equivocado (firma EIP-155). Usa siempre `LacchainSigner`.
- **`gasPrice: 0` y `gasLimit` explícito** en cada escritura.
- **Interactúa con el PROXY**, nunca con la implementación (esta no tiene estado/balances).
- Cada escritura también tarda **1–3 min** (minado síncrono) y la cuenta debe estar **permisionada**.

---

## 7. Cómo actualizar (upgrade) un contrato UUPS

Un upgrade NO cambia la dirección del proxy ni su estado (balances, owner, supply): solo
reemplaza la **lógica** (la implementación). Pasos (todo relayado por el gas model):

1. Compilar la nueva implementación (ej. `LnetTokenUpgradeableV2`) con Foundry.
2. Desplegar la nueva implementación (constructor sin args).
3. El **owner** llama `proxy.upgradeToAndCall(nuevaImpl, "0x")`.

El script `lacchain-deploy/update-upgradeable.js` hace los pasos 2 y 3 y verifica el
resultado (slot ERC1967, `version()`, estado preservado):

```bash
forge build
cd lacchain-deploy
PROXY=0x<dirección_del_proxy> node update-upgradeable.js
# Implementación distinta a la V2 por defecto:
PROXY=0x<proxy> CONTRACT_V2=MiImplV3 node update-upgradeable.js
```

Requisitos:
- La nueva implementación debe ser **compatible de storage** con la anterior (mismo layout;
  añade variables nuevas solo al final, respetando el `__gap`). No la inicialices de nuevo
  salvo que uses un `reinitializer`.
- La cuenta del `.env` debe ser el **owner** del proxy (el script lo verifica antes y aborta
  si no lo es).

> El segundo argumento de `upgradeToAndCall` es `"0x"` (sin reinicialización). Si la nueva
> versión necesita inicializar estado nuevo, pasa ahí el calldata de un `reinitializer`.

---

## 8. Diagnóstico / problemas comunes

| Síntoma | Causa probable | Solución |
|---|---|---|
| `CREATE` interno falla con `INVALID` y gas de sobra | Bytecode con `PUSH0` | `evm_version = "paris"` |
| El RelayHub recupera un sender equivocado | Firma EIP-155 (`v=36`) en vez de pre-155 | Usar `deploy.js` (LacchainSigner), no `cast`/`forge` |
| El comando "se queda colgado" | El nodo mina síncrono y tarda | Esperar 1-3 min; hay heartbeat |
| `eth_estimateGas` falla en creación | El gas model no lo soporta | Pasar `gasLimit` explícito (lo hace `deploy.js`) |
| `contractAddress` = `0x0` en el receipt | El deploy es un CREATE interno del RelayHub | Leer la dirección del trace (lo hace `deploy.js`) |
| `error -32007 Sender not authorized` | Cuenta/nodo no permisionado | Completar el proceso de permisionamiento de LNet |

### Notas útiles

- El relay devuelve `returnValue = 8` como estado **normal de éxito** (no es un error).
  Todas las llamadas relayadas de la red lo tienen.
- `getMsgSender()` del forwarder es **estado global** que cualquier tx de la red sobrescribe;
  no sirve para verificar tu propia tx.

---


## 9. Referencias

- Documentación LNet: <https://docs.l-net.io/en/Deploy-Contract>
- Nodo Mainnet / permisionamiento: <https://docs.l-net.io/en/Mainnet-Node>
- Librería: `@lacchain/gas-model-provider` (npm)
- EIP-155 (replay protection): <https://eips.ethereum.org/EIPS/eip-155>
- EIP-3855 (`PUSH0`): <https://eips.ethereum.org/EIPS/eip-3855>
