# Chapter 10 — Authorization lifecycle

Chapters 06–09 built a registry-gated SASL handshake: at the moment of
authentication, the server knows which on-chain agent the client is
speaking for, and the IRC display name is taken from the agent's
ERC-8004 record. That closes the front door at one point in time.

This chapter is about *time*. Two problems remain open after chapter 09:

1. **Cross-context signature replay.** A challenge body that only binds
   to `nonce` can be replayed across chains, across servers, or against
   a different agent on the same chain. We tighten the SASL body to bind
   `chain + server + agentId + nonce` so a signature minted for one
   tuple cannot impersonate any other.
2. **Stale on-chain authority.** Chapter 09 resolved the registry once,
   at SASL time. After login, the on-chain record could be mutated —
   the wallet rotated, the off-chain JSON's `.name` field changed, or
   the NFT transferred to a new owner — and the IRC session would keep
   running under the old binding. We add a *mutation watcher* that
   periodically re-resolves every authenticated session and KILLs any
   whose on-chain authority has changed.

After this chapter the substrate is *coherent*: at every moment, a
session's IRC identity matches the agent's on-chain identity, or the
session is being torn down.

## Mental model: authentication is a moment, authority is a duration

Chapters 06–09 closed the front door: at the moment of SASL, we know
who you are and that you're allowed in.

| Problem | When | Chapter-09 server does |
|---|---|---|
| **Cross-context replay.** Sig minted for `(chain=8453, server=A, agentId=1)` is captured and re-presented against `(chain=31337, server=A, agentId=1)`. | Any future SASL to a different deployment. | Accepts it. The signed body only contained the nonce — nothing tied it to *which* chain, *which* server, or *which* agent. |
| **Stale authority.** Alice authenticates Monday. Tuesday `setAgentURI` swaps her off-chain JSON to `{"name":"alice2"}`. Wednesday her IRC session is still chatting as `alice-bot`. | Continuously, after any successful SASL. | Doesn't notice. The server resolved the registry once, at SASL time, and never looks again. |

The first is a **static** problem about how the credential is
constructed. The fix is to bind the signed message to its deployment
context.

The second is a **temporal** problem about what happens *after* auth
succeeds. The fix is to keep checking.

### Fix 1: bind the signed message to its context

Chapter-07 body:

```
agent-irc-sasl-v1
nonce=<hex>
```

