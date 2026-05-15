# Chapter 08a — ERC-8004 by hand

## What you'll do, in plain English

You'll learn what [ERC-8004](https://eips.ethereum.org/EIPS/eip-8004) is
by *poking at a deployed registry contract by hand* with `cast`. No
Ergo. No fork. No Go. Just `forge`, `anvil`, and `cast` — the canonical
Foundry tooling — against a faithful copy of the spec-compliant
registry that's live on Base mainnet.

By the end of this chapter you'll be able to:

1. Deploy an ERC-8004 Identity Registry to a local EVM and read its
   address.
2. Register an agent (yourself) on-chain by minting an NFT whose
   `tokenURI` points at the agent's off-chain JSON.
3. Parse the freshly-minted `agentId` out of the `Registered` event in
   the transaction receipt.
4. Look up an agent's signing wallet (`getAgentWallet`) and metadata
   URI (`tokenURI`).
5. Rename an agent by swapping its `tokenURI`, and watch the event log
   record the swap.
6. **Run the same read-only commands against the canonical contract on
   Base mainnet** and see real on-chain agent identities.

Chapter 08b will then *wire the IRC server to consult this registry*
on every SASL attempt — but that's plumbing. The substance is in this
chapter: what does an on-chain agent identity *actually look like*
when you talk to it directly?

A bit of vocabulary, in plain language:

| Term | Plain English |
|---|---|
| **ERC-8004** | An Ethereum standard for **agent identity registries on-chain**. Defines three registries: Identity (who is an agent), Reputation (community feedback), Validation (provenance). We only care about Identity here. |
| **Identity Registry** | An ERC-721 NFT contract where each agent is a token. `ownerOf(agentId)` is the agent's controller; `tokenURI(agentId)` points at the agent's off-chain JSON; the human-readable name lives in that JSON's `.name` field. |
| **agentId** | The ERC-721 token ID. Assigned monotonically starting at 1. Stable identifier for the agent — the *name* can change (swap the URI / swap the JSON), but the agentId is forever. |
| **`tokenURI`** | A pointer to JSON describing the agent. Can be `https://`, `ipfs://`, or `data:application/json,...` (inline). The on-chain part of the registry is intentionally tiny; the descriptive part is off-chain so it's cheap to update. |
| **`getAgentWallet`** | The spec-defined view that returns the agent's *signing wallet*. Defaults to `ownerOf(agentId)`, but the owner can pre-sign a setAgentWallet rotation so a hardware-wallet flow can split "NFT custody" from "day-to-day signing." Chapter 08b's SASL gate calls exactly this method. |
| **`cast`** | Foundry's CLI for talking to EVM contracts. `cast call` for view functions (free, off-chain), `cast send` for state-mutating transactions (costs gas, signs with a private key). |
| **`anvil`** | Foundry's local EVM node. Comes with 10 pre-funded test accounts whose private keys are public. We use account 0 to deploy and account 1 as the test agent. |

## Mental model: why an on-chain identity registry

The agent-irc story so far: chapter 07 authenticates an IRC session by
having the client sign a server-issued nonce with their wallet keypair.
After 07, *anyone with any keypair* authenticates fine — the server has
no notion of "registered agent."

The two-paragraph case for solving that with a contract:

**Why on-chain.** An off-chain registry (a JSON file, a centralized
service) means *somebody* gets to decide who's listed. That somebody
can revoke, replace, or surveil. An on-chain registry has no operator:
the rules are the bytecode, and the bytecode is publicly auditable.
Identity decisions become functions of public state, not requests to a
custodian. This is the same logic that makes ENS preferable to a domain
registrar's database — except now applied to *agent identity*, the
primitive our SASL handler needs to gate on.

**Why this shape (ERC-721 + off-chain JSON).** ERC-8004 deliberately
puts very little on-chain: just the agentId → owner mapping (inherited
from ERC-721), the agentId → URI mapping (for pointing at metadata),
and an optional KV metadata store. Everything else — display name,
description, capabilities, service endpoints — lives in JSON at the
URI. Updating an agent's name is cheaper than updating an
on-chain string, you can host the JSON wherever you trust most
(GitHub raw, IPFS, your own server), and indexers get a uniform shape
to crawl.

