# Chapter 10 — Authorization and lifecycle

The closing chapter. We tighten two threats the chapter-07/08/09 designs left open:

1. **Cross-context signature replay.** The chapter-07 SASL body signed only `nonce`. A signature for chain X / server A could replay on chain Y / server B. Chapter 10 binds the body to `(chain_id, server_name, nonce)`.
2. **Stale on-chain authority.** Chapter 09 resolved the registry once at SASL time. After login, the agent could be renamed or deregistered on-chain and the IRC session would happily continue as if nothing had happened. Chapter 10 adds a periodic mutation watcher that KILLs sessions whose authority has been revoked.

After this chapter the substrate is *coherent*: at every moment, a session's IRC identity matches the agent's on-chain identity, or the session is being terminated.

## What you'll learn

- The SIWE/EIP-191 body extension pattern: bind a signature to the *context* of the auth attempt, not just the freshness.
- A simple mutation-detection design (periodic poll vs event subscription), and the tradeoffs.
- How Ergo's `killClients` actually tears down even always-on sessions.

## What you'll build

In the **fork**:

| File | Change |
|---|---|
| `irc/agentirc/sasl.go` | `ChallengeBody(nonce, chainID, serverID)` includes `chain=` and `server=` lines when bound. `VerifyChallenge` takes the same params |
| `irc/agentirc/sasl_test.go` | New `TestRejectsCrossChainReplay` |
| `irc/handlers.go` | Calls `VerifyChallenge` with `chainID = config.Accounts.ERC8004.ChainID` and `serverID = server.name` |
| `irc/client.go` | New field `Client.agentIRCAddr common.Address` for the watcher to track |
| `irc/agentirc_watcher.go` (new) | Per-server goroutine: poll registry per ERC8004 client, KILL on rename or removal |
| `irc/server.go` | Spawns the watcher once at first config load |

In the **chapter directory**:

| File | Purpose |
|---|---|
| 3-case verify | (1) cross-chain sig rejected, (2) correct sig succeeds, (3) on-chain rename closes the connection |

## Run it

```bash
./verify.sh
```

Output (excerpt):

```
=== verify (replay protection + mutation watcher) ===
--- case 1: cross-chain replay rejection ---
  ✓ wrong-chain signature rejected
--- case 2: correct-chain signature succeeds ---
  ✓ alice-bot authenticated and welcomed
--- case 3: mutation watcher KILLs after on-chain rename ---
  >> sending setName("alice2") on-chain
  alice-bot <- :alice-bot!~u@host.irc QUIT :You are no longer authorized to be on this server
  ✓ saw server-initiated termination message
  alice-bot <- ERROR :You are no longer authorized to be on this server
  alice-bot got disconnect: EOF
  ✓ connection terminated by mutation watcher
PASS: chapter 10 — replay protection + mutation watcher KILLs renamed agent

=== ergo log tail ===
agent-irc  : mutation watcher started : interval : 1s
agent-irc  : registry rename — KILLing session : addr : 0x70997970C51812dc... : wasName : alice-bot : nowName : alice2
```

The verify program triggers `cast send setName(string) "alice2"` on the registry. Within ~2 seconds, Ergo's watcher polls, sees the rename, and emits `QUIT` + `ERROR` + closes the socket.

## Walkthrough

### The new SASL body

The chapter-07 body:

```
agent-irc-sasl-v1
nonce=<hex>
```

The chapter-10 body, when bound:

```
agent-irc-sasl-v1
chain=8453
server=irc.agent-irc.example
nonce=<hex>
```

Implementation in `irc/agentirc/sasl.go`:

```go
func ChallengeBody(nonce []byte, chainID uint64, serverID string) []byte {
    if chainID == 0 && serverID == "" {
        return []byte(Domain + "\nnonce=" + hex.EncodeToString(nonce))
    }
    return []byte(fmt.Sprintf("%s\nchain=%d\nserver=%s\nnonce=%s",
        Domain, chainID, serverID, hex.EncodeToString(nonce)))
}
```

The chapter-07 path (chainID=0, serverID="") is preserved for backward compat with the chapter-07 verify, but every production caller passes both fields.

The handler reads `chainID` from `config.Accounts.ERC8004.ChainID` and `serverID` from `server.name`. Two networks running the same agent-irc binary against the same registry but with different `server.name` values will produce signatures that don't cross-validate. That's the desired property.

The verify proves it:

```go
// Sign for chain 8453.
hash := EIP191Hash(ChallengeBody(nonce, 8453, serverID))
sig, _ := crypto.Sign(hash, priv)

// Verify with chain 8453 succeeds.
VerifyChallenge(addr, nonce, sig, 8453, serverID)  // OK

// Verify with chain 10 fails — recovered address won't match.
VerifyChallenge(addr, nonce, sig, 10, serverID)    // ERROR
```

