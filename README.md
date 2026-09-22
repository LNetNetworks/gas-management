# Gas Management

This solution is in charge of distributing gas to the different LACChain Besu writer nodes, it is composed of backend components such as smart contracts. Gas distribution is automatic, whose logic is written in smart contracts. 

## Package overview

1. **audit** contains ways to log.
2. **blockchain** contains connections to Ethereum.
3. **controller** controller layer that receives all external requests and redirects requests to the service layer
4. **service** contains main logic
5. **model** contains data models of requests and responses of APIs
6. **errors** contains different errors types
7. **relayhub** contains all smart contract 
8. **rpc** contains models and ways to interact with RPC request and response
9. **docs** contains documentation about architecture and developer interaction with this 
solution

## Prerequisites

* Being a validator node in LACChain network
* Go 1.13+ installation or later
* **GOPATH** environment variable is set correctly

## Install

```
$ git clone https://github.com/lacchain/gas-management

$ cd gas-management
$ make build      # builds with the version injected from the git tag (see "Version")
```

> Plain `go build` works too, but leaves the version as `dev`. Use `make build` (or the
> `-ldflags`) so the binary reports the real version.

## Run

Execute the executable file generated previously in a Validator node

```
$ ./gas-relay-signer
```

## Configuration

The keys below were added by the `01`..`05` changes (event bus, HTTP endpoints, dashboard, nonce
reordering and metatx validation). Every one of them is **optional**: when a key is absent the
service applies the default listed here, and with all defaults in place it behaves exactly as it
did before those changes. That is why `config.toml` ships them commented out — a commented key
follows the binary's default and picks up a new one on upgrade, while an explicit key freezes the
value on every node the template is copied to.

Absence is meaningful: the blocks are read key by key, so an invalid value falls back to its
default and is logged instead of aborting startup, and a key written with its default value is not
the same as a missing key (see `dashboard.bufferSize`).

### `[reorder]` — nonce tracking, reordering and receipt watching

| Key | Default | What it does |
|---|---|---|
| `enabled` | `false` | Turns on the authoritative nonce tracker, the hold-and-reorder buffer and the receipt watcher. With it off none of the three runs. **Observable change when on:** `eth_sendRawTransaction` for an out-of-order metatx does not answer until its turn comes or the window expires. |
| `windowMs` | `3000` | How long a metatx may stay held **without the expected nonce advancing**. It measures stalling, not total wait: it is renewed every time the chain moves forward, so a long burst does not lose its tail to the clock. Also the grace period before forgetting a user with nothing in flight. |
| `maxInflightPerUser` | `5` | Cap of metatx of the same user in flight or held. Bounds the damage when a nonce chain breaks. **What gets discarded above the cap is the highest nonce**, not the last to arrive: a metatx arriving with a nonce lower than some held one evicts the highest held and takes its place, because the low one is what can unblock the queue. Discarding from the middle of a chain would orphan everything after it. The cap bounds **admission, not departure** — a metatx already admitted and in turn is sent without re-checking it — so the *number* of survivors of an over-cap burst is not fixed; who gets discarded is. **The default matches the ceiling the network imposes, and that is the criterion for choosing it — not the number.** Besu limits how many pending transactions it accepts from a single account (`tx-pool-limit-by-account-percentage`, ~5 with defaults) and that account is the writer node, sender of every wrapper transaction. If the validators raise theirs, raise this one. Going above the ceiling mines no extra metatx — Besu decides — and makes rejections worse: the surplus occupies the whole reorder window only to be rejected for a wrong nonce, which is not the real reason. Measured with bursts of 12 from one user, 4 runs per value: with `5`, five are mined and **none** dies by nonce; with `7`, the same five are mined and 1-2 die by nonce after waiting the window; with `16`, 6 of 7 rejections are by nonce after ~3.8 s instead of ~0.7 s. Lowering it does **not** cost throughput — the eviction picks by nonce, so the chain does not break — but going *below* the ceiling does: with `2`, one user mines only 2 out of 12. |
| `receiptTimeoutMs` | `60000` | How long the result of a sent metatx is awaited before declaring it undetermined and releasing its slot. |
| `autoNonce` | `false` | Hands out nonces: queries from the same user are serialised and each one gets a different number. Off, querying reserves nothing and two clients asking at once get the same value. |
| `autoNonceTicketMs` | `2000` | How long a handed-out nonce waits for the metatx that uses it. It is a **ticket, not a reservation**: if it expires unused the next caller gets that same number, so a client that asks and never sends does not block the queue. |

### `[validation]` — gas model suffix

