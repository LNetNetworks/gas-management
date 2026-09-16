# Desarrollo de smart contracts para el gas model de LNet

Guía para **escribir** contratos que funcionen bajo el gas model LACChain (el despliegue
está en `gas-model.md`). Todo lo de aquí se deriva de un solo hecho: la llamada del usuario
no llega directo al contrato, llega **como llamada interna desde el RelayHub**, así que
`msg.sender` es el RelayHub y no el usuario.

## 1. La regla única: heredar `BaseRelayRecipient` y usar `_msgSender()`

```
usuario --firma metatx--> writer node (relay-signer) --tx--> RelayHub --call--> TuContrato
                                                                                  ^
                                          msg.sender == RelayHub ----------------'
```

`BaseRelayRecipient._msgSender()` resuelve el remitente real:

1. `staticcall` a `trustedForwarder.getRelayHub()`.
2. Si `msg.sender == relayHub` → `staticcall` a `trustedForwarder.getMsgSender()` y devuelve
   ese valor (el usuario original de la metatx).
3. Si no → devuelve `msg.sender` (llamada directa de un EOA u otro contrato).

Consecuencias de diseño:

- **Nunca** `msg.sender` en lógica de negocio, auth, `mapping` de saldos, `require`, ni eventos.
- **Nunca** `tx.origin`: bajo el gas model es la cuenta del **writer node**, no el usuario.
- El `trustedForwarder` tiene que ser el **proxy** `BaseRelayRecipientProxy`, no el RelayHub:
  el hub no tiene `getRelayHub()`, el `abi.decode` revierte y el constructor se cae (deploy a
  dirección `0x0`).
- El contrato sigue siendo usable **sin relay** (rama `else` del `_msgSender()`), lo que permite
  probarlo localmente y llamarlo desde otro contrato.

### Direcciones del `trustedForwarder` (proxy)

| Red | trustedForwarder |
|---|---|
| mainnet (648541) | `0xEAA5420AF59305c5ecacCB38fcDe70198001d147` |
| open-protestnet / dev (648540) | `0xa4B5eE2906090ce2cDbf5dfff944db26f397037D` |

## 2. Las tres variantes de `BaseRelayRecipient`

| Variante | Forwarder | Cuándo | Plantilla / ejemplo |
|---|---|---|---|
| **Hardcodeado** | `address internal trustedForwarder = 0x…;` inline | contrato no-upgradeable, red fija | `templates/BaseRelayRecipient.sol` |
| **Por constructor** | `constructor(address trustedForwarder_)` | mismo contrato en varias redes / relayer propio | `templates/GasModelStorage.sol` |
| **Upgradeable** | `__BaseRelayRecipient_init(...)` en `initialize` | proxy UUPS/ERC1967 | `templates/BaseRelayRecipientUpgradeable.sol` |

En la variante upgradeable el forwarder **no** puede fijarse inline: eso escribe el storage de
la implementación, no del proxy. Se fija en `initialize` (constante `TRUSTED_FORWARDER` del
contrato hijo) y la base reserva `uint256[49] private __gap`.

## 3. Conflicto con OpenZeppelin (`Context`)

OZ define `Context._msgSender()` (`internal view virtual`). Al heredar de ambos hay que
sobreescribir explícitamente:

```solidity
contract MiToken is ERC20, Ownable, BaseRelayRecipient {
    function _msgSender() internal view override(BaseRelayRecipient, Context) returns (address) { … }
}
```

- El override tiene que ser **`view`** (el de `Context` lo es) → la resolución se hace con
  `staticcall`, nunca con `call`.
- En la variante upgradeable el par es `override(BaseRelayRecipientUpgradeable, ContextUpgradeable)`
  y el cuerpo puede delegar: `return BaseRelayRecipientUpgradeable._msgSender();`.
- Al sobreescribirlo, **todo OZ queda alineado**: `ERC20.transfer`, `approve`, `burn`,
  `Ownable.onlyOwner`, `AccessControl`, etc. usan `_msgSender()` internamente y pasan a ver al
  usuario relayado. Por eso `Ownable(_msgSender())` en el constructor deja como owner al deployer
  real de la metatx.
- `_msgData()` de `Context` **no** se corrige: bajo el gas model el calldata trae 64 bytes extra
  (`nodeAddress`, `expirationDate`). No usar `_msgData()` para lógica.

## 4. Constructor: qué es seguro

El gas model añade `abi.encode(nodeAddress, expirationDate)` (64 bytes) al final del calldata,
también al initcode de un deploy.

