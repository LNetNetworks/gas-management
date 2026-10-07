# Plan: reordenamiento de nonces + endpoints nuevos + dashboard

Rama: **`feature/add-reorden-dashboard`** (cortada de `develop`).

Objetivo: traer al RelaySigner en Go tres cosas que hoy solo existen en el relayer en Node
(`node_relayer`):

1. **Reordenamiento de metatx por nonce** — buffer que retiene la que llega adelantada hasta que se
   cierra el hueco, en vez de mandarla y que el hub conteste `BadNonce`.
2. **Endpoints nuevos** — `/info`, `/nonce/{address}`, `/relay`.
3. **Dashboard** — API de eventos (SSE) + frontend, para ver la cola de nonces en vivo.

Referencias del lado Node: `node_relayer/src/relayer.ts` (buffer y tracker), `src/app.ts` (rutas),
`src/events.ts` + `src/dashboard.ts` + `src/dashboard-page.ts` (dashboard), y los documentos
`COMPARACION-GO-NODE.md`, `ENDPOINTS-GO-VS-NODE.md`, `DASHBOARD.md`.

## Punto de partida (lo que hay hoy en Go)

| Pieza | Estado actual | Archivo |
|---|---|---|
| Rutas HTTP | **una sola**, catch-all `/`, JSON-RPC; lo no interceptado responde `method is not supported` | `main.go:78`, `controller/relayController.go:35` |
| Nonce del usuario | caché `senders` con TTL, **solo alimenta** `eth_getTransactionCount(pending)`; no valida ni reserva | `service/relaySignerService.go:55,321,745` |
| Orden de llegada | ninguno: se manda y decide el hub | `controller/processController.go` |
| Concurrencia | `lock` global que serializa **todos** los relays | `controller/processController.go:163` |
| Espera del receipt | no espera: responde el hash | `service/relaySignerService.go:98` |
| Suscripción a bloques | ya existe, con reconexión y backoff | `service/relaySignerService.go:584` |
| Log | texto libre a `./log/idbServiceLog.log` | `audit/log.go` |
| Cliente RPC | uno nuevo por request (`Connect`/`Close`) | `blockchain/client.go` |
| Tests | 735 líneas sobre `service` | `service/relaySignerService_test.go` |

## Decisiones de diseño (a confirmar antes de F4)

1. **Compatibilidad primero.** El contrato JSON-RPC actual (`POST /`) no cambia. Todo lo nuevo va
   detrás de flags en `config.toml`, y el reordenamiento arranca **apagado** por defecto.
2. **Vocabulario de eventos idéntico al de Node.** El frontend del dashboard se porta casi tal cual;
   si los nombres o campos de los eventos cambian, la página deja de pintar. Contrato a respetar:
   `relay.received`, `relay.decoded`, `relay.held` (`nonce`, `expected`, `gap`, `windowMs`),
   `relay.turn` (`heldMs`, `reason`), `relay.sent` (`transactionHash`, `hubNonce`,
   `writerNodeNonce`, `pendingForUser`), `relay.settled` (`blockNumber`, `gasUsed`, `executed`,
   `errorCodeName`), `relay.rejected` (`code`).
3. **Retener alarga la request.** Con buffer, `eth_sendRawTransaction` de una metatx adelantada no
   responde hasta que le toca el turno o vence la ventana. Es lo que hace Node y es inherente al
   reordenamiento sobre HTTP. Al vencer se responde el error de siempre, sin haber gastado una tx
   del writer node.
4. **El dashboard no es alcanzable por el puerto 80.** El nginx del writer enruta *por método*
   leyendo el body; un `GET /dashboard` no matchea la regex y termina en besu `:4545`. F3 lo expone
   solo en el puerto del relay-signer (`:9001`, acceso por túnel SSH o red interna). Abrirlo de
   verdad exige tocar la plantilla de `besu-networks` (`location /dashboard` → `:9001`), que va como
   PR aparte y **fuera del alcance de esta rama**.
5. **Sin autenticación**, igual que el resto del servicio. El dashboard expone `from`, hashes y gas
   de todas las metatx: por eso no se publica y por eso el default es `enabled = false` en cualquier
   entorno que no sea local.

## Fases

Cada fase deja el binario compilando, los tests en verde y el comportamiento actual intacto.

### F0 — Andamiaje (sin cambios de comportamiento)

- `model/Config.go`: bloques nuevos `[reorder]` (`enabled`, `windowMs`, `maxInflightPerUser`,
  `receiptTimeoutMs`) y `[dashboard]` (`enabled`, `bufferSize`), todos con defaults conservadores
  (`reorder.enabled = false`, `dashboard.enabled = false`).
- `config.toml`: las claves nuevas comentadas, como plantilla.
- Tests de carga de config con y sin las claves.

**Listo cuando**: `go build` y `go test ./...` pasan y el servicio arranca igual que hoy con un
`config.toml` viejo.

### F1 — Log estructurado + bus de eventos

- `audit/`: emisor JSON por evento (`event`, `ts`, `level`, `reqId`, campos) **en paralelo** al log
  de texto actual, que no se toca (hay operadores que lo parsean).
- `events/`: bus en memoria — ring buffer de `bufferSize` (default 500) con `seq` incremental,
  `Subscribe`/`Unsubscribe`, `Replay(afterSeq)`. Con `dashboard.enabled = false`, `Publish` es un
  `return` y no cuesta nada.
- `reqId` por request HTTP (equivalente al `AsyncLocalStorage` de Node) propagado por `context`.
- Emitir `relay.received` / `relay.decoded` / `relay.sent` / `relay.rejected` en el camino que ya
  existe.

