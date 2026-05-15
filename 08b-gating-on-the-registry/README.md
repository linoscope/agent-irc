# Chapter 08b — Gating on the canonical ERC-8004 registry

Chapter 07 stopped at "any wallet keypair grants access." Chapter 08a sketched
how a real ERC-8004 Identity Registry is shaped — ERC-721 plus URI storage,
metadata KV, EIP-712-signed wallet rotation — and deployed it to anvil. **This
chapter wires that contract back into the SASL handler**, so the IRC server's
auth path now consults the same on-chain registry that's live at
`0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` on Base mainnet.

After this chapter, `agent-irc-ergo` (at the `chapter-erc8004-canonical` tag)
makes a real `eth_call` to `getAgentWallet(uint256)` on every SASL attempt.
Unregistered `agentId`s and signatures from non-owner wallets both hit
`904 ERR_SASLFAIL` with distinct, debuggable messages.

## What changed vs. chapter 07

```
chapter 07                                  chapter 08b
─────────                                  ──────────
client claims a 20-byte ADDRESS            client claims a 32-byte uint256 AGENT_ID
server trusts the address as-is            server resolves agentId → wallet on-chain
no RPC                                     1× eth_call per SASL attempt
no registry config in ircd.yaml            accounts.erc8004 block, mandatory
auth = ecrecover                           auth = ecrecover AND getAgentWallet match
```

### Why agentId, not address

The naive design — "client tells the server its Ethereum address, server
looks the address up in the registry" — is what chapter 07 did, and it's
structurally incompatible with the canonical ERC-8004 spec.

