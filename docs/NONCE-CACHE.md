# Manejo del nonce en el RelaySigner (caché por sender y anti-bloqueo)

> **Pregunta:** ¿cómo resuelve `gas-management` el nonce, qué estructura usa y cómo evita que una
> address quede **bloqueada** (nonce atascado) tras una colisión?
>
> **Respuesta corto:** mantiene un **caché en memoria del "próximo nonce" por sender** para cubrir
> la ventana de minado síncrono; la **fuente de verdad** sigue siendo el `getNonce` on-chain del
> RelayHub. Cuatro mecanismos (incremento por `max`, invalidación ante `BadTransactionSent`, TTL de
> respaldo y clave normalizada + mutex) impiden que el caché quede permanentemente por delante del
> nonce real.

Aplica al **modelo clásico (RelayHub / `TxRelay`)**. El fix del atasco está en la rama **`develop`**
(commit `3b833bf`, mergeado en PR #25 `ee7f400`), reproducido y verificado contra el nodo dev con
`samples-error-tx-gas-model/scripts/10-bad-nonce-concurrent.js`. Todas las referencias `file:line`
son de `service/relaySignerService.go`.

---

## El problema de raíz

El nonce del gas model **no es el de Besu**. El RelayHub (`TxRelay`) lleva su propio contador
`nonces[node][from]` — **por par (nodo que relaya, sender original)** — y lo expone con
`getNonce(from)` (leído con `msg.sender = nodeAddress`). Cada meta-tx debe firmarse con ese nonce.

El minado en LNet es **síncrono (1–3 min)**. Si un cliente manda 2 tx seguidas dentro de esa
ventana, ambas leerían el **mismo** `getNonce` on-chain (la primera aún no se refleja) → firman el
mismo nonce → el RelayHub acepta una y la otra sale `BadTransactionSent(node, from, BadNonce)`. Para
paliarlo, el RelaySigner **adelanta el nonce** desde un caché en memoria en las lecturas
`eth_getTransactionCount(addr, "pending")`.

## La estructura: un mapa-caché por sender (no una lista/cola)

```go
// service/relaySignerService.go  (~L46, L54)
type nonceEntry struct {
    next      uint64      // PRÓXIMO nonce a usar por este sender
    updatedAt time.Time   // marca temporal para expirar la entrada (TTL)
}

type RelaySignerService struct {
    Config      *model.Config
    senders     map[string]*nonceEntry  // clave = address del sender (normalizada a lowercase)
    sendersLock sync.Mutex              // protege el mapa entre goroutines HTTP
}
```

No es un array ni una cola de nonces: es **un contador `next` por sender**. Un solo entero basta
porque los nonces son estrictamente secuenciales por (nodo, sender); no hay que recordar huecos.
Se inicializa vacío en `Init` (`service.senders = make(map[string]*nonceEntry)`, ~L81).

## Lectura y escritura

**Lectura** — `GetTransactionCount(id, from, isPending)` (~L320): si es `pending`, intenta el caché;
si no hay entrada válida, **cae al nonce real on-chain** `getNonce(from)`:

```go
var count *big.Int
if isPending {
    if next, ok := service.cachedNonce(from); ok { count = new(big.Int).SetUint64(next) }
}
if count == nil {
    // ... client.GetTransactionCount(RelayHub, from, nodeAddress) -> getNonce(from) on-chain
}
```

**Escritura** — `incrementTransactionCount(sender, nonce)` (~L745), llamada desde
`SendMetatransaction` (~L122) justo tras aceptar el envío en Besu: registra el próximo nonce del
sender.

## Cómo se evitaba el bloqueo — los 4 mecanismos del fix

El atasco permanente venía de un caché que **avanzaba `+1` a ciegas** y no tenía forma de
corregirse: tras una colisión quedaba **por delante** del nonce real y cada reintento lo alejaba
`+1` más → address bloqueada hasta reiniciar el servicio. El fix ataca las 4 causas.

### 1. Incremento por `max`, no `+1` incondicional (~L745)

```go
func (service *RelaySignerService) incrementTransactionCount(from string, nonce uint64) {
    service.sendersLock.Lock(); defer service.sendersLock.Unlock()
    key := senderKey(from)
    next := nonce + 1
    if entry := service.senders[key]; entry != nil &&
        time.Since(entry.updatedAt) <= service.nonceCacheTTL() && entry.next > next {
        next = entry.next   // conserva el mayor: dos tx con el MISMO nonce no suman 2
    }
    service.senders[key] = &nonceEntry{next: next, updatedAt: time.Now()}
}
```

Antes: `senders[from] = senders[from] + 1` siempre. Una colisión (dos tx que firman el mismo nonce)
avanzaba el caché `+2`, pero on-chain solo se consume `+1` → desfase de 1 que no se recuperaba.
Además había un **off-by-one** en el seed (la primera entrada guardaba el nonce recién usado en vez
de `nonce+1`).

### 2. Auto-reparación: `invalidateNonce` ante `BadTransactionSent` (~L734)

Es el mecanismo clave. Al post-procesar el receipt (`GetTransactionReceipt` ~L194 y
`GetMetaTxResult` ~L295), si se ve el evento de tx fallida se extrae su `originalSender` y se
**borra su entrada del caché**:

```go
errorCode, badSender := badTransactionErrorCode(id, log.Data)
service.invalidateNonce(badSender.Hex())   // esa tx NO consumió nonce on-chain
```

```go
func (service *RelaySignerService) invalidateNonce(from string) {
    service.sendersLock.Lock(); defer service.sendersLock.Unlock()
    delete(service.senders, senderKey(from))
    log.GeneralLogger.Println("nonce cache invalidated for sender:", from)
}
```

La próxima lectura `"pending"` no encuentra entrada y vuelve al `getNonce` real on-chain → el caché
se resincroniza solo. (`badTransactionErrorCode` se amplió para devolver también el `originalSender`
además del `ErrorCode`.)

### 3. TTL de respaldo (default 300 s) (~L707, L715)

Red de seguridad por si el cliente **nunca consulta el receipt** de la tx fallida (y por tanto no se
dispara la invalidación del mecanismo 2). Cada entrada caduca; pasado el TTL se descarta y se relee
on-chain:

```go
func (service *RelaySignerService) cachedNonce(from string) (uint64, bool) {
    service.sendersLock.Lock(); defer service.sendersLock.Unlock()
    key := senderKey(from)
    entry := service.senders[key]
    if entry == nil { return 0, false }
    if time.Since(entry.updatedAt) > service.nonceCacheTTL() {  // expiró
        delete(service.senders, key); return 0, false
    }
    return entry.next, true
}
```

El TTL es configurable: `nonceCacheTTL` (en segundos) en la config de `[application]`; default 300 s
si no se define (`nonceCacheTTL()` ~L707).

### 4. Clave normalizada + mutex dedicado (~L700, L55)

La misma address llegaba con **distinto case** según el camino: `eth_getTransactionCount` trae la
string cruda del cliente (ethers la manda **lowercase**), mientras que la escritura interna usaba
`message.From().Hex()` (**checksum EIP-55**). Sin normalizar se creaban **dos entradas** con estados
distintos, y el cliente podía leer una u otra según el case → lecturas incoherentes. Fix:

```go
func senderKey(from string) string { return strings.ToLower(from) }
```

Además, el mapa se tocaba desde varias goroutines HTTP sin lock (**data race**); ahora todo acceso va
protegido por `sendersLock` (`sync.Mutex`).

## Resumen del flujo anti-bloqueo

- **Fuente de verdad** = `getNonce` on-chain del RelayHub (por par nodo/sender).
- El caché solo **adelanta** el nonce durante la ventana de minado, con `max` (no `+1` ciego).
- Se **auto-repara** en cuanto una tx sale `BadNonce` (invalida al sender) → vuelve al on-chain.
- **Doble red**: si nadie lee el receipt, el **TTL** lo resincroniza igual.
- **Concurrencia segura**: clave normalizada + mutex.

## Relación con NAAS

El fork multi-tenant `naas-gas-management` **portó estos mismos mecanismos** (commit `582c28e`,
"port nonce concurrency improvements with TTL caching and RWMutex"). La diferencia es que en NAAS la
key de firma es **por empresa** (KMS), no una única del nodo; el resto del diseño de caché de nonce
por sender es equivalente.
