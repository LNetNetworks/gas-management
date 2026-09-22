/**
 * Despliegue de un contrato UUPS upgradeable en la red LNet (gas model LACChain).
 *
 * Hace DOS despliegues relayados:
 *   1) La implementación (LnetTokenUpgradeable), constructor sin args.
 *   2) El ERC1967Proxy(impl, initData), donde initData = initialize(owner).
 *
 * Foundry compila los artifacts (out/); este script firma/relaya con el signer de LACChain.
 * La dirección con la que se interactúa es la del PROXY.
 *
 * Uso:
 *   forge build
 *   node deploy-upgradeable.js                       # despliega LnetTokenUpgradeable
 *   CONTRACT=LnetTokenUpgradeable node deploy-upgradeable.js
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

function loadArtifact(name) {
  const p = path.resolve(__dirname, `../out/${name}.sol/${name}.json`);
  if (!fs.existsSync(p)) {
    throw new Error(`No existe el artifact ${p}. Ejecuta primero: forge build`);
  }
  return JSON.parse(fs.readFileSync(p, "utf8"));
}

// La dirección real del contrato creado por el RelayHub se lee del resultado del
// opcode CREATE en el trace (keccak(deployer,nonce) NO aplica en el gas model).
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

// Despliega un contrato (relayado) y devuelve { address, txHash }.
async function deployOne(provider, signer, label, abi, bytecode, args, gasLimit) {
  console.log(`\nDesplegando ${label}... (gasLimit ${gasLimit}, gasPrice 0)`);
  console.log("El nodo relaya y mina de forma SÍNCRONA: puede tardar 1-3 min. No lo canceles.");

  const t0 = Date.now();
  const beat = setInterval(() => {
    process.stdout.write(`\r  ...esperando confirmación del nodo (${Math.round((Date.now() - t0) / 1000)}s)   `);
  }, 2000);

  let receipt, txHash;
  try {
    const factory = new ethers.ContractFactory(abi, bytecode, signer);
    const contract = await factory.deploy(...args, { gasLimit, gasPrice: 0 });
    receipt = await contract.deploymentTransaction()?.wait();
    txHash = receipt?.hash;
  } finally {
    clearInterval(beat);
    process.stdout.write("\n");
  }

  const ZERO = "0x" + "0".repeat(40);
  let address = receipt?.contractAddress;
  if (!address || address.toLowerCase() === ZERO) {
    address = await findCreatedAddress(provider, txHash);
  }
  console.log(`  ${label} → ${address || "(no encontrada)"}  | tx ${txHash}`);
  if (!address) throw new Error(`No se pudo resolver la dirección de ${label}`);
  return { address, txHash };
}

async function main() {
  const RPC_URL = req("RPC_URL");
  const PRIVATE_KEY = req("PRIVATE_KEY");
  const NODE_ADDRESS = req("NODE_ADDRESS");
  const contractName = process.env.CONTRACT || "LnetTokenUpgradeable";

  const implArtifact = loadArtifact(contractName);
  const proxyArtifact = loadArtifact("ERC1967Proxy");
  const gasLimit = process.env.GAS_LIMIT ? Number(process.env.GAS_LIMIT) : 8_000_000;

  const expiration = process.env.EXPIRATION_MS
    ? Number(process.env.EXPIRATION_MS)
    : Date.now() + 10 * 60 * 1000;

  const provider = new LacchainProvider(RPC_URL);
  const signer = new LacchainSigner(PRIVATE_KEY, provider, NODE_ADDRESS, expiration);
  const owner = await signer.getAddress();

  console.log("== Despliegue UUPS upgradeable en LNet (gas model) ==");
  console.log("Contrato     :", contractName);
  console.log("RPC          :", RPC_URL);
  console.log("Node address :", NODE_ADDRESS);
  console.log("Expiration   :", expiration, `(${new Date(expiration).toISOString()})`);
  console.log("Deployer/owner:", owner);

  // 1) Implementación (constructor sin args).
  const impl = await deployOne(
    provider, signer, `${contractName} (implementación)`,
    implArtifact.abi, implArtifact.bytecode.object, [], gasLimit
  );

  // 2) Proxy ERC1967 con initData = initialize(owner).
  const initData = new ethers.Interface(implArtifact.abi).encodeFunctionData("initialize", [owner]);
  const proxy = await deployOne(
    provider, signer, "ERC1967Proxy",
    proxyArtifact.abi, proxyArtifact.bytecode.object, [impl.address, initData], gasLimit
  );

  console.log("\n✅ Despliegue completo!");
  console.log("Implementación:", impl.address);
  console.log("Proxy (USAR ESTE):", proxy.address);

  // Verificación on-chain a través del proxy (con la ABI de la implementación).
  const token = new ethers.Contract(proxy.address, implArtifact.abi, provider);
  try {
    console.log("\n== Verificación (vía proxy) ==");
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