Rejects, **before spending a writer node transaction**, what the RelayHub would reject on-chain.
Both switches default to `false`, the opposite of the reference relayer: turning them on changes
which metatx are accepted. A suffix that is absent, short or carries an unrepresentable expiration
never rejects anything — what is validated is a present, readable suffix that says something
unacceptable.

| Key | Default | What it does |
|---|---|---|
| `enforceNodeAddress` | `false` | Rejects a metatx whose embedded node address is not this service (`WRONG_NODE_ADDRESS`). |
| `enforceExpiration` | `false` | Rejects an expired metatx (`EXPIRED`) and one arriving without the minimum window (`EXPIRATION_TOO_LOW`). |
| `minExpirationSeconds` | `300` | Minimum validity a metatx must still have on arrival. An expiration on the edge is useless: validating, waiting for the nonce turn and mining take seconds. Only applies with `enforceExpiration`. |
| `expirationToleranceSeconds` | `2` | Tolerance the minimum is applied with — the real floor is minimum minus this. Whoever signs `now + 300` arrives with 298 (request latency plus second rounding on both sides), and demanding the exact value would reject precisely the client that did the right thing. `0` demands the exact value. |

While `enforceExpiration` is off, `GET /info` reports both numbers as **zero**: publishing a value
that is not applied would make a client sign to satisfy a rule that does not exist.

### `[security]` — where the account rules contract comes from

These two join the pre-existing `permissionsEnabled` and `accountContractAddress`.

| Key | Default | What it does |
|---|---|---|
| `accountIngressAddress` | *(empty)* | Permissioning registry the rules contract is resolved from **when `accountContractAddress` is not set**. The configured address takes precedence, so an existing deployment does not change behaviour on upgrade. Empty means no registry is queried. `GET /info` reports which of the two sources was used. |
| `accountRulesCacheMs` | `30000` | How long a per-account permission result stays valid. Without a cache every metatx pays a chain call; with it, granting an account takes up to this long to be seen. |

### `[dashboard]` — live monitor

| Key | Default | What it does |
|---|---|---|
| `enabled` | `false` | Publishes events to the in-memory bus and registers `GET /dashboard` and `GET /dashboard/stream`. With it off those routes are **not registered** at all. The monitor does not authenticate and exposes the `from`, hashes and gas of every metatx, which is why it is off by default. |
| `bufferSize` | `500` | Events retained for a late subscriber. An explicit `0` leaves the bus with no capacity — inert, same as `enabled = false`. This is the one key where zero has its own meaning rather than being an unwritten default. |

### `[log]` — structured log

| Key | Default | What it does |
|---|---|---|
| `level` | `"info"` | Minimum level written to standard output: `debug`, `info`, `warn` or `error`. |
| `rawTx` | `false` | Dumps the full signed transaction in `relay.received`. Off by default: a deploy is several KB of initcode and long lines get truncated, losing the rest of the event. The raw tx is still identified by its hash and size. |

### `[cors]` — cross-origin headers

| Key | Default | What it does |
|---|---|---|
| `allowedOrigins` | *(empty)* | Origins allowed to call the service from a browser. Empty emits **no** header at all, which is how the service has always behaved. Closed by default matters: the service relays with the node's gas quota and asks for no authentication, so opening it is the operator's decision and not the binary's. `"*"` allows anyone. |

## Version

The binary reports its version:

```
$ ./gas-relay-signer --version
gas-relay-signer v1.1.0 (commit 5b7a3a7, built 2026-07-01T22:48:36Z, go1.23.0)
```

The version is the **git tag** (`git describe --tags`), injected at build time through
`-ldflags "-X main.version=... -X main.commit=... -X main.date=..."` (that is what `make build`
does). Built on tag `v1.1.0` it reports `v1.1.0`; on `develop` with no tag, something like
`v1.0.1-9-g5b7a3a7`. Without `ldflags` it reports `dev`.

### Publishing a release (manual)

1. Merge `develop` → `master` (PR) and check out an up-to-date `master`.
2. Create the annotated tag and push it:
   ```
   git tag -a v1.1.0 -m "gas-relay-signer v1.1.0"
   git push origin v1.1.0
   ```
3. Build the artifact with the version injected and publish the release:
   ```
   make build VERSION=v1.1.0
   gh release create v1.1.0 gas-relay-signer --title "v1.1.0" --notes "..."
   ```

## HTTP routes

Besides the `POST /` JSON-RPC catch-all, which does not change, the service exposes three REST
routes.

