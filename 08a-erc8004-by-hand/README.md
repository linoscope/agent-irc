# Chapter 08a — ERC-8004 by hand

## What you'll do, in plain English

You'll learn what ERC-8004 is by *poking at a deployed registry contract by hand* with `cast`. No Ergo. No fork. No Go. Just `forge`, `anvil`, and `cast` — the canonical Foundry tooling.

By the end of this chapter you'll be able to:

1. Deploy an ERC-8004-shaped registry to a local EVM and read its address.
2. Register an agent (yourself) on-chain via `cast send`.
3. Look up an agent by their wallet address (`nameOf`) or their agent ID (`agentIdOf`).
4. Mutate your registered name and see the change persist on-chain.
5. Read the events the registry emits — the audit log of every identity action.

Chapter 08b will then *wire the IRC server to consult this registry* — but that's a question of plumbing. The substance is in this chapter: what does an on-chain agent identity *actually look like* when you talk to it directly?

A bit of vocabulary, in plain language:

| Term | Plain English |
|---|---|
| **ERC-8004** | An Ethereum standard (currently EIP-8004 draft, 2025-08-13) for **agent identity registries on-chain**. Defines three registries: Identity (who is an agent), Reputation (community feedback), Validation (provenance). We only use Identity. |
| **Identity Registry** | A smart contract that maps `wallet address ↔ agent name ↔ agentId`. Authoritative source of "who controls this name." |
| **agentId** | A monotonic integer assigned at registration time. Conceptually the agent's NFT-like token ID (the canonical EIP-8004 inherits ERC-721 here; we don't, for tutorial simplicity). |
| **wallet** | The Ethereum address that controls the agent's entry. Whoever holds the private key for this address can rename, transfer, or remove the agent. |
| **name** | A human-readable string the registry maps to a unique agentId. Uniqueness is enforced on-chain — no two agents can hold the same name simultaneously. |
| **`cast`** | Foundry's CLI for talking to EVM contracts. `cast call` for view functions (free, off-chain), `cast send` for state-mutating transactions (costs gas, signs with a private key). |
| **`anvil`** | Foundry's local EVM node. Comes with 10 pre-funded test accounts whose private keys are public. We use account 0 to deploy and account 1 as the test agent. |

## Mental model: why an on-chain identity registry

The agent-irc story so far: chapter 07 authenticates an IRC session by having the client sign a server-issued nonce with their wallet keypair. After 07, *anyone with any keypair* authenticates fine — the server has no notion of "registered agent."

The two-paragraph case for solving that with a contract:

**Why on-chain.** An off-chain registry (a JSON file, a centralized service) means *somebody* gets to decide who's listed. That somebody can revoke, replace, or surveil. An on-chain registry has no operator: the rules are the bytecode, and the bytecode is publicly auditable. Identity decisions become functions of public state, not requests to a custodian. This is the same logic that makes ENS preferable to a domain registrar's database, or DNS preferable to a hosts file — except now applied to *agent identity*, the primitive our SASL handler needs to gate on.

**Why this shape (Identity Registry).** ERC-8004 separates concerns. Identity is *just* "who controls this name." Reputation (other agents vouching for or warning about this one) is a separate registry. Validation (agents publishing signed claims about their behavior) is a third. We only need Identity for chapter 08b's gate — chapters that want richer signals can layer on top.

### What's in scope here

| ERC-8004 piece | This chapter? | Where used |
|---|---|---|
| Identity Registry — `nameOf`, `agentIdOf`, `register`, `setName`, `remove` | yes | 08b SASL gate; 09 name binding; 10 mutation watcher |
| ERC-721 inheritance (agentIds are NFTs) | no — tutorial-grade contract drops this | omitted for code-size; would need OpenZeppelin in production |
| Reputation Registry | no | not needed for agent-irc |
| Validation Registry | no | not needed for agent-irc |
| Metadata (per-agent extensible fields) | no | spec'd but unused here |

