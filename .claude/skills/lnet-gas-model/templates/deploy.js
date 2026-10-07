/**
 * Despliegue de un contrato compilado por Foundry en la red LNet (gas model LACChain).
 *
 * Foundry compila (artifact en ../out), y este script hace el broadcast con el signer
 * de LACChain, que añade los campos NodeAddress + ExpirationDate y firma la transacción
 * legacy pre-EIP155 (v=27/28) que el RelayHub espera.
 *
 * Uso:
 *   npm install
 *   node deploy.js                       # despliega LnetToken (por defecto)
 *   CONTRACT=LnetToken node deploy.js    # o especifica el contrato
 */
const fs = require("fs");
const path = require("path");
require("dotenv").config({ path: path.resolve(__dirname, "../.env") });
const { ethers } = require("ethers");
const { LacchainProvider, LacchainSigner } = require("@lacchain/gas-model-provider");

function req(name) {
  const v = process.env[name];
  if (!v) throw new Error(`Falta la variable de entorno ${name} en ../.env`);
  return v;
}

// Obtiene la dirección real del contrato creado por el RelayHub, leyendo el
// resultado del opcode CREATE en el trace (debug_traceTransaction).
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
    /* nodo sin debug_traceTransaction: se devuelve null */
  }
  return null;
}

async function main() {
  const RPC_URL = req("RPC_URL");
  const PRIVATE_KEY = req("PRIVATE_KEY");
  const NODE_ADDRESS = req("NODE_ADDRESS");

  // El contrato a desplegar (nombre del archivo y del contrato).
  const contractName = process.env.CONTRACT || "LnetToken";

  // Carga el artifact generado por Foundry: ../out/<file>.sol/<contract>.json
  const artifactPath = path.resolve(
    __dirname,
    `../out/${contractName}.sol/${contractName}.json`
  );
  if (!fs.existsSync(artifactPath)) {
    throw new Error(
      `No existe el artifact ${artifactPath}. Ejecuta primero: forge build`
    );
  }
  const artifact = JSON.parse(fs.readFileSync(artifactPath, "utf8"));
  const abi = artifact.abi;
  const bytecode = artifact.bytecode.object;

  // Expiración: el modelo de LACChain usa MILISEGUNDOS (Date.getTime()).
  // Por defecto: ahora + 10 minutos. Se puede forzar con EXPIRATION_MS.
  const expiration = process.env.EXPIRATION_MS
    ? Number(process.env.EXPIRATION_MS)
    : Date.now() + 10 * 60 * 1000;

  const provider = new LacchainProvider(RPC_URL);
  const signer = new LacchainSigner(PRIVATE_KEY, provider, NODE_ADDRESS, expiration);
  const owner = await signer.getAddress();

  console.log("== Despliegue LNet (gas model) ==");
  console.log("Contrato     :", contractName);
  console.log("RPC          :", RPC_URL);
  console.log("Node address :", NODE_ADDRESS);
  console.log("Expiration   :", expiration, `(${new Date(expiration).toISOString()})`);
  console.log("Deployer/owner:", owner);

  const factory = new ethers.ContractFactory(abi, bytecode, signer);

  // El constructor de LnetToken NO recibe argumentos (el owner se toma de _msgSender()).
  // Se pasa gasLimit explícito y gasPrice 0: el gas model no soporta eth_estimateGas
  // para creación, así que evitamos la estimación automática.
  const gasLimit = process.env.GAS_LIMIT ? Number(process.env.GAS_LIMIT) : 8_000_000;
  console.log(`\nDesplegando ${contractName}... (gasLimit ${gasLimit}, gasPrice 0)`);
  console.log("El nodo relaya y mina de forma SÍNCRONA: puede tardar 1-3 min. No lo canceles.");

  // Heartbeat para que no parezca congelado mientras el nodo procesa.
  const t0 = Date.now();
  const beat = setInterval(() => {
    process.stdout.write(`\r  ...esperando confirmación del nodo (${Math.round((Date.now() - t0) / 1000)}s)   `);
  }, 2000);

  let receipt, txHash;
  try {
    const contract = await factory.deploy({ gasLimit, gasPrice: 0 });
    receipt = await contract.deploymentTransaction()?.wait();
    txHash = receipt?.hash;
  } finally {
    clearInterval(beat);
    process.stdout.write("\n");
  }

  // En el gas model el contrato lo crea el RelayHub (CREATE interno). El receipt suele
  // traer contractAddress = 0x0, así que si falta o es cero, se resuelve leyendo el
  // resultado del opcode CREATE en el trace (keccak(deployer,nonce) NO aplica aquí).
  const ZERO = "0x" + "0".repeat(40);
  let contractAddress = receipt?.contractAddress;
  if (!contractAddress || contractAddress.toLowerCase() === ZERO) {
    contractAddress = await findCreatedAddress(provider, txHash);
  }

  console.log("\n✅ Contrato desplegado!");
  console.log("Address      :", contractAddress || "(no encontrada en el trace)");
  console.log("Tx hash      :", txHash);

  // Verificación on-chain (lectura vía provider).
  if (!contractAddress) return;
  const token = new ethers.Contract(contractAddress, abi, provider);
  try {
    console.log("\n== Verificación ==");
    console.log("name        :", await token.name());
    console.log("symbol      :", await token.symbol());
    console.log("decimals    :", (await token.decimals()).toString());
    console.log("totalSupply :", (await token.totalSupply()).toString());
    console.log("owner       :", await token.owner());
  } catch (e) {
    console.log("(No se pudo verificar automáticamente:", e.message, ")");
  }
}

main().catch((err) => {
  console.error("\n❌ Error en el despliegue:", err.message || err);
  process.exitCode = 1;
});