The writer node's nginx routes **by method**, reading the request body, so a `GET` never matches
and would end up at Besu. The testnet template in `besu-networks` therefore declares these three
routes explicitly and they are reachable on port 80; the dashboard is deliberately left out, since
it does not authenticate and exposes the `from`, hashes and gas of every metatx. On a node whose
nginx has not been updated they are still reachable on the service port (`:9001`) from the internal
network or through an SSH tunnel.

Like the rest of the service, these routes require no authentication: `GET /info` reveals addresses
and the node balance, and `POST /relay` consumes its gas quota just like the JSON-RPC path.

```bash
curl -s http://localhost:9001/info
curl -s http://localhost:9001/nonce/0xAbC...
curl -s "http://localhost:9001/nonce/0xAbC...?peek=true"
curl -s -X POST http://localhost:9001/relay \
  -H 'content-type: application/json' -d '{"rawTx":"0xf8aa..."}'   # "signedTransaction" also accepted
```

### `GET /info`

Returns which addresses this node is using, where each one came from, and the parameters it is
operating with. This is where the `relayHubProxyAddress` used as the contracts' `trustedForwarder`
comes from.

A value that cannot be obtained is reported **without a value** rather than omitted, and the route
answers anyway: it is the one you turn to when something is wrong, so it cannot be the first to
fall over.

### `GET /nonce/{address}`

`{address, nonce, nonceHex, nextNonce, nextNonceHex, pending}`. `nonce` is what the RelayHub says;
`nextNonce` is what must be signed now, counting the metatx already relayed and not yet mined.
Chaining without `nextNonce` produces repeated nonces.

`?peek=true` asks to look without taking a place in the queue. With `reorder.autoNonce` off — the
default — it does not change the answer, because there is no hand-out to avoid. With hand-out on,
queries from the same user are serialised and each one gets a different number; `peek` looks
without entering that queue.

What is handed out is a TURN and not a reservation: querying does not advance the next nonce by
itself. A number handed out and never used blocks nobody — when `autoNonceTicketMs` expires the
next caller gets that same number — and what makes two clients get different numbers is the first
one's metatx arriving.

### `POST /relay`

Relays the metatx and answers only once its outcome is known. It goes through exactly the same
validations as `POST /`.

A revert in the target contract is answered with a success status and `executed: false`: the metatx
**was** relayed, what failed was the destination. A rejection answers `400` with
`{error, code, details}`.

A timed-out wait answers `RECEIPT_TIMEOUT` with the hash: the metatx **was sent** and may still be
mined. Treating that as a rejection and resending it produces a repeated nonce.

### Differences with the Node relayer

The routes are field-for-field compatible except for the following, which comes from capabilities
this service does not have yet:

| Field | Node | Here | Why |
|---|---|---|---|
| `relayHubSource` (`/info`) | `config` or `proxy` | always `proxy` | the address is always resolved from the proxy |
| `reorderEnabled`, `receiptTimeoutMs` (`/info`) | do not exist | present | parameters specific to this service |
| `simulated` (`/relay`) | whether there was a pre-check | always `false` | there is no `eth_call` simulation pre-check |
| `errorCode` (`/relay`) | from the simulation or the hub | from the hub only | same reason |
| `output` (`/relay`) | return data, or the revert reason | the revert reason, or no value | the return data of a successful call is not exposed yet |

Of the thirteen error codes in the Node catalogue, this service produces twelve: `BAD_RAW_TX`,
`BAD_META_TX`, `BAD_NONCE`, `WRONG_NODE_ADDRESS`, `EXPIRED`, `EXPIRATION_TOO_LOW`,
`TOO_MANY_INFLIGHT`, `SENDER_NOT_PERMITTED`, `PERMISSIONING_UNAVAILABLE`, `SEND_FAILED`,
`RECEIPT_TIMEOUT` and `RELAY_ERROR`. The missing one is `SIMULATION_FAILED`, which belongs to the
simulation pre-check that does not exist yet. Anything without its own code uses `RELAY_ERROR`:
codes outside the catalogue are never invented.

### Gas model suffix validation

The gas model appends to the metatx `data` the address of the node that must relay it and its
expiration. With `validation.enforceNodeAddress` the service rejects a metatx aimed at **another**
writer node, and with `validation.enforceExpiration` an expired one and one arriving without the
minimum window. Both rejections happen **before** spending a writer node transaction: without them
the hub finds out on-chain with the transaction already consumed.

The minimum is enforced with tolerance (`expirationToleranceSeconds`, 2 s by default). Whoever
signs `now + 300` arrives with 298 — request latency plus second rounding on both sides — so
demanding the exact value would reject precisely the client that did the right thing.

