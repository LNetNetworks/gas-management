#!/usr/bin/env bash
#
# Despliegue en la red LNet (gas model LACChain) usando Foundry.
#
# Foundry NO puede hacer el broadcast en esta red (el gas model exige firma legacy
# pre-EIP155 que forge/cast no producen). Por eso el flujo es híbrido:
#   1) Foundry COMPILA el contrato (forge build, con evm_version=paris).
#   2) lacchain-deploy/deploy.js hace el BROADCAST vía el signer de LACChain.
#
# Uso:
#   ./deploy.sh                      # despliega LnetToken (token simple)
#   ./deploy.sh LnetTokenUpgradeable # despliega un contrato UUPS (implementación + proxy)
#
# Los contratos cuyo nombre termina en "Upgradeable" se despliegan con deploy-upgradeable.js
# (implementación + ERC1967Proxy); el resto con deploy.js.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
CONTRACT="${1:-LnetToken}"

echo "==> [1/2] Compilando con Foundry (evm_version=paris)..."
forge build --root "$ROOT"

cd "$ROOT/lacchain-deploy"
[ -d node_modules ] || npm install

case "$CONTRACT" in
  *Upgradeable)
    echo "==> [2/2] Desplegando $CONTRACT (UUPS: implementación + proxy) en LNet..."
    CONTRACT="$CONTRACT" node deploy-upgradeable.js
    ;;
  *)
    echo "==> [2/2] Desplegando $CONTRACT en LNet (gas model)..."
    CONTRACT="$CONTRACT" node deploy.js
    ;;
esac