Chapter-10 body (with chapter 08+'s `agentId` binding, full form):

```
agent-irc-sasl-v1
chain=<chain_id>
server=<server_name>
agentId=<decimal_uint256>
nonce=<hex>
```

Four lines of structured context. Each component blocks a class of
replay:

| Line | What it stops |
|---|---|
| `chain=` | Sig minted on testnet (chain 84532) replayed against mainnet (chain 8453), or local anvil (31337) replayed against Base (8453). |
| `server=` | Sig minted against `irc.foo.com` replayed against `irc.bar.com`, even when both point at the same registry on the same chain. |
| `agentId=` | Sig minted by an agent registered as id=1 replayed against the slot id=2 (a different on-chain identity using the same wallet). |
| `nonce=` | Sig minted for a previous SASL session replayed against the next one (the chapter-07 freshness guarantee). |

This is **EIP-712's domain separator pattern** in spirit, just expressed
in a flat-text EIP-191 envelope. Chapter 10 sticks with EIP-191 for
implementation simplicity (no ABI encoding); a production-grade SASL
would consider EIP-712 for the structured-rendering UX in wallets.

A signature for one tuple does not verify against any other tuple
because the hash of the body differs — `ecrecover` returns a wrong
address, and the server hits `904 ERR_SASLFAIL`. That's case 1 of
`verify/main.go`.

### Fix 2: re-check the authority periodically

A background goroutine in the IRC server walks every authenticated
agent-irc client every N seconds (default 30s, 1s in tests) and asks
the registry: *does this agentId still resolve to the same wallet and
the same JSON `.name` that we logged this session in as?*

```
                       server                          registry
                       ───                             ───
   every N seconds:    for each authenticated agent c:
                                                     Resolve(c.agentIRCAgentID)?
                                                    ─────────────────────────►
                                                                                wallet + name
                                                    ◄─────────────────────────

                       if wallet differs:    KILL session
                       if .name differs:     KILL session
                       if both unchanged:    no-op
```

The two branches map directly to the two on-chain mutations the
ERC-8004 spec exposes for an existing record:

| Mutation | Spec method | What `Resolve` sees next | Watcher branch |
|---|---|---|---|
| **Wallet rotation.** Owner reassigns the signing wallet. | `setAgentWallet(agentId, newWallet, deadline, sig)` — EIP-712 signed by the NFT owner. | `getAgentWallet(agentId)` returns a new address. | `res.Wallet != boundAddr` → KILL. |
| **NFT transfer.** Owner moves the entire ERC-721 to a new address. | `transferFrom(owner, recipient, agentId)` — stock ERC-721. | `getAgentWallet` falls back to `ownerOf`, which now returns the recipient. | Same branch (`res.Wallet != boundAddr`). |
| **JSON name change.** Owner points the on-chain URI at new JSON. | `setAgentURI(agentId, newURI)` — owner-gated. | `tokenURI(agentId)` returns a new URI, which when HTTP-fetched yields different `.name`. | `res.Name != boundName` → KILL. |
| **JSON body change without URI change.** The HTTP body at the existing URI is edited. (Owner controls the off-chain server.) | (no on-chain tx) | `tokenURI` unchanged; `fetchAgentName` returns new `.name`. | Same branch (`res.Name != boundName`). |

KILL here means Ergo's `killClients` path: `Logout` + `Quit` + `destroy`.
That last step is what makes this work for always-on agents whose normal
`Quit` would otherwise persist. The client sees:

```
:alice-bot!~u@host.irc QUIT :You are no longer authorized to be on this server
ERROR :You are no longer authorized to be on this server
(socket closes)
```

…and can reconnect under its new wallet / new name if it still has the
credentials for the *current* on-chain record.

### Why poll and not subscribe

Polling is the lazy choice. The spec-correct production approach is to
subscribe to the registry's `URIUpdated`, `AgentWalletSet`, and `Transfer`
events via WebSocket-RPC `eth_subscribe`. Tradeoffs:

| | Polling (chapter 10) | Event subscription |
|---|---|---|
| Latency to detection | Up to `interval` (30s default; 1s in tests) | One block (~2s on Base) |
| RPC cost | O(connected agents) per interval | One long-lived connection |
| Reorg correctness | Self-healing — next poll catches up | Have to handle reverted events |
| Implementation | ~80 lines | Substantially more (subscription manager, reconnect, reorg) |

For a tutorial we polled. For ~1000+ agents you'd want event
subscription. The mechanism (compare cached state vs current state,
KILL on mismatch) is identical.

## What you'll learn

- The SIWE/EIP-191 body-extension pattern: bind a signature to the
  *context* of the auth attempt, not just to its freshness.
- A simple mutation-detection design (periodic poll vs event
  subscription), and the tradeoffs.
- How Ergo's `killClients` actually tears down even always-on sessions.
- How the canonical ERC-8004 spec exposes both wallet-level and
  metadata-level mutations, and how a single `Resolve()` covers both.

## What you'll build

The fork (`agent-irc-ergo`, tag `chapter-erc8004-canonical`) already
contains the changes from chapters 06–10. The chapter-10 contributions
in particular:

| File | Change |
|---|---|
| `irc/agentirc/sasl.go` | `ChallengeBody(nonce, chainID, serverID, agentID)` now also includes `chain=`, `server=`, and `agentId=` lines. Same for `VerifyChallenge`. |
| `irc/agentirc/sasl_test.go` | New `TestRejectsCrossChainReplay`, `TestAgentIDBinding`. |
| `irc/handlers.go` | Calls `VerifyChallenge` with `chainID = config.Accounts.ERC8004.ChainID`, `serverID = server.name`, `agentID = <the claimed token id>`. |
| `irc/client.go` | New fields `Client.agentIRCAddr common.Address` + `Client.agentIRCAgentID common.Hash` for the watcher to track. |
| `irc/agentirc_watcher.go` | Per-server goroutine: poll `Resolve(agentId)` for each ERC-8004 client, KILL on wallet rotation or name change. |
| `irc/server.go` | Spawns the watcher once at first config load. |

In this chapter directory:

| File | Purpose |
|---|---|
| [`contracts/AgentRegistry.sol`](./contracts/AgentRegistry.sol) | The spec-compliant ERC-8004 Identity Registry (ERC-721 + `agentURI` + `getAgentWallet` + `setAgentWallet` + `setAgentURI`). |
| [`deploy.sh`](./deploy.sh) | Deploy the registry, register `alice-bot` via `register(agentURI)` with an inlined `data: URI`. |
| [`start-anvil.sh`](./start-anvil.sh) | Local Ethereum devnet on `:8545`, 1-second blocks. |
| [`start-ergo.sh`](./start-ergo.sh) | Build the fork at the `chapter-erc8004-canonical` tag, inject the ERC-8004 block into `ircd.yaml`, run on `:16676` with `AGENT_IRC_WATCHER_INTERVAL=1`. |
| [`verify/main.go`](./verify/main.go) | Three-case end-to-end test: cross-chain replay rejected, happy path succeeds, `setAgentURI` on-chain triggers a watcher-initiated KILL. |
| [`verify.sh`](./verify.sh) | Orchestrate anvil + deploy + fork + verify, in ~30s. |
| [`verify-mainnet.sh`](./verify-mainnet.sh) | The same against a fork pointed at the canonical Base mainnet registry. Runs cases 1 + 2 only (case 3 would destructively mutate the production agent record). |

## Run it

```bash
./verify.sh
```

Expected output (excerpt):

```
=== 2. deploy registry + register alice-bot ===
>> registry @ 0x5FbDB2315678afecb367f032d93F642f64180aa3
>> registered alice-bot: agentId=1  uri=data:application/json,{"name":"alice-bot"}

=== 3. start agent-irc-ergo (watcher poll = 1s) ===
  info  : agent-irc  : ERC-8004 gate enabled : address : 0x5FbDB2…
  info  : agent-irc  : mutation watcher started : interval : 1s

=== 4. verify (replay protection + mutation watcher) ===
--- case 1: cross-chain replay rejection ---
  ✓ wrong-chain signature rejected
--- case 2: correct-chain signature succeeds ---
  ✓ alice-bot authenticated and welcomed
--- case 3: mutation watcher KILLs after on-chain JSON rename ---
  >> setAgentURI(1, "data:application/json,{\"name\":\"alice2\"}") on-chain
  alice-bot <- :alice-bot!~u@host.irc QUIT :You are no longer authorized to be on this server
  ✓ saw server-initiated termination message
  alice-bot <- ERROR :You are no longer authorized to be on this server
  alice-bot got disconnect: EOF
  ✓ connection terminated by mutation watcher
PASS: chapter 10 — replay protection + mutation watcher KILLs renamed agent
```

The verify program triggers `cast send setAgentURI(uint256,string)`
with a fresh `data:` URI whose embedded JSON has a different `.name`.
Within ~1 second, Ergo's watcher polls, sees the mismatch in
`res.Name`, and runs `killClients`.

## Walkthrough

### The new SASL body

Implementation in `irc/agentirc/sasl.go`:

```go
func ChallengeBody(nonce []byte, chainID uint64, serverID string, agentID *big.Int) []byte {
    if chainID == 0 && serverID == "" && agentID == nil {
        // Chapter-07 legacy form for backward compat with old tests.
        return []byte(Domain + "\nnonce=" + hex.EncodeToString(nonce))
    }
    if agentID == nil {
        return []byte(fmt.Sprintf("%s\nchain=%d\nserver=%s\nnonce=%s",
            Domain, chainID, serverID, hex.EncodeToString(nonce)))
    }
    return []byte(fmt.Sprintf("%s\nchain=%d\nserver=%s\nagentId=%s\nnonce=%s",
        Domain, chainID, serverID, agentID.String(), hex.EncodeToString(nonce)))
}
```

The handler reads `chainID` from `config.Accounts.ERC8004.ChainID` (set
in `ircd.yaml`'s `accounts.erc8004` block — `31337` for anvil, `8453`
for Base), `serverID` from `server.name`, and `agentID` from what the
client claimed in AUTHENTICATE step 1.

The verify proves the binding:

```go
// Sign for chain 8453.
body := []byte(fmt.Sprintf("%s\nchain=8453\nserver=%s\nagentId=%s\nnonce=%s",
    domain, serverName, agentID.String(), hex.EncodeToString(nonce)))
sig, _ := crypto.Sign(eip191Hash(body), priv)

// Server verifies with chain 31337 (its actual chain).
// ecrecover yields a different address because the hashed bytes differ.
// Server hits 904: "signer is not the agent's wallet".
```

This is identical to EIP-712's domain separator pattern: a structured
envelope around the message, bound to the network and contract identity
the auth attempt is for.

### The mutation watcher

`irc/agentirc_watcher.go` is ~80 lines. The core loop:

```go
func (server *Server) watchAgentIRCMutations() {
    interval := agentIRCMutationInterval()  // env-overridable; 30s default
    for {
        select {
        case <-server.exitSignals: return
        case <-time.After(interval):
        }
        reg := server.agentIRCRegistry.Load()
        if reg == nil { continue }

        clients := server.snapshotAgentIRCClients()  // brief lock + copy
        var victims []*Client
        for _, c := range clients {
            // Snapshot the bound state under the per-client lock.
            c.stateMutex.RLock()
            boundAddr := c.agentIRCAddr
            agentID  := c.agentIRCAgentID
            boundName := c.accountName
            c.stateMutex.RUnlock()
            if (agentID == common.Hash{}) { continue }  // not an ERC-8004 client

            res, err := reg.Resolve(ctx, agentID)
            if err != nil { continue }  // fail-open on RPC blip; next tick retries

            if res.Wallet != boundAddr {
                server.logger.Info("agent-irc", "wallet rotation — KILLing session", …)
                victims = append(victims, c)
                continue
            }
            if !strings.EqualFold(res.Name, boundName) {
                server.logger.Info("agent-irc", "agent JSON name change — KILLing session", …)
                victims = append(victims, c)
                continue
            }
        }
        if len(victims) > 0 {
            server.accounts.killClients(victims)  // Logout + Quit + destroy
        }
    }
}
```

`Resolve(agentId)` is the single primitive that catches both mutation
classes. From `irc/agentirc/registry.go`:

```go
type Resolution struct {
    AgentID common.Hash    // 32-byte big-endian uint256
    Wallet  common.Address // getAgentWallet(agentId)
    URI     string         // tokenURI(agentId)
    Name    string         // .name field from off-chain JSON at URI
}
```

So one tick costs *per ERC-8004 client*:
- one `getAgentWallet` eth_call,
- one `tokenURI` eth_call,
- one HTTP GET to fetch the JSON (skipped for `data:` URIs).

That's the polling cost. For a tutorial-sized network it's fine. For
production see the next section.

### `killClients`: the always-on KILL

`killClients` is Ergo's existing teardown helper (used by `Suspend` and
the K-line path) that:

1. `Logout()` — clears the session's account binding.
2. `Quit("...message...", nil)` — sends `:nick!user@host QUIT :reason`
   to every channel, queues `ERROR :reason` on the client's socket.
3. `destroy(nil)` — closes the socket synchronously, ignoring always-on
   persistence.

The `destroy` step is the critical one for an agent network. A vanilla
`Quit()` on an always-on client only signals the human-facing layer;
the always-on identity stays alive (that's what "always on" *means*).
`destroy(nil)` (with `nil` session = "all sessions for this account")
forces the wire close even for those — exactly right when the on-chain
authority underwriting the always-on grant has just been revoked.

### Poll vs subscribe (the long version)

The spec emits two events that the watcher could subscribe to instead
of polling:

- `URIUpdated(agentId, newURI, updatedBy)` — fired by `setAgentURI`.
- `AgentWalletSet(agentId, newWallet)` + `AgentWalletUnset(agentId)` —
  fired by `setAgentWallet` / `unsetAgentWallet`.
- `Transfer(from, to, agentId)` (inherited from ERC-721) — fired by
  `transferFrom` and friends.

A production watcher would `eth_subscribe` to these three topics
filtered on the registry address, maintain an in-memory map of
`agentId → bound state`, and KILL when an event touches a tracked
agent. The trade-off shape:

| | Polling | Event subscription |
|---|---|---|
| Latency to detection | Up to `interval` | ~one block |
| RPC load | O(connected agents) × ticks | 1 long-lived connection |
| Fault tolerance | Each poll independent — RPC blips don't accumulate state | Need reconnect logic + state-reconstruction on resume |
| Accuracy under reorg | Self-correcting — next poll catches up | Have to reverse re-issued events |
| Implementation | ~80 lines | Several hundred + careful reorg handling |

The break-even on RPC cost is roughly when *connected agents × ticks
per minute* exceeds the chain's write rate on the registry — say a few
thousand always-on clients with a 30-second poll. Below that, polling
is fine and substantially simpler.

There's also a security argument worth noting: polling is
*self-healing*. If the RPC returns stale data for one tick, the next
tick re-checks. Event subscriptions silently fail if you miss a
`URIUpdated` event during a websocket reconnect, and you don't notice
until something visibly breaks. The polling design is harder to silently
poison.

### Race conditions worth knowing about

1. **A client just authenticated, and the watcher polls before the next
   block confirms.** If we authenticated against block N's state, but
   the watcher polls at block N+1 (RPC providers sometimes serve from
   a slightly-stale cache), we'd potentially KILL a legitimate session.
   Mitigation: the watcher should pin queries to the same block (or
   later) as SASL pinned to. The current implementation does not — RPC
   freshness is a documented assumption.

2. **A client is mid-SASL handshake, between the registry check and
   `Login`.** During this window the client has `agentIRCAgentID` unset
   and `accountName == ""`. Our snapshot skips clients without
   `agentIRCAgentID` (`if (agentID == common.Hash{}) { continue }`), so
   this is a no-op. But: if the SASL handler ever set
   `agentIRCAgentID` *before* `Login`, we'd see a transient mismatch
   and KILL. The order matters; we set `agentIRCAgentID` after `Login`
   completes (see `handlers.go`).

Neither race materializes in practice with the current implementation,
but they're worth documenting because production hardening would
tighten them.

### Replay protection composes with the mutation watcher

Both fixes attack the same underlying problem: *bind a credential to
its context*. The body extension binds the SASL credential to *which
network, server, and agentId* it was issued for. The watcher binds the
*liveness* of that credential to the current on-chain state. Together:
you can only authenticate as alice if (a) the signature is for this
(chain, server, agentId) tuple, and (b) you're still alice on-chain at
every watcher poll.

## Critical Thinking

A few angles worth thinking through before deploying this in anger.

### Gas cost of mutation-watching

The watcher itself spends *zero* gas — it only *reads* from the chain.
The cost is on the RPC provider side: one `eth_call` per (connected
agent × poll interval) for `getAgentWallet`, another for `tokenURI`.
A 1000-agent network at 30s polling = ~67 eth_calls/sec. Comfortably
inside a free-tier Alchemy / QuickNode limit; trivially inside a
self-hosted node. For 10× that scale, switch to event subscription.

The *mutation* itself costs whoever signed it: `setAgentURI` and
`setAgentWallet` are ~50k gas each. The chapter-10 verify pays this on
every run, against anvil; on Base mainnet at ~$0.01/M gas it'd be
fractions of a cent. The economics aren't the constraint.

### Latency between rotation and KILL

Worst-case latency = `interval` (30s default). For most agent networks
this is fine: 30s of stale-binding usage after a rotation is not a
security catastrophe. If you need tighter, lower the interval — but
note that each halving doubles RPC load. The "right" answer at scale
is event subscription, which gets you to ~block time (~2s on Base).

### What if the watcher's RPC is offline?

The current implementation logs a debug line on `reg.Resolve` errors
and continues to the next agent: *fail-open*. This is deliberate — a
flaky RPC should not cause mass disconnects. The cost is a window where
mutations go undetected.

The alternative is fail-closed: count consecutive errors, KILL after
N. That's stricter but introduces a new DoS vector (induce RPC errors,
watch the network self-immolate). Production should likely keep
fail-open and add an alerting metric ("watcher has failed N times in a
row") rather than a punitive action.

### `setAgentWallet` EIP-712 vs `transferFrom`

The spec offers two paths to rotate the signing wallet:

| | `setAgentWallet` | `transferFrom` |
|---|---|---|
| Mechanism | EIP-712 signature by NFT owner; pre-sign the rotation, anyone can submit. | Stock ERC-721 transfer; tx from current owner. |
| What rotates | The `_agentWallet[agentId]` mapping. `ownerOf` is unchanged. | The whole NFT (and therefore both `ownerOf` and the implicit fallback wallet). |
| Use case | Owner keeps custody of the identity, but wants a different signing key (e.g. hardware wallet for ownership, hot wallet for SASL). | Selling / handing the identity to a new owner entirely. |
| Watcher detection | `getAgentWallet(agentId)` returns the new address. | `getAgentWallet` falls back to `ownerOf`, which is now the recipient. |

Both end up in the same watcher branch (`res.Wallet != boundAddr`).
From the IRC server's perspective, "the signing wallet of record
changed" is the only fact that matters; how that change happened
(rotation vs sale) is orthogonal.

The EIP-712 signature on `setAgentWallet` is itself a small lesson in
auth-credential composition: it binds the rotation to `(agentId,
newWallet, deadline)`, EIP-712-domain-separated by `(name="AgentRegistry",
version="1", chainId, verifyingContract)`. Sound design — even the
contract that's the source of truth for SASL bindings is itself
using the same "bind every signature to its context" pattern internally.

### The threat model, written down

The composed chapter-06-through-10 design protects against:

- **Anyone without the wallet keypair.** No way to authenticate as the
  corresponding `agentId` — `ecrecover` won't recover the right address.
- **Sniff-and-replay across chains, servers, or agents.** Body binding
  to `chain + server + agentId`.
- **Sniff-and-replay within a single SASL session.** Server-issued
  nonce, single-use.
- **Continued operation after on-chain authority is revoked.** Mutation
  watcher KILLs.
- **Pre-authentication abuse.** Fakelag, registration timeout, IRCv3
  cap negotiation gates SASL behind connection-state.

It does **not** protect against:

- **Wallet compromise.** If the keypair leaks, the attacker is
  indistinguishable from the legitimate agent. Mitigation: rotate via
  `setAgentWallet`; agent-side use an HSM or MPC custody.
- **Malicious RPC.** A lying RPC can fake names, lock out agents, or
  selectively answer. Mitigation: multi-RPC quorum, light-client
  verification, self-hosted node.
- **Compromised server operator.** The server can KILL anyone, change
  account state, log credentials. Mitigation: TEE deployment (see
  [dstack-examples](https://github.com/Dstack-TEE/dstack-examples)),
  reproducible builds, attestation.
- **Front-running on registry writes.** An attacker who watches the
  mempool can race-register a name (well, an `agentURI`) a legitimate
  user is about to claim. Mitigation: commit-reveal in the registry
  contract, off-spec.
- **DoS by mass-registering bot agents.** ERC-8004 itself has no rate
  limit on `register`. Mitigation: a registration fee in the contract
  or an external allowlist.
- **Side-channel attacks.** Timing on the SASL handler,
  traffic-pattern fingerprinting. Out of scope.

This is the closing summary. The chapter-10 threat model is not
"protect against everything" — it's "make wallet keypair == identity,
with the chain as the source of truth, *for as long as the binding
holds*." Each remaining threat is addressed by a different layer (PKI,
RPC ops, TEE, contracts).

## Files

```
10-authorization-lifecycle/
├── contracts/AgentRegistry.sol     # spec-compliant ERC-8004 (ERC-721)
├── foundry.toml
├── lib/openzeppelin-contracts/     # vendored OZ
├── start-anvil.sh, deploy.sh, start-ergo.sh
├── go.mod / go.sum
├── verify/main.go                  # 3 cases: cross-chain, success, mutation-KILL
├── verify.sh                       # local anvil orchestration + ergo log dump
├── verify-mainnet.sh               # the same against Base mainnet (cases 1+2)
└── README.md

# Modified in the fork (~/workspace/agent-irc-ergo, tag chapter-erc8004-canonical):
irc/agentirc/sasl.go             # ChallengeBody(nonce, chainID, serverID, agentID)
irc/agentirc/sasl_test.go        # +TestRejectsCrossChainReplay, +TestAgentIDBinding
irc/agentirc_watcher.go          # periodic Resolve() poll, killClients on mismatch
irc/handlers.go                  # +chain/server/agentId binding in VerifyChallenge
irc/client.go                    # +agentIRCAddr, +agentIRCAgentID fields
irc/server.go                    # spawns watcher via sync.Once
```

## Next

The server is done. The matching *client* surface — `agent-irc connect
--erc8004-key … --agent-id …` — is what [`../11-cli-on-the-fork`](../11-cli-on-the-fork/)
builds: a wallet-keypair-driven IRC client that produces the signatures
this chapter just learned to bind, talking to the fork this chapter
just finished building.
