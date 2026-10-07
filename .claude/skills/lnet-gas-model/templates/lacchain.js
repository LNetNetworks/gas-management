/**
 * lacchain.js — módulo reutilizable para una app web3 sobre LNet (gas model LACChain)
 * con ethers v6 + @lacchain/gas-model-provider.
 *
 * Lo que resuelve (todo verificado contra @lacchain/gas-model-provider 1.2.1):
 *  - `LacchainProvider` para lecturas y RPC crudo; `LacchainSigner` (extiende Wallet) para escribir.
 *  - La expiración se fija al CONSTRUIR el signer, así que aquí se crea un signer FRESCO por tx
 *    (un signer de larga vida firmaría metatxs ya vencidas).
 *  - `gasPrice: 0` + `gasLimit` explícito siempre (no hay `eth_estimateGas` bajo el gas model).
 *  - `data: "0x"` obligatorio: el provider concatena `data + abi.encode(node, expiration)`; sin
 *    `data` el resultado es `"undefined…"` → `invalid BytesLike value`.
 *  - Lectura del resultado real: el receipt del gas model dice `status=1` incluso si el RelayHub
 *    rechazó la metatx en versiones viejas del relay-signer, así que se consulta también
 *    `relay_getMetaTxResult` y se traduce el `ErrorCode`.
 *
 * Uso: ver send-tx.js y deploy.js.
 */
const { ethers } = require("ethers");
const { LacchainProvider, LacchainSigner } = require("@lacchain/gas-model-provider");

/** enum ErrorCode del RelayHub (contract-relayhub/IRelayHub.sol). OK=8 es ÉXITO, no error. */
const ERROR_CODES = {
  0: "MaxBlockGasLimit",   // gasLimit firmado > maxGasBlockLimit (120M)
  1: "BadOriginalSender",  // el sender recuperado de la firma no es válido
  2: "BadNonce",           // nonce != nonces[node][user] que lleva el RelayHub
  3: "NotEnoughGas",       // gasLimit > ventana de gas del nodo relayer
  4: "IsNotContract",      // el `to` no tiene bytecode (es una EOA)
  5: "EmptyCode",          // el CREATE interno no dejó bytecode
  6: "InvalidSignature",   // ECDSA.recover falló (s alto, v∉{27,28}, recover→0)
  7: "InvalidDestination", // destino inválido (trustedAccountIngress / address(0))
  8: "OK",                 // ÉXITO
};

/** Inverso de ERROR_CODES: el relay devuelve `errorCode` como NOMBRE ("BadNonce"), no como índice. */
const ERROR_CODE_BY_NAME = Object.fromEntries(Object.entries(ERROR_CODES).map(([n, name]) => [name, Number(n)]));

const DEFAULT_GAS_LIMIT = 1_500_000;
const DEFAULT_EXPIRATION_MS = 10 * 60 * 1000; // 10 min de margen

/**
 * Provider de lectura. El RPC debe ser el endpoint QUE RELAYA (p.ej. el nginx :80 del writer,
 * que enruta eth_sendRawTransaction al relay-signer :9001). El besu directo :4545 NO relaya:
 * las escrituras se rechazan por permisionado o se quedan sin ejecutar.
 */
function makeProvider(rpcUrl) {
  return new LacchainProvider(rpcUrl);
}

/**
 * Signer fresco: la expiración se congela en el constructor, así que hay que crear uno por
 * operación (o cada pocos minutos). `expirationMs` es un timestamp en MILISEGUNDOS.
 */
function makeSigner({ privateKey, provider, nodeAddress, expirationMs }) {
  const expiration = expirationMs ?? Date.now() + DEFAULT_EXPIRATION_MS;
  return new LacchainSigner(privateKey, provider, nodeAddress, expiration);
}

/** Contrato de solo lectura (view/pure): no relaya, no gasta gas. */
function readContract({ address, abi, provider }) {
  return new ethers.Contract(address, abi, provider);
}

/**
 * Consulta el resultado real de una metatx. Devuelve
 * `{ status, revertReason, meta, errorCode, errorName, ok }`.
 * `meta` es `relay_getMetaTxResult` (puede ser null: hay despliegues cuyo router no expone
 * los métodos `relay_*` y responde -32601; en ese caso se decide con el receipt).
 */
async function getMetaTxResult(provider, txHash) {
  const rcpt = await provider.send("eth_getTransactionReceipt", [txHash]).catch(() => null);
  const status = rcpt?.status != null ? parseInt(rcpt.status, 16) : null;
  const revertReason = rcpt?.revertReason ?? rcpt?.revertreason ?? null;

  let meta = null;
  try {
    meta = await provider.send("relay_getMetaTxResult", [txHash]);
  } catch (_) {
    /* método no expuesto por este endpoint (-32601): nos quedamos con el receipt */
  }

  // `errorCode` llega como nombre ("BadNonce") en los relay-signer actuales, y como índice en otros.
  const rawCode = meta?.errorCode ?? meta?.error_code ?? null;
  const isName = typeof rawCode === "string" && isNaN(Number(rawCode));
  const errorName = isName ? rawCode : rawCode == null ? null : ERROR_CODES[Number(rawCode)] ?? null;
  const errorCode = isName ? ERROR_CODE_BY_NAME[rawCode] ?? null : rawCode == null ? null : Number(rawCode);

  const ok =
    status === 1 &&
    (meta == null || (meta.executed !== false && (errorName == null || errorName === "OK")));

  return { status, revertReason, meta, errorCode, errorName, ok };
}

/** Error de una metatx que se relayó pero no se ejecutó como el usuario esperaba. */
class RelayError extends Error {
  constructor(message, details) {
    super(message);
    this.name = "RelayError";
    Object.assign(this, details);
  }
}