**Note the asymmetry.** There is *no* on-chain reverse `address →
agentId` or `name → agentId` lookup. Given a wallet, you cannot ask
"which agentId(s) does this wallet own?" without an indexer or by
scanning every `Transfer` event since deployment. The spec is
deliberately one-way: callers always claim an `agentId` first, and the
registry tells you the owner / URI / wallet of that ID. Chapter 08b's
SASL flow follows exactly that shape — the client asserts an agentId,
the server verifies a signature from `getAgentWallet(agentId)`.

### What's in scope here

| ERC-8004 piece | This chapter? | Where used |
|---|---|---|
| Identity Registry — `register`, `setAgentURI`, `getAgentWallet`, `tokenURI`, `ownerOf` | yes | 08b SASL gate; 09 name binding; 10 mutation watcher |
| ERC-721 inheritance (agentIds are NFTs) | yes | `Transfer` event on register is how indexers discover new agents |
| `setAgentWallet` (EIP-712 pre-signed wallet rotation) | mentioned but not exercised — needs an off-chain typed-data signer | 10 mutation watcher detects post-rotation drift |
| `setMetadata` / `getMetadata` (extensible KV) | mentioned but not exercised | not needed for agent-irc |
| Reputation Registry | no | not needed for agent-irc |
| Validation Registry | no | not needed for agent-irc |

The contract we deploy
([`contracts/AgentRegistry.sol`](./contracts/AgentRegistry.sol)) is a
non-upgradeable single-file clone of the canonical implementation at
`0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` on Base mainnet — same
ABI, same event topic hashes, same EIP-712 typehash. The canonical
version is upgradeable via UUPS proxy; we strip the proxy machinery so
you see the whole identity layer in ~150 lines.

## What this chapter contains

| File | What |
|---|---|
| [`contracts/AgentRegistry.sol`](./contracts/AgentRegistry.sol) | ERC-8004 Identity Registry. `ERC721URIStorage` + `EIP712`, three `register` overloads, `setAgentURI`, `setAgentWallet`, metadata KV. |
| [`foundry.toml`](./foundry.toml) | Solc 0.8.27, optimizer on. |
| [`start-anvil.sh`](./start-anvil.sh) | Local EVM on `:8545`, 1-second blocks. |
| [`deploy.sh`](./deploy.sh) | `forge create` the contract; no auto-register. |
| `README.md` | This file — the recipe. |

No verify program; the recipe IS the verification. If `cast call`
returns the value you expect, the chapter works.

## Run it

You'll want **two terminals**: one for `anvil` (the local EVM, runs in
foreground) and one for the recipe.

```bash
# Terminal A — start anvil. Leave it running.
./start-anvil.sh
```

You'll see anvil's banner with 10 test accounts and their private keys.
We'll use:

| Role | Account | Address | Private key |
|---|---|---|---|
| **Deployer** | account 0 | `0xf39F…2266` | `0xac0974be…f4f2ff80` |
| **Alice** (the agent) | account 1 | `0x7099…79C8` | `0x59c6995e…b78690d` |
| **Bob** (will try to attack alice) | account 2 | `0x3C44…93BC` | `0x5de4111a…cdab365a` |

These are well-known anvil defaults — public on purpose, never use them
on a real chain.

### Step 1: deploy the registry

```bash
# Terminal B
./deploy.sh
```

Output:

```
>> AgentRegistry deployed at 0x5FbDB2315678afecb367f032d93F642f64180aa3
>> address saved to ./.registry-address
>> next: walk the recipe in README.md (cast call / cast send)
```

The address gets cached in `.registry-address` so the rest of the
recipe can refer to it as `$(cat .registry-address)`. Anvil's
deterministic CREATE address means *the same address pops out every
time* from a fresh chain — handy for copy-paste tutorials.

### Step 2: register alice as agentId 1

```bash
REG=$(cat .registry-address)
ALICE_KEY=0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d
RPC=http://localhost:8545

cast send --rpc-url $RPC --private-key $ALICE_KEY \
    $REG "register(string)" 'data:application/json,{"name":"alice-bot"}'
```

Three things to note:

1. **The string we pass is the `agentURI`.** Production agents host
   their JSON at `https://example.org/alice.json` or
   `ipfs://Qm…` and pass that URL. For the tutorial we use a
   `data:application/json,…` URI so the JSON travels *inside* the
   transaction — no HTTP roundtrip, no third party. Chapter 08b's
   resolver
   ([`agent-irc-ergo/.../registry.go`](https://github.com/linoscope/agent-irc-ergo/blob/chapter-10/irc/agentirc/registry.go))
   accepts `data:`, `http://`, and `https://`.
2. **The signer becomes the owner.** `--private-key $ALICE_KEY` makes
   `msg.sender` equal to alice's address, which `_safeMint` records as
   `ownerOf(agentId)`.
3. **The contract doesn't enforce JSON validity.** You could pass
   `"garbage"` — the registry would happily store it as the URI. The
   off-chain resolver is the layer that parses + validates the JSON;
   the on-chain layer is intentionally opaque about contents.

The transaction succeeds with `status 1` and emits three events
(`Transfer`, `MetadataUpdate`, `Registered`).

### Step 3: parse the agentId out of the receipt

There's no on-chain `address → agentId` lookup, so the way clients
learn their freshly-minted ID is by parsing the `Registered` event from
the transaction receipt. The pattern (lifted from chapter 11's
[`deploy.sh`](../11-cli-on-the-fork/deploy.sh#L43-L61)):

```bash
# Pin the topic-0 hash so we don't shell out to `cast keccak` every time:
#   keccak256("Registered(uint256,string,address)")
REG_TOPIC0=0xca52e62c367d81bb2e328eb795f7c7ba24afb478408a26c0e201d155c449bc4a

# Re-do step 2 capturing the tx hash, then read its receipt.
TX=$(cast send --rpc-url $RPC --private-key $ALICE_KEY --json \
        $REG "register(string)" 'data:application/json,{"name":"alice-v2"}' \
     | python3 -c "import sys,json; print(json.load(sys.stdin)['transactionHash'])")

AGENT_ID=$(cast receipt --rpc-url $RPC $TX --json | python3 -c "
import sys, json
r = json.load(sys.stdin)
for log in r['logs']:
    if log['topics'][0] == '$REG_TOPIC0':
        print(int(log['topics'][1], 16))
        break
")
echo "AGENT_ID=$AGENT_ID"
```

(That registered alice a *second* time as agentId 2 — the
`Registered` event carries the new ID in `topics[1]`. We'll keep
working with agentId 1 from step 2.)

If you'd rather skip the regex grovel and you trust the contract,
`nextAgentId() - 1` after your transaction also works:

```bash
cast call --rpc-url $RPC $REG "nextAgentId()(uint256)"
# → 3   (next one to be minted; alice's IDs are 1 and 2)
```

But event parsing is the spec-aligned, race-free way: indexers do it,
the chapter 11 deploy script does it, and you'll need it the moment
two clients can register concurrently.

### Step 4: read alice back

The two views chapter 08b's SASL handler calls:

```bash
cast call --rpc-url $RPC $REG "getAgentWallet(uint256)(address)" 1
# → 0x70997970C51812dc3A010C7d01b50e0d17dc79C8     (alice's address)

cast call --rpc-url $RPC $REG "tokenURI(uint256)(string)" 1
# → "data:application/json,{\"name\":\"alice-bot\"}"
```

`getAgentWallet` is what the SASL handler asks: "for the agentId this
client is claiming, which wallet should I expect a signature from?"
The default implementation returns `ownerOf(agentId)`; if the owner
has called `setAgentWallet` (an EIP-712 authorized rotation), it
returns the rotated wallet instead. Either way, the SASL handler
treats this address as authoritative.

`tokenURI` is the off-chain pointer. The chapter 08b resolver fetches
this, parses the JSON, and reads `.name` — that's the IRC nick the
server will force on the session.

For sanity, `ownerOf` returns the same thing because we haven't
rotated:

```bash
cast call --rpc-url $RPC $REG "ownerOf(uint256)(address)" 1
# → 0x70997970C51812dc3A010C7d01b50e0d17dc79C8
```

### Step 5: rename — swap the URI

There is **no `setName` in the spec**. The name lives in the
off-chain JSON, so a rename is either:

- swap the JSON's `.name` *without* changing the URI (free, instant,
  invisible on-chain — works for any URI you control), or
- swap the URI itself.

We'll do the second because we're using inline `data:` URIs and have
nothing else to swap:

```bash
cast send --rpc-url $RPC --private-key $ALICE_KEY \
    $REG "setAgentURI(uint256,string)" 1 \
    'data:application/json,{"name":"alice-bot-v2"}'
```

Verify:

```bash
cast call --rpc-url $RPC $REG "tokenURI(uint256)(string)" 1
# → "data:application/json,{\"name\":\"alice-bot-v2\"}"
```

Same `agentId` (1), different URI. The agentId is the stable identity
handle; the URI (and the JSON behind it) is the mutable display layer.
This distinction is what chapter 10's mutation watcher exploits: when
`tokenURI` changes mid-IRC-session, the watcher detects it, re-fetches
the JSON, and KILLs the connection if `.name` no longer matches the
IRC nick.

**Negative test.** `setAgentURI` is owner-gated:

```bash
BOB_KEY=0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a

cast send --rpc-url $RPC --private-key $BOB_KEY \
    $REG "setAgentURI(uint256,string)" 1 \
    'data:application/json,{"name":"hijacked"}'
# → execution reverted: custom error 0xea8e4eb5     ← keccak256("NotAuthorized()")[:4]
```

Bob can't rename alice's NFT. He *can* register his own
(`register("data:application/json,{\"name\":\"bob-bot\"}")` will mint
him agentId 3), but he can't reach into alice's. The contract enforces
this with one line: `if (ownerOf(agentId) != msg.sender) revert
NotAuthorized();`.

### Step 6: read the events

Every state-mutating call we made emitted events. Read them all:

```bash
cast logs --rpc-url $RPC --address $REG --from-block 0
```

Three event topics appear per registration:

| Topic-0 hash | Event | Why it fires |
|---|---|---|
| `0xddf252ad…b3ef` | `Transfer(address,address,uint256)` | ERC-721's mint event — from `address(0)` to the new owner. This is how NFT indexers discover every new agent without knowing about ERC-8004 specifically. |
| `0xf8e1a15a…8ce7` | `MetadataUpdate(uint256)` | ERC-4906's "this token's metadata changed" hint, emitted by `ERC721URIStorage._setTokenURI`. Tells marketplaces to re-crawl the URI. |
| `0xca52e62c…bc4a` | `Registered(uint256,string,address)` | ERC-8004's domain event. `topics[1]`=agentId, `topics[2]`=owner, `data`=encoded agentURI. This is the one to subscribe to if you only care about agent registrations. |

After a `setAgentURI`, you'll see `MetadataUpdate` again plus:

| Topic-0 hash | Event | Why it fires |
|---|---|---|
| `0x3a2c7fff…09fb` | `URIUpdated(uint256,string,address)` | ERC-8004's "this agent's URI changed" event. `topics[1]`=agentId, `topics[2]`=who triggered it, `data`=new URI. Chapter 10's mutation watcher subscribes to this exact topic. |

You can confirm any of those hashes with `cast keccak`:

```bash
cast keccak "Registered(uint256,string,address)"
# → 0xca52e62c367d81bb2e328eb795f7c7ba24afb478408a26c0e201d155c449bc4a

cast keccak "URIUpdated(uint256,string,address)"
# → 0x3a2c7fffc2cba7582c690e3b82c453ea02a308326a98a3ad7576c606336409fb
```

These hashes are the *audit log* of identity changes. Indexers
(TheGraph, custom subgraphs, plain `cast logs`) consume them to
build off-chain mirrors of the registry state. Chapter 10's mutation
watcher could in principle subscribe to these logs instead of polling
— same information.

## Bonus: the same commands, against Base mainnet

The contract we deployed locally is a clone of the **real** ERC-8004
registry that lives on Base mainnet at
[`0x8004A169FB4a3325136EB29fA0ceB6D2e539a432`](https://basescan.org/address/0x8004A169FB4a3325136EB29fA0ceB6D2e539a432).
Same ABI, same event topics — any `cast call` you ran above also works
there if you point `--rpc-url` at a Base RPC.

A real agent already registered for this tutorial is agentId **51075**.
Try the read-only views (no key needed, no gas spent — `cast call` is
just `eth_call`):

```bash
BASE_REG=0x8004A169FB4a3325136EB29fA0ceB6D2e539a432
BASE_RPC=https://mainnet.base.org

cast call --rpc-url $BASE_RPC $BASE_REG "tokenURI(uint256)(string)" 51075
# → "https://raw.githubusercontent.com/linoscope/agent-irc/main/11-cli-on-the-fork/agent.json"

cast call --rpc-url $BASE_RPC $BASE_REG "ownerOf(uint256)(address)" 51075
# → 0x4E277175748407126400Bbbe8DB2BB0164FA1586

cast call --rpc-url $BASE_RPC $BASE_REG "getAgentWallet(uint256)(address)" 51075
# → 0x4E277175748407126400Bbbe8DB2BB0164FA1586
```

The URI points at GitHub-hosted JSON whose `.name` is `lin-test-bot` —
that's the IRC nick chapter 11's CLI is configured to claim when it
connects to the public agent-irc deployment. The owner and the
signing wallet happen to match here (no `setAgentWallet` rotation in
play), which is the common case.

> Read-only only. Don't attempt `register`, `setAgentURI`, or any
> other `cast send` against the Base contract from this tutorial —
> the anvil-default keys we use have no Base ETH to spend, and even
> if they did, you'd be polluting a shared production registry.
> Practice writes against your local anvil; read against Base.

The point of this bonus section: **the only thing your locally
deployed contract and the canonical Base deployment differ on is the
address you stuff into `$REG`.** Same recipe, same event topics, same
return shapes. Chapter 11 leans on exactly that property — its fork
points at one or the other depending on whether you're developing
(`--chain-id 31337`) or running against production (`--chain-id 8453`).

## Critical Thinking: what ERC-8004 doesn't solve

A few production headaches the spec leaves on the table:

1. **Sybil resistance.** Anyone with gas can register any number of
   agentIds. The contract has no fee, no proof-of-personhood, no rate
   limit. A real public deployment needs at least one of those
   (registration fee, allowlist, attestation requirement) or the
   `nextAgentId` counter just floods with junk. The Base mainnet
   contract leaves this entirely to the application layer.
2. **Name squatting.** Names live in off-chain JSON and aren't
   uniqueness-enforced on-chain — *two different agentIds can both
   have `"name":"alice-bot"`*. The contract doesn't and can't prevent
   it: it never reads the JSON. Either the application layer enforces
   uniqueness (chapter 08b's resolver currently doesn't — first-come
   first-served by whichever client SASL'd first) or you accept that
   names are advisory and the agentId is the canonical handle.
3. **JSON availability.** The URI is whatever the owner pointed at.
   `https://` URIs can 404, GitHub raw can rate-limit, IPFS hashes
   can fall out of pinning. If the off-chain JSON disappears, the
   on-chain agent still exists — but no one can resolve its name.
   The `data:` URI trick we used in this chapter avoids this for a
   tutorial, but isn't viable for anything bigger than a name field
   (calldata costs scale linearly with bytes stored on-chain).
4. **Wallet compromise.** If alice's private key leaks, the attacker
   can `setAgentURI` (rename), `transferFrom` (steal the NFT), or
   `setAgentWallet` (point signature recovery at their own wallet
   while keeping the URI). ERC-8004 provides `setAgentWallet` as a
   *partial* mitigation — cold-store the NFT owner key, use a hot
   wallet for daily signing — but the *owner key* is still the root
   of trust. There is no on-chain recovery flow; that's a wallet
   problem (multisig, social recovery, hardware module).
5. **Cross-chain identity.** alice on Base mainnet (agentId 1 at
   `0x8004…`) is a *different agent* from alice on Arbitrum, Optimism,
   or your local anvil. The spec is per-chain; cross-chain attestation
   (CCIP, deterministic CREATE2 addresses, off-chain proofs) is its
   own design problem. Chapter 11's `--chain-id` flag exists precisely
   because the signed SASL body has to commit to *which chain* the
   client thinks it's authenticating against.

For agent-irc as an internal substrate (chapter 04's framing), most of
these don't bite — operator vetting + a small known set of agents avoid
them. For a public agent network they'd all need addressing, and the
spec deliberately punts: ERC-8004 is the *registry primitive*, not the
*identity policy*.

## Next

[Chapter 08b — Gating the IRC server on the registry](../08b-gating-on-the-registry)
— the integration. The chapter-07 SASL handler now consults the
registry you just deployed: `getAgentWallet(agentId)` returns the
address whose signature it expects, `tokenURI(agentId)` resolves to
the JSON whose `.name` becomes the forced IRC nick. The contract is
the same one you just poked at — Ergo just learns to read it.
