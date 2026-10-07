/**
 * Actualiza (upgrade) un contrato UUPS ya desplegado en LNet (gas model LACChain).
 *
 * Hace:
 *   1) Despliega la NUEVA implementación (relayado, constructor sin args).
 *   2) Llama proxy.upgradeToAndCall(nuevaImpl, "0x") como owner (relayado).
 *
 * El proxy conserva su dirección y estado; solo cambia la lógica.
 *
 * Uso:
 *   forge build
 *   PROXY=0x... node update-upgradeable.js                       # usa LnetTokenUpgradeableV2
 *   PROXY=0x... CONTRACT_V2=MiImplV3 node update-upgradeable.js
 *   node update-upgradeable.js 0x<proxy>                         # proxy como argumento
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

// Lee la dirección del contrato creado por el RelayHub desde el opcode CREATE del trace.
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

function withHeartbeat(label) {
  console.log(`\n${label}`);
  console.log("El nodo relaya y mina de forma SÍNCRONA: puede tardar 1-3 min. No lo canceles.");
  const t0 = Date.now();
  const beat = setInterval(() => {
    process.stdout.write(`\r  ...esperando confirmación del nodo (${Math.round((Date.now() - t0) / 1000)}s)   `);
  }, 2000);
  return () => {
    clearInterval(beat);
    process.stdout.write("\n");
  };
}

async function main() {
  const RPC_URL = req("RPC_URL");
  const PRIVATE_KEY = req("PRIVATE_KEY");
  const NODE_ADDRESS = req("NODE_ADDRESS");

  const proxyAddress = process.env.PROXY || process.argv[2];
  if (!proxyAddress) throw new Error("Indica el proxy: PROXY=0x... o como argumento.");
  const v2Name = process.env.CONTRACT_V2 || "LnetTokenUpgradeableV2";

  const v2Artifact = loadArtifact(v2Name);
  const gasLimit = process.env.GAS_LIMIT ? Number(process.env.GAS_LIMIT) : 8_000_000;
  const expiration = process.env.EXPIRATION_MS
    ? Number(process.env.EXPIRATION_MS)
    : Date.now() + 10 * 60 * 1000;

  const provider = new LacchainProvider(RPC_URL);
  const signer = new LacchainSigner(PRIVATE_KEY, provider, NODE_ADDRESS, expiration);
  const caller = await signer.getAddress();

  console.log("== Upgrade UUPS en LNet (gas model) ==");
  console.log("Proxy        :", proxyAddress);
  console.log("Nueva impl   :", v2Name);
  console.log("Caller (owner):", caller);

  // Estado/owner antes del upgrade (lectura).
  const tokenRead = new ethers.Contract(proxyAddress, v2Artifact.abi, provider);
  const ownerOnChain = await tokenRead.owner();
  const supplyBefore = await tokenRead.totalSupply();
  console.log("Owner on-chain:", ownerOnChain, "| totalSupply:", supplyBefore.toString());
  if (ownerOnChain.toLowerCase() !== caller.toLowerCase()) {
    throw new Error("La cuenta no es el owner del proxy; upgradeToAndCall revertiría.");
  }

  // 1) Desplegar la nueva implementación (constructor sin args).
  let stop = withHeartbeat(`Desplegando nueva implementación ${v2Name}... (gasLimit ${gasLimit})`);
  let implAddr, implTx;
  try {
    const factory = new ethers.ContractFactory(v2Artifact.abi, v2Artifact.bytecode.object, signer);
    const c = await factory.deploy({ gasLimit, gasPrice: 0 });
    const r = await c.deploymentTransaction()?.wait();
    implTx = r?.hash;
    implAddr = r?.contractAddress;
    if (!implAddr || implAddr === "0x" + "0".repeat(40)) implAddr = await findCreatedAddress(provider, implTx);
  } finally { stop(); }
  console.log(`  Nueva implementación → ${implAddr}  | tx ${implTx}`);
  if (!implAddr) throw new Error("No se pudo resolver la dirección de la nueva implementación.");

  // 2) upgradeToAndCall(nuevaImpl, "0x") como owner (relayado).
  stop = withHeartbeat("Ejecutando upgradeToAndCall en el proxy...");
  let upTx;
  try {
    const proxy = new ethers.Contract(proxyAddress, v2Artifact.abi, signer);
    const tx = await proxy.upgradeToAndCall(implAddr, "0x", { gasLimit, gasPrice: 0 });
    const r = await tx.wait();
    upTx = r?.hash;
  } finally { stop(); }
  console.log(`  upgradeToAndCall  | tx ${upTx}`);

  // Verificación: slot de implementación + version() + estado preservado.
  const IMPL_SLOT = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc";
  const slot = await provider.getStorage(proxyAddress, IMPL_SLOT);
  const slotAddr = ethers.getAddress("0x" + slot.slice(-40));

  console.log("\n✅ Upgrade completado!");
  console.log("Proxy            :", proxyAddress);
  console.log("Implementación   :", implAddr);
  console.log("Slot ERC1967     :", slotAddr, slotAddr.toLowerCase() === implAddr.toLowerCase() ? "✓ coincide" : "✗ NO coincide");

  try {
    console.log("\n== Verificación (vía proxy) ==");
    console.log("version     :", await tokenRead.version());
    console.log("name        :", await tokenRead.name());
    console.log("totalSupply :", (await tokenRead.totalSupply()).toString(), "(antes:", supplyBefore.toString() + ")");
    console.log("owner       :", await tokenRead.owner());
  } catch (e) {
    console.log("(No se pudo verificar automáticamente:", e.message, ")");
  }
}

main().catch((err) => {
  console.error("\n❌ Error en el upgrade:", err.message || err);
  process.exitCode = 1;
});