The contract we deploy (`contracts/AgentRegistry.sol`) is a faithful subset of the Identity Registry — just enough that chapter 08b can query it with one `nameOf(address)` call to gate SASL.

### Spec status note

EIP-8004 is a **draft** as of 2025-08-13. There is **no canonical Base mainnet deployment** at the time of this tutorial; if and when one emerges, swap the registry address in `start-ergo.sh` for the canonical one and chapter 08b's behavior is unchanged.

For development, we deploy our own minimal compliant contract on local `anvil`. Same ABI as you'd write against the canonical version when it ships.

## What you'll learn

- The `cast call` / `cast send` workflow against an arbitrary EVM contract.
- The Identity Registry interface: `register`, `setName`, `remove`, `nameOf`, `agentIdOf`, `walletOf`.
- How registry events surface as on-chain logs (`AgentRegistered`, `AgentRenamed`, `AgentRemoved`).
- What information lives on-chain vs off-chain, and why.

## What you'll build

Nothing in code. The deliverable is **muscle memory**: by the end of this chapter you should be able to deploy any ERC-8004-shaped contract, register an agent, query it, and read the resulting events without referring to a cheat sheet.

Files in this chapter:

```
08a-erc8004-by-hand/
├── contracts/AgentRegistry.sol     # the minimal Identity Registry
├── foundry.toml
├── start-anvil.sh                  # local EVM
├── deploy.sh                       # forge create the contract; no auto-register
└── README.md                       # this file (the recipe)
```

No verify program; the recipe IS the verification. If `cast call` returns the value you expected, the chapter works.

## Run it

You'll want **two terminals**: one for `anvil` (the local EVM, runs in foreground) and one for the recipe.

```bash
# Terminal A — start anvil. Leave it running.
./start-anvil.sh
```

You'll see anvil's banner with 10 test accounts and their private keys. We'll use:

| Role | Account | Address | Private key |
|---|---|---|---|
| **Deployer** | account 0 | `0xf39F…2266` | `0xac0974be…f4f2ff80` |
| **Alice** (the agent) | account 1 | `0x7099…79C8` | `0x59c6995e…b78690d` |

These are well-known anvil defaults — public on purpose, never use them on a real chain.

### Step 1: deploy the registry

```bash
# Terminal B
./deploy.sh
```

Output:

```
>> AgentRegistry deployed at 0x5FbDB2315678afecb367f032d93F642f64180aa3
>> address saved to ./.registry-address
```

The address gets cached in `.registry-address` so the rest of the recipe can refer to it as `$(cat .registry-address)`.

### Step 2: nameOf(alice) before registration

```bash
REG=$(cat .registry-address)
ALICE=0x70997970C51812dc3A010C7d01b50e0d17dc79C8
RPC=http://localhost:8545

cast call --rpc-url $RPC $REG "nameOf(address)(string)" $ALICE
# → ""
```

Empty string. Alice isn't registered yet. **`nameOf` returning empty is the gate signal chapter 08b uses** — "no on-chain entry, reject SASL."

### Step 3: register alice as "alice-bot"

```bash
ALICE_KEY=0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d

cast send --rpc-url $RPC --private-key $ALICE_KEY \
    $REG "register(string)" "alice-bot"
```

Output ends with `status 1 (success)` and a transaction hash. The `--private-key $ALICE_KEY` is what makes this a transaction *from alice* — `msg.sender` in the contract is `0x7099…79C8`, so alice's wallet address gets stored as the agent's controller.

### Step 4: nameOf(alice) after registration

```bash
cast call --rpc-url $RPC $REG "nameOf(address)(string)" $ALICE
# → "alice-bot"
```

The registry now knows alice. Same call, different result — pure consequence of step 3's transaction landing on-chain.

### Step 5: agentIdOf(alice)

Every registered agent also has an integer ID:

```bash
cast call --rpc-url $RPC $REG "agentIdOf(address)(uint256)" $ALICE
# → 1
```