Both switches start **off**, the opposite of the Node relayer: turning them on changes which metatx
are accepted. With both `false` the service accepts exactly what it accepted before. For the same
reason `GET /info` reports `minExpirationSeconds` and `expirationToleranceSeconds` as zero while
the check is off: publishing a number that is not applied would make a client sign to satisfy a
rule that does not exist.

A suffix that is absent, short, or carries an expiration that does not fit in an integer rejects
**nothing**: what is validated is a present, readable suffix that says something unacceptable.

### Where the account rules contract comes from

If `security.accountContractAddress` is set, that one is used — which is what an existing
deployment does, and why upgrading the binary does not change its behaviour. If it is not set, the
contract is resolved from the network's permissioning registry (`security.accountIngressAddress`).
`GET /info` reports which of the two sources was used: a wrong address is indistinguishable from a
correct one if you cannot tell where it came from.

A network that exposes no rules contract — no registry, or no contract published — simply has no
account permissioning, and the service starts and relays all the same. But if `permissionsEnabled`
is `true` and **none** could be resolved, the metatx is rejected with `PERMISSIONING_UNAVAILABLE`:
if somebody asked for the check and there is nothing to check against, letting it through would be
opening the door while believing otherwise.

The check result is cached per account (`security.accountRulesCacheMs`, 30 s by default). The
trade-off is explicit: without a cache every metatx pays a chain call; with it, granting an account
takes up to that long to be seen.

At startup the service also checks whether **this** node is permitted and reports it in `GET /info`.
It does not prevent startup: a node that is not permitted relays with no apparent error and all of
its metatx fail on-chain, and that is diagnosed better by seeing it in `/info` than with a binary
that refuses to come up.

### Nonce reordering

With `reorder.enabled = true` the service stops sending and waiting to see what the hub says:

- **It validates the nonce before sending.** An already consumed one is rejected with `BAD_NONCE`
  without spending a writer node transaction.
- **It holds an out-of-order metatx** until the gap closes, instead of spending it to discover it
  arrived out of order. The window measures **stalling**: it is renewed every time the expected
  nonce advances, so a long burst does not lose its tail to the clock. On expiry it answers the
  same `BAD_NONCE`, having sent nothing.
- **It caps the burst per user** with `maxInflightPerUser`: beyond it, `TOO_MANY_INFLIGHT` — always
  to the highest nonce among the candidates, so the chain that goes out has no gaps.
- **It detects how each metatx ended** without the client asking, and releases its slot.

Observable semantics that change with the flag on: `eth_sendRawTransaction` for an out-of-order
metatx **does not answer** until its turn comes or the window expires. This is inherent to
reordering over HTTP and is what the Node relayer does.

With the flag off — the default — none of this runs and the behaviour is the usual one.

## Know More

* [In depth overview of the GAS distribution mechanism](https://github.com/LACNetNetworks/gas-management/blob/master/docs/OVERVIEW.md)
* [How to adapt you solution to the GAS distribution mechanism](https://github.com/LACNetNetworks/gas-management/blob/master/docs/How_adapt_your_Dapp.md)
* [Deploy your first ERC20 and time-stamping (notarization) smart contracts](https://github.com/LACNetNetworks/gas-management/blob/master/docs/tutorial/Deploy_SmartContract.md)
* [Deploy and interact with the LACChain ID verifiable credential registry smart contract](https://github.com/LACNetNetworks/gas-management/blob/master/docs/tutorial/VC_en.md)
* [Stress testing and performance of the network with the GAS distribution mechanism](https://github.com/LACNetNetworks/gas-management/blob/master/docs/STRESS_TESTING.md)
* [Comparison with Ethereum](https://github.com/LACNetNetworks/gas-management/blob/master/docs/COMPARISON_WITH_ETHEREUM.md)
* [FAQ](https://github.com/LACNet-Networks/gas-management/blob/master/docs/FAQ.md)
* [Reporting a failed inner call (status=1 → failed)](docs/RECEIPT-FALLO-INTERNO.md) — how the RelaySigner rewrites the receipt to `status=0` plus `revertReason` and exposes `relay_getMetaTxResult` (branch `develop`, document in Spanish).
* [Nonce handling (per-sender cache and anti-blocking)](docs/NONCE-CACHE.md) — in-memory cache of the next nonce per sender, and the 4 mechanisms that keep an address from getting stuck after a `BadNonce` collision (branch `develop`, document in Spanish).

## Copyright 2022 LACNet

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
