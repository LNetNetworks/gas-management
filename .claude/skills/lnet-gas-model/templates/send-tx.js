/**
 * send-tx.js — envía una escritura relayada (ERC20 `transfer`) a LNet y reporta el resultado real.
 *
 * Uso:
 *   npm install                 # ethers ^6.13 + @lacchain/gas-model-provider ^1.2.1 + dotenv
 *   node send-tx.js             # .env: PRIVATE_KEY, RPC_URL (endpoint que RELAYA), NODE_ADDRESS,
 *                               #       TOKEN, TO; opcional AMOUNT (def 1), GAS_LIMIT
 */
require("dotenv").config();
const { ethers } = require("ethers");
const { makeProvider, readContract, sendRelayed, RelayError } = require("./lacchain");

const need = (k) => { const v = process.env[k]; if (!v) throw new Error(`Falta ${k} en .env`); return v; };

const ERC20_ABI = [
  "function transfer(address to, uint256 amount) returns (bool)",
  "function balanceOf(address) view returns (uint256)",
  "function decimals() view returns (uint8)",
  "function symbol() view returns (string)",
];

async function main() {
  const provider = makeProvider(need("RPC_URL"));
  const token = ethers.getAddress(need("TOKEN"));
  const to = ethers.getAddress(need("TO"));

  // Lecturas: sin relay, sin gas.
  const read = readContract({ address: token, abi: ERC20_ABI, provider });
  const [dec, sym] = await Promise.all([read.decimals(), read.symbol().catch(() => "?")]);
  const amount = ethers.parseUnits(process.env.AMOUNT || "1", Number(dec));

  console.log(`transfer ${ethers.formatUnits(amount, dec)} ${sym} -> ${to}`);
  console.log("El nodo relaya y mina de forma SÍNCRONA: 1-3 min. No cancelar.");

  try {
    const res = await sendRelayed({
      address: token,
      abi: ERC20_ABI,
      call: (contract, opts) => contract.transfer(to, amount, opts),
      privateKey: need("PRIVATE_KEY"),
      provider,
      nodeAddress: need("NODE_ADDRESS"),
      gasLimit: process.env.GAS_LIMIT ? Number(process.env.GAS_LIMIT) : undefined,
      onProgress: (s) => process.stdout.write(`\r  ...esperando confirmación (${s}s)   `),
    });
    process.stdout.write("\n");
    console.log("✅ ejecutada | hash:", res.txHash);
    console.log("   saldo destino:", ethers.formatUnits(await read.balanceOf(to), dec), sym);
  } catch (e) {
    process.stdout.write("\n");
    if (e instanceof RelayError) {
      // stage "send"  → lo rechazó el relay-signer antes del RelayHub (rpcCode -32000/-32007/…)
      // stage "relay" → llegó al RelayHub y falló ahí (errorName del enum ErrorCode)
      console.error("❌ falló en", e.stage, "|", e.message);
      if (e.errorName) console.error("   ErrorCode:", e.errorCode, e.errorName);
      if (e.revertReason) console.error("   revertReason:", e.revertReason);
      if (e.txHash) console.error("   hash:", e.txHash);
      if (e.meta) console.error("   relay_getMetaTxResult:", JSON.stringify(e.meta));
    } else {
      console.error("❌", e.shortMessage || e.message);
    }
    process.exitCode = 1;
  }
}

main().catch((e) => { console.error(e.shortMessage || e.message || e); process.exitCode = 1; });