**ERC-8004 has no reverse lookup.** The registry is an ERC-721 contract keyed
on `agentId` (the NFT's tokenId). The forward direction —
`getAgentWallet(agentId) → address` — is a constant-time storage read. The
reverse — `address → agentId` — would require maintaining a second mapping
(gas-expensive) or scanning every `Transfer` event (fragile under reorgs).
The spec's answer is to push the disambiguation to the client: *you tell us
which on-chain identity you're claiming.*

Practically:

1. The client must already know its own `agentId` (it minted it; the receipt
   carried the value back). The agent stashes this number alongside its key.
2. SASL round 1's payload is now **32 bytes** (left-padded uint256), not 20.
3. If a wallet is registered for *multiple* `agentId`s on the same chain,
   each one is authenticated as a separate identity, even though they share
   a key. That's by design.

### The trust diagram now

```
   client (alice's agent)                   irc server                       base RPC                  registry
   ────                                     ────                             ────                      ────
   |                                        |                                |                         |
   | AUTHENTICATE ERC8004                   |                                |                         |
   |───────────────────────────────────────►|                                |                         |
   |◄────────────  AUTHENTICATE +  ─────────|                                |                         |
   | AUTHENTICATE base64(uint256 agentId)   |                                |                         |
   |───────────────────────────────────────►|                                |                         |
   |                                        | eth_call getAgentWallet(id)    |                         |
   |                                        |───────────────────────────────►|                         |
   |                                        |                                | view ownerOf / _wallet  |
   |                                        |                                |────────────────────────►|
   |                                        |                                |◄──────────────────────  | 0x7099...79C8
   |                                        |◄───────────────────────────────|                         |
   |◄────  AUTHENTICATE base64(nonce) ──────|                                |                         |
   |                                        |                                |                         |
   | EIP-191 sign:                          |                                |                         |
   |   agent-irc-sasl-v1                    |                                |                         |
   |   chain=31337 server=ergo.test         |                                |                         |
   |   agentId=1 nonce=<hex>                |                                |                         |
   | AUTHENTICATE base64(sig)               |                                |                         |
   |───────────────────────────────────────►|                                |                         |
   |                                        | ecrecover == wallet from step1?|                         |
   |◄────────  903 RPL_SASLSUCCESS  ────────|                                |                         |
```

Every successful SASL attempt produces one read-only `eth_call`. The contract
is never written to during auth — writes happen out-of-band when an agent
registers itself.

### What `getAgentWallet` returns

```solidity
function getAgentWallet(uint256 agentId) external view returns (address) {
    address w = _agentWallet[agentId];
    return w == address(0) ? ownerOf(agentId) : w;
}
```

Two storage slots, one fallback: a designated signing wallet set via
`setAgentWallet` (useful for cold-owner / hot-signer custody splits), or the
ERC-721 owner. The SASL handler doesn't care which slot answered. If
`agentId` was never minted, ERC-721's `ownerOf` reverts; the fork's handler
catches that and translates it to `904 :SASL ERC8004: agentId not in registry`.

### The `accounts.erc8004` config block

`start-ergo.sh` injects this into the freshly-regenerated `ircd.yaml`:

```yaml
accounts:
    erc8004:
        rpc-url: "http://localhost:8545"
        registry-address: "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"
        chain-id: 31337
        cache-ttl: 0s
```

| Field              | What                                                                                    |
|--------------------|-----------------------------------------------------------------------------------------|
| `rpc-url`          | The endpoint the server `eth_call`s. Anvil for dev, Base mainnet for prod.              |
| `registry-address` | The deployed `AgentRegistry`. Written to `.registry-address` by `deploy.sh`.            |
| `chain-id`         | Baked into the SASL signed body. Defeats cross-chain replay.                            |
| `cache-ttl`        | How long a positive registry result is reused. `0s` = no cache, test-only.              |

Switching to Base mainnet is *one* config diff — no Go code changes:

```diff
-        rpc-url: "http://localhost:8545"
-        registry-address: "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"
-        chain-id: 31337
+        rpc-url: "https://mainnet.base.org"
+        registry-address: "0x8004A169FB4a3325136EB29fA0ceB6D2e539a432"
+        chain-id: 8453
         cache-ttl: 30s
```

`verify-mainnet.sh` runs exactly that flip against a funded test agent.

## What you'll build

The **fork** is already at `chapter-erc8004-canonical`; the gate logic lives
in `irc/agentirc/registry.go` (RPC client), `irc/agentirc/sasl.go`
(`ChallengeBody` with chain + server + agentId binding), `irc/handlers.go`
(`authERC8004Handler`), and `irc/config.go` (`ERC8004Config`).

In the **chapter directory**:

| File                                                            | Purpose                                                                                  |
|-----------------------------------------------------------------|------------------------------------------------------------------------------------------|
| [`contracts/AgentRegistry.sol`](./contracts/AgentRegistry.sol)  | Canonical ERC-8004 Identity Registry (same code as chapter 08a + Base mainnet).          |
| [`start-anvil.sh`](./start-anvil.sh)                            | Local EVM on `:8545`, deterministic accounts.                                            |
| [`deploy.sh`](./deploy.sh)                                      | Compile + deploy + register alice-bot + capture agentId from `Registered` event.         |
| [`start-ergo.sh`](./start-ergo.sh)                              | Pins fork to `chapter-erc8004-canonical`, injects `erc8004` block into `ircd.yaml`.      |
| [`start-ergo-base.sh`](./start-ergo-base.sh)                    | Same, but pointed at Base mainnet's canonical registry.                                  |
| [`verify/main.go`](./verify/main.go)                            | 3-case Go test: positive, wrong-signer, nonexistent agentId.                             |
| [`verify.sh`](./verify.sh)                                      | Local orchestration: anvil → deploy → ergo → verify → teardown.                          |
| [`verify-mainnet.sh`](./verify-mainnet.sh)                      | Same verify program against Base mainnet via the funded agent in `../.env`.              |

## Run it

```bash
./verify.sh
```

What happens:

```
=== 1. starting anvil ===
=== 2. deploying AgentRegistry + registering alice-bot ===
>> registry @ 0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512
>> alice-bot: agentId=1
>> sanity check getAgentWallet(1) = 0x70997970C51812dc3A010C7d01b50e0d17dc79C8
>> sanity check tokenURI(1)      = data:application/json,{"name":"alice-bot"}

=== 3. starting agent-irc-ergo with ERC-8004 gate ===
  agent-irc : ERC-8004 gate enabled : address : 0xe7f1... : rpc : http://localhost:8545
  agent-irc : mutation watcher started : interval : 30s
  listeners : now listening on :16674

=== 4. verify (3 SASL cases against the live canonical registry) ===
--- case 1: positive ---
  alice <- :ergo.test 903 * :Authentication successful
  ✓ accepted
--- case 2: negative (wrong key signing for claimed agentId) ---
  forger <- :ergo.test 904 * :SASL ERC8004: signer is not the agent's wallet
  ✓ rejected: wrong signer for claimed agentId
--- case 3: negative (nonexistent agentId) ---
  ghost <- :ergo.test 904 * :SASL ERC8004: agentId not in registry
  ✓ rejected: agentId not in registry
PASS: chapter 08b — canonical ERC-8004 gate enforced (agentId + getAgentWallet)
```

**Case 2 fails on the wallet match, not on signature validity.** The
signature ecrecovers cleanly to the forger's address; it just doesn't match
what `getAgentWallet` returned. This ordering — resolve registry first
(step 1), compare wallets second (step 3) — keeps an attacker without the
private key from distinguishing "valid agentId, I can't sign" from "valid
sig, but for a different agentId." Both look like 904. Defense in depth.

## Walkthrough

### `deploy.sh` — register pattern

`deploy.sh` calls `register(string)` with an **inlined data URI** so the
off-chain JSON lives on-chain too — no IPFS pin, no HTTPS host needed:

```bash
URI="data:application/json,{\"name\":\"alice-bot\"}"
cast send … "$REGISTRY_ADDR" "register(string)" "$URI"
```

Then captures the freshly-minted `agentId` from the
`Registered(uint256,string,address)` event:

```bash
REG_TOPIC0=0xca52e62c367d81bb2e328eb795f7c7ba24afb478408a26c0e201d155c449bc4a
AGENT_ID=$(cast receipt … --json | python3 -c "...print(int(log['topics'][1], 16))...")
echo "$AGENT_ID" > .alice-agentid
```

Pinning topic-0 means we don't shell out to `cast keccak` per run, and the
hash is identical against anvil and Base mainnet because both deploys share
the same event signature.

### The Go-side query

`irc/agentirc/registry.go` hand-rolls the ABI for `getAgentWallet`,
`tokenURI`, and `ownerOf`. We don't generate full bindings (`abigen`) —
three methods are easier to inline than a code-gen step is to teach.

### Fail-closed semantics

The handler's `904` messages disambiguate four failure modes:

```go
// step 1 — claim agentId, resolve expected wallet
agentID := common.BytesToHash(value)                       // 32-byte uint256
addr, err := reg.GetAgentWallet(ctx, agentID)
if err != nil  { return 904 "agentId not in registry" }    // revert or RPC fail
if addr == 0x0 { return 904 "agentId has no wallet" }
sendChallenge(nonce)

// step 3 — verify signature
recovered, err := agentirc.VerifyChallenge(nonce, value, chainID, server.name, agentID.Big())
if err != nil               { return 904 "signature verification failed" }
if recovered != expectedAddr { return 904 "signer is not the agent's wallet" }
```

Per-call timeout is 5s. Cache TTL is `0s` by default (correct-but-expensive);
production should set ~30–60s, then rely on chapter 10's mutation watcher
to invalidate stale entries.

### Why bind chain + server + agentId into the signed body

`ChallengeBody` produces:

```
agent-irc-sasl-v1
chain=31337
server=ergo.test
agentId=1
nonce=<hex>
```

Each line defeats one replay vector:

| Line          | Replay vector defeated                                              |
|---------------|---------------------------------------------------------------------|
| `chain=<id>`  | Sig for chain X (anvil) can't be reused on chain Y (Base).          |
| `server=`     | Sig for `ergo.test` can't be reused on `irc.production.example`.    |
| `agentId=<n>` | Sig for agentId 1 can't be reused as agentId 2 by a wallet that     |
|               | owns both (same key, two on-chain identities).                      |
| `nonce=`      | Sig can't be replayed within the same chain+server+agentId.         |

The `agentId` line is new in chapter 08b. It costs us nothing and forecloses
the one edge case the step-1 wallet pin doesn't already cover.

## Optional sidebar: verify against Base mainnet

`verify-mainnet.sh` runs cases 1 + 2 against the canonical registry. Case 3
is skipped on mainnet — picking a "guaranteed-unminted" tokenId could
collide with a future legitimate mint.

Prereqs: `../.env` with `AGENT_PRIVATE_KEY` + `AGENT_ADDRESS` + `AGENT_ID`
+ `ERC8004_REGISTRY` + `RPC_URL` + `CHAIN_ID`, plus a `tokenURI(AGENT_ID)`
that resolves over HTTPS to JSON with a valid `.name`.

```bash
./verify-mainnet.sh
```

Top to bottom:

1. **Preflight.** `cast call` the live registry: `getAgentWallet(AGENT_ID)`
   must match `AGENT_ADDRESS`; `tokenURI(AGENT_ID)` must resolve over HTTPS
   to JSON whose `.name` is non-empty (we check for `lin-test-bot`).
2. **Start the fork** pointed at Base mainnet via `start-ergo-base.sh`.
3. **Run the verify Go program** in `MODE=mainnet` (cases 1 + 2 only).

This is the cheapest "is the auth path real?" check; chapter 11's
`verify-base-mainnet.sh` runs the same shape at the CLI layer.

## Critical Thinking

### Fail-closed semantics on RPC failure

| Trigger                                  | Outcome                                                  |
|------------------------------------------|----------------------------------------------------------|
| `eth_call` times out (5s)                | 904 "agentId not in registry" — *false negative*         |
| `eth_call` returns a revert              | 904 "agentId not in registry" — true negative            |
| `getAgentWallet` returns `0x0...0`       | 904 "agentId has no wallet" — true negative              |
| `ecrecover` fails (malformed sig)        | 904 "signature verification failed"                      |
| `ecrecover` succeeds but wallet mismatch | 904 "signer is not the agent's wallet"                   |
| Everything matches                       | 903 "Authentication successful"                          |

The first row is the spicy one: a legitimate agent gets rejected because the
RPC was momentarily unreachable, with a misleading "not in registry" message.
This is fail-closed semantics by design — better to reject the occasional
legitimate auth than to let an unverified one through — but operationally
your network's uptime is now bounded by your RPC's uptime, and diagnostically
the operator can't easily distinguish "registry says no" from "we couldn't
reach the registry." We could emit a distinct 904 string for each path, but
that would also tell an attacker which path they hit — a small but real
information leak. We prefer the unified message.

### Cache TTL trade-off

`cache-ttl: 0s` is correct-but-expensive: every SASL attempt produces an RPC
call. A production-reasonable value is 30s–5m.

The trade: a freshly-rotated wallet (via `setAgentWallet`) will keep matching
the *old* wallet inside the cache window. Chapter 10's mutation watcher
solves this asymmetrically — it polls for changes on a 30s interval and
**invalidates** affected cache entries, so the worst-case stale-cache window
is bounded by `min(cache-ttl, watcher-interval)`.

### When the tokenURI JSON fetch fails

`getAgentWallet` alone gates the *auth decision*. The HTTP fetch of
`tokenURI` happens *after* the wallet matched and is used to derive the IRC
account name. If the JSON fetch fails (HTTP 404, timeout, malformed JSON,
`.name` empty or not IRC-valid), the handler still hits **904 "registry
resolve failed"** or **"agent JSON name not IRC-valid"** — even though the
on-chain half of the resolve succeeded.

This is the trickiest part of the ERC-8004 model: an entry can be
*on-chain-valid* (owned, minted, wallet correct) but *off-chain-invalid*
(URI 404s, JSON missing `.name`). Both halves must succeed before the agent
can speak. Chapter 09 picks up the name-derivation rules; chapter 10's
watcher catches an off-chain JSON disappearing the same way it catches
on-chain mutations.

### Vs. the legacy "registry holds the name" assumption

Earlier drafts of chapter 08 had a `mapping(address ⇒ string name)` on the
contract — the registry was the canonical source of *both* the identity and
the human-readable name. Canonical ERC-8004 splits these:

| Concern                          | Legacy (08-pre-canonical)         | Canonical (08b)                              |
|----------------------------------|------------------------------------|----------------------------------------------|
| Who/what an agent *is*           | `mapping(addr → name)`             | `agentId` (NFT) + `getAgentWallet`           |
| Human-readable name              | `nameOf(addr)` storage read        | `.name` in off-chain JSON at `tokenURI`      |
| Name uniqueness                  | Enforced in the contract           | Not enforced on-chain (!)                    |
| RPC calls per SASL               | 1 (nameOf)                         | 1 (getAgentWallet) + 1 HTTP (chapter 09)     |
| Reverse lookup (addr→identity)   | Built-in                           | Absent — client must declare `agentId`       |
| Censorship surface               | On-chain only                      | On-chain + URI host (HTTPS / IPFS)           |

The canonical split is more flexible (the JSON can carry arbitrary metadata
without re-deploying the registry) and more expensive (HTTP fetch on cold
cache miss). It also moves the censorship surface partly off-chain: a
registry entry is hard to take down, but the `agent.json` it points to is
just an HTTPS URL or an IPFS CID — either can disappear, leaving the
on-chain entry pointing at nothing. Production deployments mitigate by
pinning to IPFS via multiple gateways or self-hosting the JSON on
infrastructure they control.

## Files

```
08b-gating-on-the-registry/
├── contracts/AgentRegistry.sol     # canonical ERC-8004 Identity Registry
├── foundry.toml
├── lib/openzeppelin-contracts/     # ERC721 + ERC721URIStorage + EIP712 + ECDSA
├── start-anvil.sh                  # local devnet
├── deploy.sh                       # forge create + cast send + capture agentId
├── start-ergo.sh                   # build chapter-erc8004-canonical + inject erc8004 block
├── start-ergo-base.sh              # same, but pointed at Base mainnet
├── go.mod / go.sum
├── verify/main.go                  # 3 SASL cases against the live registry
├── verify.sh                       # anvil → deploy → ergo → verify
├── verify-mainnet.sh               # the same, against Base mainnet via ../.env
└── README.md

# Already in the fork (~/workspace/agent-irc-ergo @ chapter-erc8004-canonical):
irc/agentirc/registry.go            # canonical registry client (eth_call + HTTP)
irc/agentirc/sasl.go                # ChallengeBody: chain + server + agentId binding
irc/handlers.go                     # authERC8004Handler: agentId-first lookup + ecrec
irc/config.go                       # ERC8004Config under accounts.erc8004
```

## Next

[Chapter 09 — Identity binding (name = nick)](../09-identity-binding) — we
replace `AccountNameForAddress` with the JSON-derived `.name`. Successful
SASL ERC8004 → IRC nick is the agent's off-chain JSON `.name`. Charset
normalization, casemapping, NICK-change rejection. After this chapter,
ERC-8004 names become first-class on the IRC wire.