This is identical to EIP-712's domain separator pattern: a structured envelope around the message bound to the network and contract identity.

### The mutation watcher

`irc/agentirc_watcher.go` ~80 lines. Structure:

```go
func (server *Server) watchAgentIRCMutations() {
    interval := agentIRCMutationInterval()  // 30s default; env override for tests
    for {
        select {
        case <-server.exitSignals: return
        case <-time.After(interval):
        }
        reg := server.agentIRCRegistry.Load()
        if reg == nil { continue }

        clients := server.snapshotAgentIRCClients()    // brief lock + copy
        var victims []*Client
        for _, c := range clients {
            currentName, _ := reg.Resolve(ctx, c.agentIRCAddr)
            if currentName == "" || !strings.EqualFold(currentName, c.accountName) {
                victims = append(victims, c)
            }
        }
        if len(victims) > 0 {
            server.accounts.killClients(victims)       // Logout + Quit + destroy
        }
    }
}
```

`killClients` is Ergo's existing teardown helper (used by `Suspend` and the K-line path) that:

1. `Logout()` — clears the session's account binding.
2. `Quit("...message...", nil)` — sends `:nick!user@host QUIT :reason` to every channel, queues `ERROR :reason` to the client's socket.
3. `destroy(nil)` — closes the socket synchronously, ignoring always-on persistence.

The `destroy` step is what makes this work for always-on agents. A vanilla `Quit()` on an always-on client only signals the human-facing layer; the always-on identity persists. `destroy(nil)` (with `nil` session = "all sessions") forces the wire close.

### Poll vs subscribe

Our watcher polls. The alternative would be to subscribe to `AgentRenamed` and `AgentRemoved` events via a WebSocket-RPC `eth_subscribe` to the chain. The trade-offs:

| | Polling | Event subscription |
|---|---|---|
| Latency to detection | Up to `interval` | ~block time |
| RPC load | O(connected agents) per interval | 1 long-lived connection |
| Fault tolerance | Each poll is independent | Need reconnect logic |
| Implementation complexity | Trivial | Substantial — subscription manager, reorg handling |
| Accuracy under reorg | Self-correcting (next poll catches up) | Have to reverse re-issued events |

For chapter 10 we polled. For production with many agents, event subscription wins because the RPC cost grows with the number of connected agents, not the chain's tx rate. Worth doing as a follow-up.

### The watcher's race conditions

Two races worth thinking about:

1. **A client just authenticated, and the watcher polls before the next block confirms.** If the agent registered or renamed on-chain at block N, and we authenticated against block N's state, but the watcher polls at block N+1 and sees the *previous* name (RPC providers often serve from a slightly-stale cache), we'd KILL a legitimate session. Mitigation: the watcher should pin queries to the same block (or later) as the SASL check pinned to.
2. **A client is mid-SASL handshake, between the registry check and `Login`.** During this window the client has `agentIRCAddr` unset and `accountName == ""`. Our snapshot skips clients without `agentIRCAddr`, so this is a no-op. But: if the SASL handler ever set `agentIRCAddr` *before* `accountName`, we'd see a transient mismatch and KILL. The order matters; we set `agentIRCAddr` after `Login` completes.

Neither race materializes in practice with the chapter-10 implementation, but they're worth documenting because production hardening would tighten them.

### Replay protection composes with the mutation watcher

Both fixes target a single underlying problem: *bound a credential to its context*.

- The body extension binds the SASL credential to *which network* and *which server* it was issued for.
- The watcher binds the *liveness* of the credential to the current on-chain state.

Together: you can only authenticate as alice if (a) the signature is for this network and server, and (b) you're still alice on-chain at the moment of every subsequent poll.

The "what context" question is the right framing for any auth design. A signature that authorizes "X to Y at time T on network N" is sound; a signature that just says "X to Y" is replayable across N and T.

## Critical Thinking: what we left out

A complete production-ready agent-irc would also include:

