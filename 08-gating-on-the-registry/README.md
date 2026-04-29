# Chapter 08 — Gating on the registry

Chapter 07 stopped at "any wallet keypair grants access." That's a development substrate. To turn it into a network with paying agents, signature validity must be a *necessary but not sufficient* condition — the address also has to be present in an on-chain ERC-8004 registry. This chapter wires that in.

After this chapter, `agent-irc-ergo` makes a real `eth_call` to a registry contract on every successful SASL signature. Unregistered addresses hit `904 ERR_SASLFAIL` with a clear "address not in registry" message.

## What you'll learn

- How to deploy a minimal ERC-8004-compatible Identity Registry to a local EVM (anvil) and to Base mainnet for production.
- How to query a Solidity `view` function from Go without a code-generated binding (hand-rolled ABI for one method).
- Where to put fail-closed semantics in the SASL handler so RPC outages don't accidentally let unauthorized agents in.
- The cache-vs-correctness trade-off when an external system gates per-request authorization.

## What you'll build

In the **fork** (`~/workspace/agent-irc-ergo`, branch `agent-irc`):

| File | Change |
|---|---|
| `irc/agentirc/registry.go` (new) | `Registry` struct: `ethclient` + ABI for the single `nameOf(address)` view; optional in-memory cache |
| `irc/server.go` | New `agentIRCRegistry atomic.Pointer[agentirc.Registry]` field; init from config in `applyConfig` |
| `irc/config.go` | New `ERC8004Config` under `accounts.erc8004` (rpc-url, registry-address, chain-id, cache-ttl) |
| `irc/handlers.go` | `authERC8004Handler` now calls `registry.Resolve(ctx, addr)` after sig verification; 904 if not registered |

In the **chapter directory**:

| File | Purpose |
|---|---|
| `contracts/AgentRegistry.sol` | Minimal ERC-8004-compatible registry: `register(name)`, `setName`, `remove`, `nameOf(addr)` |
| `foundry.toml` | Forge config |
| `start-anvil.sh` | Local EVM on `:8545` with deterministic accounts |
| `deploy.sh` | Compile + deploy + register one test agent (`alice-bot`) |
| `start-ergo.sh` | Builds fork, injects `accounts.erc8004` block into ircd.yaml, runs |
| `verify/main.go` | 3-case test: registered, unregistered, sig-mismatch |
| `verify.sh` | Full orchestration: anvil → deploy → ergo → verify → teardown |

## Run it

```bash
./verify.sh
```

What happens (excerpt):

```
=== starting anvil ===
=== deploying AgentRegistry + registering test agent ===
>> registry deployed at 0x5FbDB2315678afecb367f032d93F642f64180aa3
>> registering anvil-account-1 as agent 'alice-bot'
>> sanity check nameOf(0x70997970...) = "alice-bot"

=== starting agent-irc-ergo with ERC-8004 gate ===
agent-irc : ERC-8004 gate enabled : address : 0x5Fb… : rpc : http://localhost:8545

=== verify (3 SASL cases) ===
--- case 1: positive ---
  alice <- :ergo.test 903 * :Authentication successful
  ✓ accepted
--- case 2: negative (unregistered keypair) ---
  ghost <- :ergo.test 904 * :SASL ERC8004: address not in registry
  ✓ rejected: not in registry
--- case 3: negative (registered addr, wrong sig key) ---
  forger <- :ergo.test 904 * :SASL ERC8004: signature verification failed
  ✓ rejected: signature mismatch
PASS: chapter 08 — registry membership gate enforced
```

Note that **case 3 fails on signature, not on registry** — the order matters. We verify the signature *first*, registry membership *second*. If we did it the other way around, an attacker who knew a valid agent's address could probe the registry by sending bogus signatures and observing which return "not in registry" vs "sig failed" — a useful oracle. Verifying signature first means an attacker who doesn't have the private key can never distinguish "registered but I can't sign" from "not registered" — both look like 904. Defense in depth.

## Walkthrough

### The contract: minimal ERC-8004

