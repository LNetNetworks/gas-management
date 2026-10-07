# Cómo el RelaySigner reporta el fallo de la llamada interna (status=1 → fallida)

> **Pregunta:** ¿`gas-management` controla el caso en que la tx externa se mina con `status=1`
> pero la **transacción interna falla**, y devuelve la tx al cliente como **fallida** en vez de
> como exitosa?
>
> **Respuesta: Sí.** El RelaySigner reescribe el resultado a fallido antes de devolverlo al
> cliente (`eth_getTransactionReceipt` → `status=0`), y además expone un endpoint dedicado
> (`relay_getMetaTxResult`) con el detalle estructurado.

Aplica al **modelo clásico (RelayHub / `TxRelay`)**. Disponible en la rama **`develop`** (fixes de
observabilidad: commits `fc7774e`, `d24b40d`, `5b7a3a7`).

---

## El problema de raíz (en el contrato, no en el signer)

El RelayHub (`TxRelay`) **no revierte** ante el fallo de la llamada interna: la ejecuta con un
`.call` de bajo nivel, captura el fallo como `executed=false`, emite
`TransactionRelayed(..., executed=false, output)` y termina con éxito. Por eso **en besu la tx
externa se mina con `status=1`** aunque la operación del usuario haya revertido. Es intencional: si
revirtiera, se haría rollback del nonce y de la contabilidad de gas del nodo.

Resultado: un cliente "ingenuo" (`tx.wait()` mirando solo el `status`) daría la tx por exitosa →
**fallo silencioso**. El RelaySigner cierra ese hueco traduciendo el resultado interno al cliente.

## Mecanismo 1 — `eth_getTransactionReceipt` reescribe `status=1` → `status=0`

`service/relaySignerService.go` → `GetTransactionReceipt` (≈ líneas 121-198). Ruteado desde
`controller/relayController.go` (`IsGetTransactionReceipt`) → `processGetTransactionReceipt`.

Flujo:

1. Pide el receipt a besu (`NodeURL`) — viene con `status=1`.
2. Recorre `receipt.Logs` y calcula los topics (keccak) de los eventos del RelayHub.
3. Según lo que encuentre:
   - **`TransactionRelayed(executed=false)`** → `receipt.Status = uint64(0)` y añade
     `revertReason` con el motivo decodificado (`decodeRevertReason(output)`).
   - **`BadTransactionSent`** → `receipt.Status = uint64(0)` y
     `revertReason = "BadTransactionSent: <errorCode>"`.
   - **`ContractDeployed`** → fija `receipt.ContractAddress` con la dirección real (el receipt de
     besu trae `0x0` porque el `CREATE` lo hace el RelayHub).
4. Si hubo fallo, devuelve el receipt modificado (`receiptReverted`, con el campo extra
   `revertReason`); si no, el receipt tal cual.

**Efecto en el cliente:** recibe `status=0` → ethers/web3 lo tratan como transacción **revertida**,
y `revertReason` lleva el motivo legible. La tx se devuelve como **fallida**, no como éxito.

## Mecanismo 2 — endpoint `relay_getMetaTxResult` (resultado estructurado)

`service/relaySignerService.go` → `GetMetaTxResult` (≈ líneas 202-263). Ruteado desde
`controller/relayController.go` (`IsGetMetaTxResult`) → `processGetMetaTxResult`. Sufijo RPC
`_getMetaTxResult` (`rpc/json.go`).

Devuelve un objeto explícito a partir de los mismos eventos:

```json
{
  "transactionHash": "0x…",
  "mined": true,
  "success": false,
  "executed": false,
  "errorCode": null,
  "revertReason": "ERC20InsufficientBalance(…)",
  "deployedAddress": null
}
```

- `success/executed=false` + `revertReason` cuando `TransactionRelayed(executed=false)`.
- `success=false`, `errorCode`, `revertReason="BadTransactionSent: …"` cuando `BadTransactionSent`.
- `deployedAddress` cuando `ContractDeployed`.

Es la vía recomendada para clientes que quieran el resultado lógico sin reinterpretar el `status`.

## Funciones de apoyo

- `transactionRelayedFailed(id, data)` (≈ línea 393) — desempaqueta el evento `TransactionRelayed`
  y devuelve `(executed, output)`.
- `decodeRevertReason(output)` — traduce el `output` (revert ABI-encoded; OZ v5 usa custom errors)
  a texto legible, usando el catálogo de custom errors añadido en `5b7a3a7`.
- `badTransactionErrorCode` / `errorCodeName` — mapean el `errorCode` del enum `IRelayHub.ErrorCode`
  a nombre (`BadNonce`, `NotEnoughGas`, `InvalidSignature`, …).

## Límites / cosas a tener en cuenta

1. **Solo aplica si el cliente consulta a través de este RelaySigner.** La tx en besu sigue siendo
   `status=1`. Quien lea el receipt desde **besu directo**, el **explorer**, u otro **RelaySigner sin
   este parche / versión vieja** verá `status=1` (fallo silencioso). La defensa robusta e
   independiente del signer es validar los **eventos** del lado cliente (ver el helper
   `assertRelayedOk` en el repo `erc20-upgradeable-gas-model-erros`).
2. **Versión:** está en `develop`, no en `master`. El nodo debe correr esta build para que aplique
   (PR `develop`→`master` pendiente). Verificar la versión desplegada en el nodo.
3. **Cobertura:** cubre `TransactionRelayed(executed=false)` y `BadTransactionSent`. **No** cubre el
   síntoma *timeout en despliegues* (ahí el problema no es el `status` sino que ethers calcula la
   dirección con `keccak(deployer,nonce)`, que no aplica bajo el gas model).

## Referencias de código

| Qué | Dónde |
|---|---|
| Reescritura de `status` en el receipt | `service/relaySignerService.go` → `GetTransactionReceipt` (≈121-198) |
| Resultado estructurado | `service/relaySignerService.go` → `GetMetaTxResult` (≈202-263) |
| Ruteo de los métodos | `controller/relayController.go`, `controller/processController.go`, `rpc/json.go` |
| Por qué `relayMetaTx` no revierte | contrato `model-gas-lnet/contracts/TxRelay.sol` (`relayMetaTx`, `_executeCall`) |
| Helper de cliente (independiente del parche) | `erc20-upgradeable-gas-model-erros/helpers/relayhub.js` (`assertRelayedOk`) |