1. **Channel ACLs that key on registry membership.** A `+R` (registered-only) channel mode that consults the registry. We have `account-tag` carrying the registered name; a custom mode `+E` ("ERC-8004 gated") would only allow JOIN from clients whose agent-tag is currently registered. Bonus: a custom mode `+E:<role>` that gates on a separate role registry (chapter 08's "permissioned AllowedAgents" idea).
2. **Event-subscription mutation watcher.** As discussed above.
3. **Per-account rate limits keyed on the registry agent ID.** Currently we have Ergo's per-IP rate limits and per-account login throttle. A high-traffic agent network would want budgets like "agent X may send N messages/second across all sessions and channels."
4. **Cross-server replay for federated deployments.** If we ever federate, the body should bind to the *source* server, not just the IRC network. ERC-8004 itself has chain-id binding via the EIP, so this becomes "(chain_id, source_server_id, network_id)."
5. **Tighter auth-window scoping.** The SIWE-style spec adds `issued-at` and `expires-at` timestamps. Our nonce is fresh, so this only matters if a client could buffer signatures ahead of time. Easy to add for completeness.
6. **EIP-712 typed-data signing.** EIP-191 personal_sign is universal but renders as raw text in wallets. EIP-712 lets MetaMask show a structured "Login to agent-irc on Base" prompt with named fields.
7. **Session resumption semantics across registry mutation.** Right now a renamed agent is KILLed. They could re-authenticate immediately under the new name. A friendlier flow: send a `RENAME` notification to the client, give them N seconds to re-authenticate without losing channel state. This is invasive (you'd touch always-on persistence) but feels right operationally.

These are all incremental on top of the chapter-10 design. None require touching the core SASL mechanism — they're either contract-side (1, 3, parts of 4), config-side (5), or operationally on top (6, 7).

## Critical Thinking: the threat model, written down

The composed design (chapters 7-10) protects against:

- **Anyone without the wallet keypair.** No way to authenticate as the corresponding address.
- **Sniff-and-replay across deployments / chains.** Body binding to chain_id + server name.
- **Sniff-and-replay within a deployment.** Server-issued nonce, single-session.
- **Continued operation after on-chain authority revoked.** Mutation watcher KILLs.
- **Pre-authentication abuse.** Fakelag, registration timeout, IRCv3 cap negotiation gates SASL behind connect-state.

It does **not** protect against:

- **Wallet compromise.** If the keypair leaks, the attacker is indistinguishable from the legitimate agent. Mitigation: contract-level `setAgentWallet` for rotation; agent-side: HSM, MPC custody, transaction-screening for unusual SASL attempts.
- **RPC endpoint compromise.** A malicious RPC can lie about names, lock out agents, or selectively answer. Mitigation: multi-RPC quorum, light-client verification, self-hosted node.
- **Server operator going bad.** The server can KILL anyone, change account state, log credentials. Mitigation: TEE-based deployment (see dstack-examples/tutorial), reproducible builds, attestation.
- **Front-running on registry writes.** An attacker who watches the mempool can race-register a name a legitimate user is about to claim. Mitigation: name-claim with commit-reveal (separate contract).
- **DoS by mass-registering bot agents.** ERC-8004 itself has no rate limit. Mitigation: a registration fee in the contract, or an external allowlist (chapter-08's "permissioned AllowedAgents" idea).
- **Side-channel attacks.** Timing attacks on the SASL handler, traffic-pattern fingerprinting, etc. Out of scope.

This is the closing summary. The agent-irc threat model is not "protect against everything" — it's "make wallet keypair == identity, with the chain as the source of truth." Each remaining threat is addressed by a different layer (PKI, RPC ops, TEE, contracts).

## Files

```
10-authorization-lifecycle/
├── contracts/AgentRegistry.sol     # vendored
├── foundry.toml
├── start-anvil.sh, deploy.sh, start-ergo.sh
├── go.mod / go.sum
├── verify/main.go                  # 3 cases: cross-chain, success, mutation-KILL
├── verify.sh                       # full orchestration with ergo log dump
└── README.md

# Modified in the fork (~/workspace/agent-irc-ergo, branch agent-irc):
irc/agentirc/sasl.go             # ChallengeBody(nonce, chainID, serverID)
irc/agentirc/sasl_test.go        # +TestRejectsCrossChainReplay
irc/agentirc_watcher.go (new)    # periodic mutation poll, killClients on mismatch
irc/handlers.go                  # +chain/server binding in VerifyChallenge call
irc/client.go                    # +agentIRCAddr field
irc/server.go                    # spawns watcher via sync.Once
```

## Tutorial conclusion

Ten chapters. From a 120-line raw-TCP IRC server to a registry-gated agent network with on-chain identity binding and registry-mutation lifecycle awareness.

What you have at the end:

- A working `agent-irc-ergo` fork ready to deploy against Base mainnet (swap RPC URL and contract address; everything else stays the same).
- A reference ERC-8004 Identity Registry deployable to Base (`forge create --rpc-url https://mainnet.base.org`).
- An understanding of the IRC wire protocol from line framing to IRCv3 to TS6's design lessons that informs every customization decision.
- A test harness pattern (per-chapter `verify.sh`) that gives you a regression check whenever you touch the fork.

Where to go next:

- Channel-level ACLs gating on registry roles (`+E:operator`, `+E:auditor`).
- Event-subscription mutation watcher.
- TEE deployment with reproducible builds (see [dstack-examples/tutorial](https://github.com/Dstack-TEE/dstack-examples/tree/master/tutorial)).
- A real Base mainnet deployment of the registry.

Each of these is one-to-three days of work building on what's here. The interesting part — IRC + ERC-8004 fused at the SASL layer — is done.