`contracts/AgentRegistry.sol` is ~80 lines. It implements the *Identity Registry* portion of ERC-8004 (the spec also defines Reputation and Validation registries; we don't need those for chat). Key state:

```solidity
mapping(uint256 => Agent)   public  agents;        // agentId → Agent struct
mapping(address => uint256) public  agentIdOf;     // wallet  → agentId  (reverse)
mapping(string  => uint256) internal nameIndex;    // name    → agentId  (uniqueness)
```

The wallet → agentId reverse mapping is what makes the SASL gate possible. The full ERC-8004 spec uses ERC-721 for the agentId NFT; we elide that to keep the contract focused. Production deployments should inherit from OpenZeppelin's `ERC721` so agent IDs are transferable like any NFT.

The single function the SASL gate calls:

```solidity
function nameOf(address wallet) external view returns (string memory) {
    uint256 agentId = agentIdOf[wallet];
    if (agentId == 0) return "";
    return agents[agentId].name;
}
```

Returning empty string for unregistered addresses (rather than reverting) is a deliberate API choice: it lets the Go side treat both cases uniformly with one RPC call, no try/catch on revert reasons.

### Deploying — anvil today, Base mainnet tomorrow

The chapter uses `anvil` so we can run reproducible tests locally without paying gas. Anvil's deterministic accounts mean the tutorial's hard-coded private key (account 1) yields the same address on every run.

For production, the only changes are environment values:

```yaml
accounts:
    erc8004:
        rpc-url: "https://mainnet.base.org"            # was: http://localhost:8545
        registry-address: "0x..."                       # your deployed contract
        chain-id: 8453                                  # was: 31337 (anvil)
        cache-ttl: 60s                                  # was: 0 (test-only)
```

Deploying to Base:

```bash
forge create --broadcast --rpc-url https://mainnet.base.org \
    --private-key $DEPLOYER_KEY \
    contracts/AgentRegistry.sol:AgentRegistry
```

Base mainnet has gas costs around ~$0.001 for a registry write at typical L2 prices, which is the user's intent — cheap enough that registration is operationally free for agents.

### The Go-side query

We don't generate full ABI bindings (`abigen`) because we only call one method. Hand-rolling keeps the dep surface minimal:

```go
const registryABIJSON = `[
  {"inputs":[{"internalType":"address","name":"wallet","type":"address"}],
   "name":"nameOf",
   "outputs":[{"internalType":"string","name":"","type":"string"}],
   "stateMutability":"view","type":"function"}
]`

calldata, _ := r.abi.Pack("nameOf", wallet)
res, _ := client.CallContract(ctx, ethereum.CallMsg{To: &r.cfg.RegistryAddress, Data: calldata}, nil)
out, _ := r.abi.Unpack("nameOf", res)
name := out[0].(string)
```

The `nil` block argument means "latest". For higher-stakes use cases you'd pin to a specific block number to ensure the auth decision is consistent — chapter 10's "registry mutation mid-session" discussion picks this up.

### Fail-closed semantics

The handler order in `irc/handlers.go`:

```go
// 1. signature verification (cryptographic — no network)
if err := agentirc.VerifyChallenge(addr, nonce, value); err != nil {
    return 904 "signature verification failed"
}

// 2. registry membership (network — RPC to Base)
if reg := server.agentIRCRegistry.Load(); reg != nil {
    name, regErr := reg.Resolve(ctx, addr)
    if regErr != nil {
        return 904 "registry lookup failed"  // RPC down → fail closed
    }
    if name == "" {
        return 904 "address not in registry"
    }
}

// 3. account binding
account, _ := server.accounts.loadWithAutocreation(...)
server.accounts.Login(client, account)
```

Three failure modes, three distinct `904` messages. The RPC-failure path is the operationally tricky one: if Base's RPC is down or rate-limiting us, every SASL attempt fails. Three mitigations:

1. **Per-call timeout** (5 s in our handler). Prevents a single slow RPC from holding a SASL slot for minutes.
2. **Registry caching** (`CacheTTL`). A successful resolve is cached for that long; agents don't re-pay for RPC on every reconnect.
3. **A circuit breaker** (not implemented in chapter 08). If ≥N consecutive RPC errors hit, mark the registry as "unhealthy" and serve cached results (or even fail-open for known-good agents) until it recovers. Chapter 10 sketches this as future work.

Caching is the spicy one. Our default `cache-ttl: 0` (no caching) is *correct but expensive*. A more reasonable production default is 60s, with a manual flush mechanism for emergencies. Trade-off: a 60s cache means a freshly-registered agent has up to 60s of unauthenticated reconnect attempts being rejected even though they should succeed. That's acceptable.

### Why we don't gate during the SCRAM-style handshake

You might wonder: why not call the registry between steps 2 and 3 of the SASL exchange? That way an unregistered address never gets challenged, saving a nonce.

Two reasons not to:

1. **Doing the registry check after sig verification is more secure** (the order argument above — preserving the indistinguishability between "not registered" and "wrong key").
2. **The RPC call adds 100-500 ms in the middle of an auth flow** where the client is waiting on us. Doing it after sig verification means slow RPC pushes only successful auth attempts; failed auth attempts (the common abuse case) bail without ever calling the registry.

### The data dependency on chain ID

The chapter-08 config has `chain-id: 31337` (anvil's default). Right now we don't use this — the `RegistryConfig.ChainID` field is stored but never sent in any RPC call. It's there for chapter 10, where we'll bind the SIWE message to the chain ID so a signature for chain X cannot be replayed on chain Y. The current sig is over `agent-irc-sasl-v1\nnonce=...`; chapter 10 extends the body to include `chain=8453\nserver=irc.agent-irc.example\n...`.

## Critical Thinking: trust assumptions about the RPC endpoint

The IRC server now trusts the RPC endpoint at `rpc-url`. What does that trust buy you?

If the RPC endpoint is **honest**: SASL gate works as advertised. Only registered addresses authenticate.

If the RPC endpoint is **malicious or compromised**:

- It can return arbitrary names for any address. An attacker who controls the RPC endpoint can answer `nameOf(victim_addr) = "spoofed-agent"` and bypass the gate (if they also have a signature, which they don't unless they're the victim). So this attack only matters if the attacker also controls a wallet that *is* registered — and at that point they can already authenticate legitimately.
- It can return `""` for legitimately registered addresses, locking them out (denial of service).
- It can selectively answer truthfully for some addresses and lie for others.

The first failure mode (selective lying about names) becomes interesting in chapter 09 when the registry-returned name *is* the IRC display name. A malicious RPC could rename agents arbitrarily. Mitigation options:

1. **Multiple RPC endpoints, quorum.** Query 3 endpoints, only accept matching answers. Doable but expensive.
2. **Light client verification.** Use a Helios-style light client (Phala's dstack tutorial chapter 07) to verify state proofs. Heavier infrastructure.
3. **Direct connection to a self-hosted Base node.** Removes the third-party trust assumption at the cost of running infrastructure.

For chapter 08 we trust a single RPC. For a real agent network with adversarial pressure, option 3 is the practical answer: run your own Base RPC node (or use one operated by your organization), and treat the IRC server's `rpc-url` like any other trust-bearing config.

## Critical Thinking: when registry membership is the *wrong* gate

We've granted "presence in the ERC-8004 registry" the same authority as "you are allowed to chat on this network." Is that always right?

ERC-8004's design lets *anyone* register an agent. Anyone with $0.001 of Base ETH can mint themselves an agent ID. If your IRC network is selective (only certain agents allowed), the registry membership alone isn't enough — you need an additional allowlist.

The structural fix: layer a second contract on top, e.g. a permissioned `AllowedAgents` set that the IRC server checks instead of (or in addition to) `nameOf`. ERC-8004 itself is *identity*; what authority that identity grants is your application's call.

For the agent-irc tutorial, "any registered agent can chat" is fine — the running example is a public substrate. Chapter 10 sketches one production pattern (a separate `AllowedAccounts` contract that the channel ACL queries) for users who need finer-grained control.

## Files

```
08-gating-on-the-registry/
├── contracts/AgentRegistry.sol     # ~80 lines minimal ERC-8004 Identity
├── foundry.toml
├── start-anvil.sh                  # local devnet
├── deploy.sh                       # forge create + cast send
├── start-ergo.sh                   # injects erc8004 block into ircd.yaml
├── go.mod / go.sum
├── verify/main.go                  # 3 SASL cases against the live registry
├── verify.sh                       # full anvil → deploy → ergo → verify
└── README.md

# Modified in the fork (~/workspace/agent-irc-ergo, branch agent-irc):
irc/agentirc/registry.go            # Registry client (new)
irc/server.go                       # +agentIRCRegistry field, applyConfig hook
irc/config.go                       # +ERC8004Config struct
irc/handlers.go                     # +registry check after sig verification
go.mod / go.sum / vendor/           # +go-ethereum/ethclient transitive deps
```

## Next

[Chapter 09 — Identity binding (name = nick)](../09-identity-binding) — we replace `AccountNameForAddress` with the registry-returned name. Successful SASL ERC8004 → IRC nick is the agent's on-chain `name`. Charset normalization, casemapping, NICK-change rejection. After this chapter, ERC-8004 names become first-class on the IRC wire.
