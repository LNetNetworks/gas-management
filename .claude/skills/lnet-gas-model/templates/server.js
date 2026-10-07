/**
 * server.js — backend mínimo de una app web3 sobre LNet (gas model LACChain).
 *
 * POR QUÉ UN BACKEND: la metatx se firma **legacy pre-EIP155** (`chainId = 0`, `v = 27/28`) con
 * 64 bytes extra (nodeAddress + expiration) en el calldata. **MetaMask y cualquier wallet de
 * navegador NO pueden producir esa firma** (rechazan `chainId 0` y no añaden esos campos), así que
 * el navegador no firma: manda la intención a este backend, que firma con `LacchainSigner` y relaya.
 * La clave privada vive SOLO aquí (o en un KMS/HSM); nunca en el front.
 *
 * Sin dependencias de framework (node:http). Endpoints:
 *   GET  /balance?address=0x…       → lectura directa (sin relay, sin gas)
 *   POST /transfer {to, amount}     → escritura relayada; responde el resultado REAL de la metatx
 *
 * Uso: node server.js   (.env: PRIVATE_KEY, RPC_URL, NODE_ADDRESS, TOKEN, PORT?)
 */
require("dotenv").config();
const http = require("node:http");
const { ethers } = require("ethers");
const { makeProvider, readContract, sendRelayed, RelayError } = require("./lacchain");

const need = (k) => { const v = process.env[k]; if (!v) throw new Error(`Falta ${k} en .env`); return v; };
const ERC20_ABI = [
  "function transfer(address to, uint256 amount) returns (bool)",
  "function balanceOf(address) view returns (uint256)",
  "function decimals() view returns (uint8)",
];

const provider = makeProvider(need("RPC_URL"));
const TOKEN = ethers.getAddress(need("TOKEN"));
const read = readContract({ address: TOKEN, abi: ERC20_ABI, provider });

// Una metatx a la vez por cuenta firmante: dos envíos concurrentes leen el mismo nonce
// (`eth_getTransactionCount` "pending") y el RelayHub rechaza la segunda con BadNonce(2).
// En una app multiusuario: una cola por clave firmante, no un signer compartido sin serializar.
let queue = Promise.resolve();
const serialize = (fn) => (queue = queue.then(fn, fn));

const json = (res, code, body) => {
  res.writeHead(code, { "content-type": "application/json" });
  res.end(JSON.stringify(body));
};

const server = http.createServer(async (req, res) => {
  try {
    const url = new URL(req.url, "http://localhost");

    if (req.method === "GET" && url.pathname === "/balance") {
      const address = ethers.getAddress(url.searchParams.get("address"));
      const [raw, dec] = await Promise.all([read.balanceOf(address), read.decimals()]);
      return json(res, 200, { address, balance: ethers.formatUnits(raw, dec) });
    }

    if (req.method === "POST" && url.pathname === "/transfer") {
      const chunks = [];
      for await (const c of req) chunks.push(c);
      const { to, amount } = JSON.parse(Buffer.concat(chunks).toString() || "{}");
      const dec = Number(await read.decimals());
      const value = ethers.parseUnits(String(amount), dec);

      // OJO: la respuesta tarda 1-3 min (minado síncrono). En una app real, encolar y
      // devolver 202 + un id que el front consulte, en vez de bloquear la petición HTTP.
      const result = await serialize(() =>
        sendRelayed({
          address: TOKEN,
          abi: ERC20_ABI,
          call: (contract, opts) => contract.transfer(ethers.getAddress(to), value, opts),
          privateKey: need("PRIVATE_KEY"),
          provider,
          nodeAddress: need("NODE_ADDRESS"),
        }),
      );
      return json(res, 200, { ok: true, txHash: result.txHash });
    }

    return json(res, 404, { error: "not found" });
  } catch (e) {
    if (e instanceof RelayError) {
      // Errores propios del gas model: se devuelven tipados para que el front los explique.
      return json(res, 502, {
        ok: false,
        stage: e.stage,               // "send" (rechazo del relay-signer) | "relay" (RelayHub)
        errorName: e.errorName ?? null, // BadNonce, NotEnoughGas, IsNotContract, …
        errorCode: e.errorCode ?? null,
        rpcCode: e.rpcCode ?? null,   // -32000, -32007, …
        revertReason: e.revertReason ?? null,
        txHash: e.txHash ?? null,
        message: e.message,
      });
    }
    return json(res, 400, { error: e.shortMessage || e.message });
  }
});

server.listen(Number(process.env.PORT || 3000), () =>
  console.log(`API en http://localhost:${process.env.PORT || 3000} | token ${TOKEN}`),
);