**Listo cuando**: una ráfaga por `POST /` produce la secuencia de eventos esperada en el bus
(test unitario sobre el bus + test de integración del handler).

### F2 — Endpoints nuevos

Router por path en `main.go`, manteniendo `/` como catch-all JSON-RPC:

| Ruta | Handler | Notas |
|---|---|---|
| `GET /info` | nuevo | node address, relayHub (+ cómo se resolvió), chainId, rpcUrl, balance, `currentGasLimit` (ya se calcula), permissioning, y los parámetros de reorden |
| `GET /nonce/{address}` | nuevo | `nonce` on-chain + `nextNonce` (con lo en vuelo) + `pending`; `?peek=true` para no reservar |
| `POST /relay` | nuevo | `{rawTx}`; **espera** el receipt y devuelve el resultado decodificado, como el de Node |
| `POST /` | el de hoy | sin cambios |

`POST /relay` reutiliza el decodificado y las validaciones de `processController` (hoy acopladas al
JSON-RPC): esta fase incluye extraer esa lógica a `service` para poder llamarla desde los dos
caminos, sin duplicarla.

**Listo cuando**: los tres endpoints responden con la misma forma que los de Node (comparación campo
a campo contra `ENDPOINTS-GO-VS-NODE.md`) y `POST /` sigue pasando sus tests.

### F3 — Dashboard (API + frontend)

- `GET /dashboard/stream`: SSE con replay (`?after=<seq>` y cabecera `last-event-id`), heartbeat de
  15 s, `X-Accel-Buffering: no`, techo de 8 clientes (`503` al noveno).
- `GET /dashboard`: la página. Se porta `node_relayer/src/dashboard-page.ts` a
  `dashboard/index.html` y se sirve con `go:embed` — sin build, sin dependencias JS. La página no
  cambia: consume el mismo vocabulario de eventos (decisión 2).
- Cabeceras CORS configurables (hoy el servicio no manda ninguna).

**Listo cuando**: con `dashboard.enabled = true`, una ráfaga se ve en la página igual que en Node
(paneles de reordenamiento, línea de tiempo y eventos), incluida la reanudación tras recargar.

### F4 — Reordenamiento de nonces (el cambio de fondo)

Es el único cambio que toca el camino crítico. Orden de trabajo:

1. **Tracker de nonces en vuelo** por `(nodo, usuario)`: reemplaza el caché `senders` actual por una
   estructura autoritativa — nonce on-chain + enviadas sin receipt. `eth_getTransactionCount(pending)`
   pasa a servirse de ahí.
2. **Buffer de reordenamiento**: si `nonce > esperado`, la metatx espera a que se cierre el hueco.
   La ventana (`windowMs`, default 3000) mide **estancamiento**, no espera total: se renueva cada vez
   que la cadena avanza, para que una ráfaga larga no pierda la cola por reloj.
3. **Tope de metatx en vuelo por usuario** (`maxInflightPerUser`, default 16): acota el daño si la
   cadena de nonces se rompe.
4. **Watcher de receipts**: goroutine por metatx enviada (o un poller único) que emite
   `relay.settled` y libera el tracker. Puede colgarse de la suscripción a bloques que ya existe
   (`ProcessNewBlocks`).
5. **Revisar el `lock` global**: hoy serializa todos los relays; con el buffer hay que acotarlo al
   envío, o el reordenamiento no compra nada. Es el punto de mayor riesgo de la fase.

**Listo cuando**: `node_relayer/examples/nonce-stress.ts --n 6` apuntado al relay-signer en Go da el
mismo resultado que contra el de Node — nonces consecutivos, sin `BadNonce`, y el dashboard muestra
las líneas cruzadas del reordenamiento.

### F5 — Validación y cierre

- Ráfagas contra el nodo dev: 6, 12 y 20 metatx, un usuario y varios; caso de `MAX_INFLIGHT`
  excedido; caso de metatx que vence en el buffer.
- Comparar con la corrida de referencia de Node documentada en `node_relayer/DASHBOARD.md`
  (llegada `72,68,70,67,71,69` → envío `67..72`, 4 reordenadas, espera media 2.24 s).
- Verificar que con `reorder.enabled = false` el comportamiento es **byte a byte** el de hoy.
- Documentar en el README y actualizar `ENDPOINTS-GO-VS-NODE.md` de `node_relayer` (las diferencias
  que esta rama elimina).
- PR `feature/add-reorden-dashboard` → `develop`.

## Riesgos

| Riesgo | Mitigación |
|---|---|
| El reordenamiento rompe el camino que hoy funciona en producción | flag `reorder.enabled = false` por defecto; F4 va última y con la comparación de F5 |
| Acotar el `lock` global introduce carreras | tests con `-race`; el tracker con su propio mutex y sin llamadas RPC dentro del lock |
| Un cliente RPC por request no aguanta el watcher de receipts | evaluar cliente compartido reusado (como el `JsonRpcProvider` de Node) dentro de F4 |
| El frontend portado se desincroniza del emisor de eventos | contrato de eventos congelado (decisión 2) y test que valida los campos de cada evento |
| El dashboard queda expuesto | default apagado, solo `:9001`, y aviso en el README |
| `go-ethereum v1.9.15` (2020) limita lo que se puede usar | no actualizar en esta rama; si hace falta, va en una aparte |

## Fuera de alcance

- Autenticación del dashboard y de los endpoints nuevos.
- Tocar la plantilla de nginx en `besu-networks` (PR aparte).
- Multi-instancia: el tracker y el bus viven en memoria del proceso, igual que en Node.
- Transacciones privadas (`priv_*`/`eea_*`) y el multi-tenant de NAAS.