/**
 * Envía una escritura relayada y NO resuelve hasta saber que se ejecutó de verdad.
 * `call` recibe el contrato conectado al signer y devuelve la promesa de la tx
 * (p.ej. `(t) => t.transfer(dest, amount, opts)`).
 * Lanza `RelayError` con `{ errorName, errorCode, revertReason, txHash, meta }` si falló.
 */
async function sendRelayed({
  address,
  abi,
  call,
  privateKey,
  provider,
  nodeAddress,
  gasLimit = DEFAULT_GAS_LIMIT,
  expirationMs,
  onProgress,
}) {
  const signer = makeSigner({ privateKey, provider, nodeAddress, expirationMs });
  const contract = new ethers.Contract(address, abi, signer);

  let txHash = null;
  const t0 = Date.now();
  const beat = onProgress ? setInterval(() => onProgress(Math.round((Date.now() - t0) / 1000)), 2000) : null;
  try {
    // gasPrice 0 + gasLimit explícito: obligatorio bajo el gas model.
    const tx = await call(contract, { gasLimit, gasPrice: 0 });
    txHash = tx.hash;
    await tx.wait(); // minado SÍNCRONO: 1-3 min. No abortar.
  } catch (e) {
    // Dos casos distintos caen aquí:
    //  (a) la tx nunca se envió: rechazo del relay-signer como error JSON-RPC
    //      (-32000 gas limit exceeds…, -32007 sender not authorized, nonce too low…);
    //  (b) sí se minó pero con `status = 0` y `tx.wait()` rechaza ("transaction execution reverted"):
    //      así es como el relay-signer reporta un ErrorCode del RelayHub o un revert del contrato,
    //      así que hay que preguntar por el resultado real antes de dar un diagnóstico.
    const info = e.info?.error || e.error || {};
    if (txHash) {
      const result = await getMetaTxResult(provider, txHash).catch(() => null);
      if (result && !result.ok) {
        throw new RelayError(
          result.errorName
            ? `metatx rechazada por el RelayHub: ${result.errorName}`
            : `metatx no ejecutada (status=${result.status})${result.revertReason ? `: ${result.revertReason}` : ""}`,
          { ...result, txHash, stage: "relay", cause: e.shortMessage || e.message },
        );
      }
    }
    throw new RelayError(info.message || e.shortMessage || e.message, {
      rpcCode: info.code ?? null,
      txHash,
      stage: "send",
    });
  } finally {
    if (beat) clearInterval(beat);
  }

  const result = await getMetaTxResult(provider, txHash);
  if (!result.ok) {
    throw new RelayError(
      result.errorName
        ? `metatx rechazada por el RelayHub: ${result.errorName}`
        : `metatx no ejecutada (status=${result.status})${result.revertReason ? `: ${result.revertReason}` : ""}`,
      { ...result, txHash, stage: "relay" },
    );
  }
  return { txHash, ...result };
}

/**
 * Despliega un contrato compilado por Foundry (`out/<Nombre>.sol/<Nombre>.json`) vía relay.
 * Constructor SIN argumentos (los 64 bytes extra van al final del initcode).
 * La dirección se lee del trace: el receipt trae `contractAddress = 0x0` porque el CREATE lo hace
 * el RelayHub, y `keccak(deployer, nonce)` no aplica.
 */
async function deployRelayed({
  abi,
  bytecode,
  privateKey,
  provider,
  nodeAddress,
  gasLimit = 8_000_000,
  expirationMs,
  onProgress,
}) {
  const signer = makeSigner({ privateKey, provider, nodeAddress, expirationMs });
  const factory = new ethers.ContractFactory(abi, bytecode, signer);

  const t0 = Date.now();
  const beat = onProgress ? setInterval(() => onProgress(Math.round((Date.now() - t0) / 1000)), 2000) : null;
  let receipt, txHash;
  try {
    const contract = await factory.deploy({ gasLimit, gasPrice: 0 });
    receipt = await contract.deploymentTransaction()?.wait();
    txHash = receipt?.hash;
  } finally {
    if (beat) clearInterval(beat);
  }

  const ZERO = "0x" + "0".repeat(40);
  let address = receipt?.contractAddress;
  if (!address || address.toLowerCase() === ZERO) {
    address = await findCreatedAddress(provider, txHash);
  }
  return { address, txHash, receipt };
}

/** Dirección del contrato creado, leída del resultado del opcode CREATE en el trace. */
async function findCreatedAddress(provider, txHash) {
  try {
    const trace = await provider.send("debug_traceTransaction", [
      txHash,
      { disableStack: false, disableMemory: true, disableStorage: true },
    ]);
    const logs = trace.structLogs || [];
    for (let i = 0; i < logs.length; i++) {
      if (logs[i].op === "CREATE" || logs[i].op === "CREATE2") {
        const depth = logs[i].depth;
        for (let j = i + 1; j < logs.length; j++) {
          if (logs[j].depth === depth) {
            const top = logs[j].stack?.[logs[j].stack.length - 1];
            if (!top) return null;
            const addr = "0x" + top.slice(-40);
            return addr === "0x" + "0".repeat(40) ? null : ethers.getAddress(addr);
          }
        }
      }
    }
  } catch (_) {
    /* nodo sin debug_traceTransaction */
  }
  return null;
}

module.exports = {
  ERROR_CODES,
  ERROR_CODE_BY_NAME,
  RelayError,
  makeProvider,
  makeSigner,
  readContract,
  sendRelayed,
  deployRelayed,
  getMetaTxResult,
  findCreatedAddress,
};