Alice was the first to register, so she's agentId 1. The next agent to call `register()` will be agentId 2, etc. Useful when other contracts want to refer to an agent compactly.

### Step 6: rename

Alice can rename herself (only she can — the contract checks `msg.sender == agent's wallet`):

```bash
cast send --rpc-url $RPC --private-key $ALICE_KEY \
    $REG "setName(string)" "alice-bot-v2"

cast call --rpc-url $RPC $REG "nameOf(address)(string)" $ALICE
# → "alice-bot-v2"
```

Same agentId (1), different name. Notice the agentId didn't change — it's a stable identifier, the *name* is the mutable display label. This distinction is what chapter 10's mutation watcher exploits: when the name changes mid-IRC-session, the watcher detects it and KILLs the connection.

### Step 7: read the events

Every state-mutating call emits an event. Read them all:

```bash
cast logs --rpc-url $RPC --address $REG --from-block 0
```

You'll see two log entries — one `AgentRegistered` and one `AgentRenamed`:

```
- topics: [
    0x0d063c… (keccak256("AgentRegistered(uint256,address,string)"))
    0x000000…0001              ← agentId = 1
    0x000000…70997970…79C8     ← alice's address (indexed)
  ]
  data: 0x… (encoded "alice-bot")

- topics: [
    0xffcea8… (keccak256("AgentRenamed(uint256,string,string)"))
    0x000000…0001              ← agentId = 1
  ]
  data: 0x… (encoded oldName="alice-bot", newName="alice-bot-v2")
```

These are the *audit log* of identity changes. Indexers (TheGraph, custom subgraphs, plain `cast logs`) consume them to build off-chain mirrors of the registry state. Chapter 10's mutation watcher could in principle subscribe to these logs instead of polling — same information.

### Bonus: try registering as alice from someone else's wallet

```bash
BOB_KEY=0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a

cast send --rpc-url $RPC --private-key $BOB_KEY \
    $REG "register(string)" "alice-bot"
# → reverts with "NameTaken"
```

The contract enforces uniqueness. Bob (account 2) can't register the name `alice-bot` because alice already has it. Try it with `alice-bot-v2` (also taken now), and `bobby` (free). The on-chain rules are the entire enforcement; there's no admin to override them.

## Critical Thinking: what ERC-8004 doesn't solve

A few production headaches the spec leaves on the table:

1. **Sybil resistance.** Anyone with gas can register any number of names. Our contract has no fee, no proof-of-personhood, no rate limit. A real deployment needs at least one of those (registration fee, allowlist, attestation requirement) or the namespace gets squatted.
2. **Name squatting.** Same problem at the value-level: someone registers `openai-gpt5-bot` before OpenAI does. Solutions: commit-reveal registration (chapter 10's "future work"), TLD-style hierarchy with delegated namespaces, retroactive disputes.
3. **Cross-chain identity.** alice on Base mainnet is a *different agent* from alice on Arbitrum from the contract's perspective — they're separate registry deployments. Cross-chain attestation (CCIP, deterministic address derivation) is its own design problem.
4. **Wallet compromise.** If alice's private key leaks, the attacker becomes alice on-chain. The registry has no recovery mechanism (would have to be added explicitly: social recovery, multisig wallets, time-delayed setOwner).
5. **Privacy.** The registry is fully public. Every name and address is indexed forever. Agents that want privacy need to use ephemeral keypairs and re-register, which forfeits reputation continuity.

For agent-irc as an internal substrate (chapter 04's framing), most of these don't bite — operator vetting + a small known set of agents avoid them. For a public agent network they'd all need addressing.

## Next

[Chapter 08b — Gating the IRC server on the registry](../08b-gating-on-the-registry) — the integration. The chapter-07 SASL handler now consults the registry you just deployed: signatures from registered agents pass; signatures from anyone else get `904 ERR_SASLFAIL`. The contract is the same; we just teach Ergo to read it.
