# Skill `lnet-gas-model` (distribución pública)

Conocimiento operativo para desarrollar, desplegar y consumir smart contracts en una red
**LNet / LACChain** con **gas model** (el gas lo paga el nodo relayer; el usuario firma una
meta-transacción).

## Instalar

Copia el directorio completo en una de estas rutas y reinicia Claude Code:

| Ruta | Alcance |
|---|---|
| `<tu-proyecto>/.claude/skills/lnet-gas-model/` | solo ese proyecto (recomendado: se versiona con el repo) |
| `~/.claude/skills/lnet-gas-model/` | todos tus proyectos |

Comprueba que aparece con `/skills`. Se activa solo cuando la tarea menciona el gas model,
`BaseRelayRecipient`, RelayHub, despliegues en LNet, etc.

## Configurar (obligatorio antes de usarlo)

El skill **no trae ninguna IP**: los endpoints salen de tu `.env`. Copia
`templates/.env.example` a la raíz de tu proyecto como `.env` y rellena:

| Variable | Qué es | Cómo obtenerlo |
|---|---|---|
| `RPC_URL` | endpoint de **tu** nodo **que relaya** | en un writer estándar es el puerto **80** (nginx/openresty enruta `eth_sendRawTransaction`, `eth_getTransactionCount`, `eth_getTransactionReceipt`, `relay_getMetaTxResult` al relay-signer `:9001`). **No** uses el `:4545` de besu: no relaya |
| `NODE_ADDRESS` | dirección del nodo que patrocina el gas | en el servidor del nodo: `cat nodeAddress` (o el que te dé el operador de la red) |
| `CHAIN_ID` | chain id de tu red | `eth_chainId` contra tu RPC |
| `PRIVATE_KEY` | cuenta que firma (debe estar **permisionada**) | la gestionas tú; nunca la subas al repo |
| `GAS_PRICE` | `0` en estas redes | — |

Además, en los contratos hay que fijar el **`trustedForwarder`** (`BaseRelayRecipientProxy`) de
**tu** red — la tabla de `SKILL.md` trae los de las redes públicas de LNet; para una red propia,
pídeselo al operador. Debe ser el **proxy**, nunca el RelayHub.

Comprobación rápida de que apuntas al endpoint correcto: `eth_getTransactionCount(tuCuenta,
"pending")` contra el relay devuelve el nonce de **meta-transacción** (lo lleva el RelayHub); contra
besu `:4545` devuelve el nonce EVM de la cuenta. Si ambos coinciden, no estás pasando por el relay.

## Qué incluye

| Archivo | Contenido |
|---|---|
| `SKILL.md` | reglas no negociables, datos de red, cómo montar el proyecto, diagnóstico |
| `references/gas-model.md` | firma pre-EIP155, los 64 bytes extra, `PUSH0`/Paris, deploy y upgrade UUPS |
| `references/desarrollo-contratos.md` | `BaseRelayRecipient` (3 variantes), `_msgSender()`, OZ `Context`, anti-patrones, tests |
| `references/app-web3.md` | ethers v6 + `@lacchain/gas-model-provider`: deploy, envío de tx y manejo de errores |
| `templates/` | contratos base, mock de tests, `foundry.toml`, y scripts de deploy/envío/backend |

Los ejemplos de código son plantillas: revísalos y ajústalos a tu red antes de usarlos en producción.