- **Recomendado: constructor sin argumentos.** Es el patrón de la doc de LNet y de
  `LnetToken`/`SampleToken`: name/symbol hardcodeados, owner y suministro desde `_msgSender()`.
- Los args estáticos (`address`, `uint256`) en la práctica sobreviven, porque Solidity decodifica
  los args desde el final del initcode e **ignora bytes de más** (comprobado en la práctica con un constructor `Storage(address)`). Si de todas formas el deploy falla o el owner sale mal, quitá los
  args antes de seguir depurando.
- En upgradeable el problema desaparece: el constructor solo hace `_disableInitializers()` y todo
  el estado va en `initialize(...)`, que se llama por calldata del proxy.
  `ERC1967Proxy(impl, initData)` sí acepta args de constructor sin problema.

## 5. Anti-patrones bajo el gas model

| Patrón | Por qué falla | Alternativa |
|---|---|---|
| `msg.sender` | es el RelayHub | `_msgSender()` |
| `tx.origin` | es el writer node | `_msgSender()` |
| `require(msg.sender == owner)` a mano | mismo problema | `Ownable`/`AccessControl` (ya usan `_msgSender()`) tras el override |
| `payable` / `msg.value` | la metatx no transporta valor; el gas y el value los pone el nodo | mover valor con un ERC20 |
| Lógica que depende de `gasprice`/`gasleft()` | `gasPrice = 0` y el gas viene de la ventana del nodo | no depender de ellos |
| `_msgData()` / `msg.data` crudo | trae los 64 bytes extra | parámetros explícitos |
| `block.timestamp` como "hora de firma" | la metatx puede minarse minutos después (`expirationDate`) | tolerancias amplias |
| `ecrecover` con firma EIP-155/EIP-712 propia | el flujo LNet firma **legacy pre-EIP155** (`v=27/28`, `chainId=0`); ERC20Permit y similares no encajan con el firmado del relay | usar `_msgSender()` en vez de firmas propias |
| Leer `forwarder.getMsgSender()` para auditar a posteriori | es estado global que sobrescribe cualquier otra tx | eventos con `_msgSender()` |
| `PUSH0` (solc ≥0.8.20 sin `evm_version`) | la EVM es Paris | `evm_version = "paris"` |

Eventos: emitir siempre el sender resuelto — `emit ValueChanged(_msgSender(), newValue)` —
porque el `from` del receipt es el writer node y no dice nada del usuario.

## 6. Tests en Foundry (no corren en LNet)

Se inyecta `templates/MockRelayForwarder.sol` en la dirección del forwarder con `vm.etch` y se
prueban **los dos caminos**:

```solidity
address internal constant FORWARDER = 0xa4B5eE2906090ce2cDbf5dfff944db26f397037D;

function setUp() public {
    MockRelayForwarder mock = new MockRelayForwarder();
    vm.etch(FORWARDER, address(mock).code);          // el mock queda en la dirección hardcodeada
    MockRelayForwarder(FORWARDER).set(relayHub, address(0)); // camino directo por defecto
    vm.prank(owner);
    token = new MiToken();                            // owner == _msgSender() == msg.sender
}

function test_RelayedSenderResolution() public {
    MockRelayForwarder(FORWARDER).set(relayHub, alice); // el forwarder reporta a alice
    vm.prank(relayHub);                                 // msg.sender == relayHub
    token.transfer(owner, 400e6);                       // los tokens salen de alice
}
```

Con forwarder por constructor no hace falta `vm.etch`: se pasa `address(mock)` al constructor.
Con proxy UUPS: `new Impl()` + `ERC1967Proxy(impl, abi.encodeCall(Impl.initialize, (owner)))`, e
interactuar con el proxy casteado a la ABI de la implementación. Ejemplo completo: `templates/GasModelToken.t.sol`.

## 7. Checklist antes de desplegar

- [ ] Hereda una variante de `BaseRelayRecipient` con el forwarder **proxy** de la red destino.
- [ ] `grep -rn "msg.sender\|tx.origin" src/` no devuelve nada fuera de `_msgSender()`.
- [ ] `_msgSender()` sobreescrito con `override(BaseRelayRecipient, Context)` y `view` si hay OZ.
- [ ] Constructor sin args (o solo estáticos) / todo el estado en `initialize` si es upgradeable.
- [ ] Sin `payable`/`msg.value`, sin dependencia de `gasprice`.
- [ ] `evm_version = "paris"` en `foundry.toml`.
- [ ] Tests cubren camino directo y camino relayado.
- [ ] Deploy y llamadas por script Node con `@lacchain/gas-model-provider` (no `forge`/`cast`).

