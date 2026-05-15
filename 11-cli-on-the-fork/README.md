# Chapter 11 — Agent on the fork: ERC-8004 SASL through the CLI

The closing chapter. Chapters 01–10 build a server: an Ergo fork whose
SASL handler authenticates against the **canonical ERC-8004 Identity
Registry** ([EIP-8004](https://eips.ethereum.org/EIPS/eip-8004)), resolves
the agent's display name from the off-chain JSON pointed to by
`tokenURI(agentId)`, forces that name as the IRC nick, binds the signed
challenge to `(chain_id, server_name, agentId)` to defeat replay, and
KILLs the session when the on-chain agent record mutates. This chapter is
the matching *client* surface — the [`agent-irc` CLI](../cli) talking to
that server, with a wallet keypair in place of a password and an
`--agent-id` flag declaring which on-chain identity it's claiming.

End state, in one line:

```bash
agent-irc connect localhost:16678 --nick alice-bot \
    --erc8004-key keys/alice-bot.key --agent-id 1 \
    --chain-id 31337 --server-name ergo.test
```

…and the rest of the appendix-cli-agent ergonomic (`join`, `send`,
`tail`) carries over unchanged. The `--password` PLAIN flow is replaced
by `--erc8004-key` + `--agent-id`; everything above the SASL layer is the
same wire shape, the same daemon, the same JSONL events.

> **Why `--agent-id` and not just `--erc8004-key`?** ERC-8004 has
> *no reverse address→agentId lookup* on-chain. The agent must declare
> which on-chain identity it's authenticating as; the server then calls
> `getAgentWallet(agentId)` to learn whose signature it expects to
> recover. See [the architectural sketch](#how-the-cli-does-it-architectural-sketch)
> below for why this shape is forced by the spec.

## What this chapter contains

| File | What |
|---|---|
| [`start-anvil.sh`](./start-anvil.sh) | Local Ethereum devnet on `:8545`, 1-second blocks. |
| [`contracts/AgentRegistry.sol`](./contracts/AgentRegistry.sol) | A non-upgradeable single-file ERC-8004 Identity Registry (ERC-721 + URIStorage + EIP-712 `setAgentWallet`). The interface matches the canonical Base mainnet deployment at `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432`. |
| [`deploy.sh`](./deploy.sh) | Forge-deploy the registry locally, then `register(agentURI)` three agents with inline `data:application/json,{"name":"<nick>"}` URIs. Captures the agentId from each `Registered` event and writes it to [`keys/<nick>.agentid`](./keys/). |
| [`start-ergo.sh`](./start-ergo.sh) | Build the fork at the `chapter-erc8004-canonical` tag, inject the `accounts.erc8004` block into `ircd.yaml`, run on `:16678`. |
| [`start-ergo-base.sh`](./start-ergo-base.sh) | Same as above, but configures the registry block to point at **Base mainnet** (`0x8004A169…` + `https://mainnet.base.org`). Used by `verify-base-mainnet.sh`. |
| [`agent.json`](./agent.json) | The off-chain JSON for the mainnet test agent (agentId 51075, name `lin-test-bot`). Served at `https://raw.githubusercontent.com/linoscope/agent-irc/main/11-cli-on-the-fork/agent.json` — the URL `tokenURI(51075)` returns. |
| [`verify.sh`](./verify.sh) | End-to-end offline: boot the whole stack, connect alice + bob via ERC8004 SASL, exchange messages, assert server-stamped `account-tag` matches the JSON-derived name, smoke-test that an unregistered agentId is rejected. |
| [`verify-base-mainnet.sh`](./verify-base-mainnet.sh) | Same, but against the **canonical Base mainnet registry**. Reads the funded test agent from `../.env`, no gas spent (reads only). |
| [`screenshots/`](./screenshots/) | Captures from the [viewer](../viewer) — what `#agents` looks like when both agents are on-chain-authenticated. |

## Mental model: what changes vs. the appendix

[`appendix-cli-agent`](../appendix-cli-agent) runs the same CLI against
**stock Ergo** with SASL PLAIN. The bytes on the wire below the
registration handshake are identical to this chapter. The only thing
that changes is the SASL window.

```
                           appendix-cli-agent                chapter 11 (spec-aligned)
                           ─────────────────────             ─────────────────────
  credential               password                          wallet private key
  what server stores       bcrypt(password)                  nothing (`ecrecover` + RPC)
  who picks the name       user, at register time            agent JSON's `.name` field
  on-chain primitive       —                                 ERC-721 NFT (agentId)
  account-tag value        whatever the user typed           always = JSON `.name`
  flag                     --password PW                     --erc8004-key PATH
                                                             --agent-id N
                                                             --chain-id 31337
                                                             --server-name ergo.test
```

The `--chain-id`, `--server-name`, and `--agent-id` flags are not cosmetic
— they get *baked into the signed body* by the CLI, which is what defeats
cross-chain, cross-server, and **cross-agent** replay. A signature minted
for `(chain=31337, server=ergo.test, agentId=1)` will not verify against
any other tuple.

### How the CLI does it (architectural sketch)

Three constraints shape the implementation:

1. **ERC-8004 has no reverse lookup.** The on-chain registry only
   exposes `getAgentWallet(agentId) → address` (and `tokenURI(agentId)
   → string`). There's no `agentIdOf(address)` getter. So the client
   must declare which on-chain identity it's claiming; the server
   queries the registry, learns the expected signer, then issues the
   nonce challenge. That's why the CLI takes `--agent-id` rather than
   inferring it from the key.

2. **`ircevent` (the Go IRC library the CLI is built on) only ships
   PLAIN and EXTERNAL natively.** Adding a third mechanism without
   vendoring the library required wedging into its existing PLAIN path:
   - `--erc8004-key` causes the bridge to configure `SASLMech = "PLAIN"`
     so ircevent's pre-dial validation passes.
   - The bridge installs a `CAP` callback that fires *before* ircevent's
     own `handleCAP`. The moment the server ACKs the `sasl` capability,
     the callback flips `SASLMech` to `"ERC8004"`. ircevent then issues
     `AUTHENTICATE ERC8004` instead of `AUTHENTICATE PLAIN`, with no
     knowledge that the swap happened.
   - The bridge installs an `AUTHENTICATE` callback that walks the
     three rounds: base64 agentId, then sign-the-nonce, then wait for
     903 — using ircevent's existing 903/904 plumbing for success/fail
     propagation.

3. **The signed body binds (chain, server, agentId, nonce).** Any of
   those four differing between sign-time and verify-time recovers a
   wrong address; the SASL handler then rejects with 904.

The entire SASL extension is **~100 lines** in
[`cli/internal/bridge/bridge.go`](../cli/internal/bridge/bridge.go),
plus a ~85-line key + signature helper at
[`cli/internal/erc8004/sign.go`](../cli/internal/erc8004/sign.go).
No fork of `irc-go`, no monkey patching, no replace directive in `go.mod`.

For the spec the CLI is implementing, see
[`agent-irc-ergo/irc/agentirc/sasl.go`](https://github.com/linoscope/agent-irc-ergo/blob/chapter-erc8004-canonical/irc/agentirc/sasl.go)
in the fork — both sides hash the same body:

```
agent-irc-sasl-v1
chain=<chain-id>
server=<server-name>
agentId=<decimal>
nonce=<hex>
```

EIP-191 prefixed (`\x19Ethereum Signed Message:\n<len>`) and
Keccak256-hashed before `crypto.Sign`. Chapter 07 introduces the SASL
mechanism (signature only); chapter 08 adds the registry lookup; chapter
09 adds the JSON-name-to-IRC-nick binding; chapter 10 adds the replay
binding (chain + server) and mutation watcher.

## Walkthrough

### Prerequisites

- The same prerequisites as chapter 10 (anvil/forge/cast via `foundryup`).
- The fork cloned at `~/workspace/agent-irc-ergo` with the
  `chapter-erc8004-canonical` tag reachable (see the
  [top-level README](../README.md#repository-layout) if you don't have it
  yet).
- `jq` for parsing JSONL output.
- Three terminals (or `tmux` panes).

### 1. Build the CLI

```bash
cd ~/workspace/agent-irc/cli
go build -o /tmp/agent-irc ./cmd/agent-irc
```

### 2. Boot anvil + deploy the registry + register three agents (terminal A)

```bash
cd ~/workspace/agent-irc/11-cli-on-the-fork
./start-anvil.sh &   # leave running in the background of terminal A
./deploy.sh
```

Expected output:

```
>> registry @ 0x5FbDB2315678afecb367f032d93F642f64180aa3
>> alice-bot: agentId=1
>> bob-bot: agentId=2
>> monitor: agentId=3
```

This writes:

- `.registry-address` — the contract address (consumed by `start-ergo.sh`).
- `keys/alice-bot.key`, `keys/bob-bot.key`, `keys/monitor.key` — the
  three private keys that `agent-irc connect --erc8004-key` will load.
- `keys/alice-bot.agentid`, `keys/bob-bot.agentid`, `keys/monitor.agentid`
  — the ERC-721 token id minted for each agent at registration time,
  parsed from the `Registered(uint256,string,address)` event. The CLI
  uses these for the `--agent-id` flag.

Each registration also stamps an **inline agent JSON** on-chain as a
`data:application/json,{"name":"<nick>"}` URI. The fork's SASL handler
parses that during step 3 to derive the IRC nick. In production this URI
would be `https://...` or `ipfs://...`; we inline it here so the test
loop doesn't need an HTTP server. See
[`agent.json`](./agent.json) for the corresponding mainnet shape.

> The keys here are anvil's *publicly-known* deterministic test keys,
> not real wallets. Don't reuse this pattern for production: in real
> deployments you'd hold the key in a hardware wallet, KMS, or an
> equivalent — the CLI only needs to be told how to produce one signature
> per session.

### 3. Start the fork (terminal A, after anvil)

```bash
./start-ergo.sh
```

You'll see the `chapter-erc8004-canonical` tag get checked out and the
binary built into `/tmp/ergo-agentirc-ch11`. The key lines in the log:

```
info  : agent-irc : ERC-8004 gate enabled : address : 0x5FbDB...
info  : agent-irc : mutation watcher started : interval : 30s
info  : listeners : now listening on :16678
```

Leave this running.

### 4. Connect alice-bot via ERC-8004 SASL (terminal B)

```bash
ALICE_ID=$(cat keys/alice-bot.agentid)
/tmp/agent-irc connect localhost:16678 --nick alice-bot \
    --erc8004-key keys/alice-bot.key --agent-id "$ALICE_ID" \
    --chain-id 31337 --server-name ergo.test
```

If everything is wired correctly you'll see `ok` (or `ok (daemon already
running)` on subsequent invocations). Under the hood:

1. The daemon spawns, dials TCP `:16678`, sends `CAP LS 302` / `NICK` /
   `USER`.
2. The server lists its caps (including `sasl`).
3. The daemon sends `CAP REQ :account-tag server-time message-tags
   batch multi-prefix echo-message sasl` and the server ACKs.
4. The bridge's CAP callback fires on the ACK, flips `SASLMech` to
   `"ERC8004"`.
5. ircevent then sends `AUTHENTICATE ERC8004`. The fork's SASL handler
   replies `AUTHENTICATE +`.
6. The daemon sends `base64(32-byte uint256 agentId)`. The fork calls
   `getAgentWallet(agentId)` against the on-chain registry, learns the
   expected signing wallet, and sends `AUTHENTICATE <base64(32-byte nonce)>`.
7. The daemon signs
   `agent-irc-sasl-v1\nchain=31337\nserver=ergo.test\nagentId=<dec>\nnonce=<hex>`
   with EIP-191 and sends `AUTHENTICATE <base64(65-byte sig)>`.
8. The fork `ecrecover`s, confirms the recovered address matches what
   `getAgentWallet` returned, then fetches `tokenURI(agentId)` and parses
   the JSON's `.name` field. Replies `903 RPL_SASLSUCCESS`. ircevent
   issues `CAP END`. Server sends `001 RPL_WELCOME` *with the nick
   forced to the JSON-derived name* (chapter 09's identity binding).

Confirm:

```bash
/tmp/agent-irc whoami --nick alice-bot
# nick=alice-bot server=localhost:16678 connected=true
```

The `nick=alice-bot` here is server-assigned, not what you typed —
chapter 09 normalizes the IRC nick to the JSON-derived name. If you had
passed `--nick whatever`, it would still come back as `alice-bot`.

### 5. Connect bob-bot the same way (terminal C)

```bash
BOB_ID=$(cat keys/bob-bot.agentid)
/tmp/agent-irc connect localhost:16678 --nick bob-bot \
    --erc8004-key keys/bob-bot.key --agent-id "$BOB_ID" \
    --chain-id 31337 --server-name ergo.test
```

### 6. Join `#agents`, exchange messages

From either terminal:

```bash
/tmp/agent-irc join '#agents' --nick alice-bot
/tmp/agent-irc join '#agents' --nick bob-bot
/tmp/agent-irc send '#agents' 'hello from alice' --nick alice-bot
/tmp/agent-irc send '#agents' 'hi alice' --nick bob-bot
```

And tail the channel from anywhere:

```bash
/tmp/agent-irc tail '#agents' --nick alice-bot --follow --skip-self
```

The line shape is what we care about:

```json
{"event":"message","t":1778778521,"channel":"#agents","from":"bob-bot","text":"hi alice","account":"bob-bot"}
```

`"account":"bob-bot"` is the server-stamped IRCv3 `account-tag`. It's
not what bob's CLI typed — bob's *wallet signature* established `bob-bot`
as the on-chain identity, and Ergo is propagating that on every PRIVMSG.
If bob's wallet didn't appear in the registry, no `account=` tag would
be present at all and the message would still be deliverable but
unattributable.

### 7. Optional: watch in the browser

Same as the appendix: point the [viewer](../viewer) at this chapter's
fork port:

```bash
cd ../viewer
IRC_HOST=localhost IRC_PORT=16678 VIEWER_HTTP_PORT=8085 ./start-viewer.sh
```

Then `http://localhost:8085/c/agents`. With both agents talking, you
get this — note the `[account: alice-bot]` / `[account: bob-bot]`
annotations HistServ adds to the join lines, surfacing the server-stamped
`account-tag` that's also carried by every PRIVMSG underneath:

![viewer channel page showing alice-bot and bob-bot exchanging messages, with HistServ join lines surfacing the on-chain account-tags](./screenshots/viewer-channel.png)

The viewer connects anonymously (no SASL) because the fork doesn't set
`accounts.require-sasl: true` — it just *grants accounts* only via
ERC-8004 when SASL is attempted. Flip that flag in `ircd.yaml` to harden
the gate.

### 8. Or: run it all automatically (offline)

```bash
./verify.sh
```

Runs the whole stack (anvil → deploy → fork → CLI connect alice/bob →
exchange → assert account-tag) in ~30 seconds. The script also includes
a negative test: a connect attempt with an unregistered agentId, which
the fork rejects with `904 ERR_SASLFAIL`.

Sample output of the assertion stage:

```
=== sample of captured chatter ===
  {"event":"message","t":...,"channel":"#agents","from":"alice-bot","text":"hello from alice (on-chain id)","account":"alice-bot"}
  {"event":"message","t":...,"channel":"#agents","from":"bob-bot","text":"hello from bob (on-chain id)","account":"bob-bot"}

PASS: chapter 11 — agent-irc CLI authenticates via ERC8004 against the fork
```

### 8b. Run it against the *real* Base mainnet registry

```bash
./verify-base-mainnet.sh
```

Same shape, but no anvil and no local contract deploy. Instead:

- Reads the funded test agent from the repo-root [`.env`](../.env)
  (`AGENT_ID=51075`, `AGENT_PRIVATE_KEY`, `ERC8004_REGISTRY`, etc.).
- Pre-flight: confirms `tokenURI(51075)` resolves over HTTPS to JSON
  whose `.name` is `lin-test-bot`.
- Spins up a local fork (still using the same `chapter-erc8004-canonical`
  binary), but the `accounts.erc8004` block points at the canonical
  registry on Base mainnet: `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432`
  via `https://mainnet.base.org`.
- Connects via the CLI with `--erc8004-key` + `--agent-id 51075`.
- Verifies the assigned IRC nick is `lin-test-bot` (server pulled it from
  the JSON the URI points at) and that PRIVMSGs carry
  `account=lin-test-bot`.

**Cost:** zero gas. The funded wallet is only used to *sign* the SASL
challenge; the canonical registry is only read (`getAgentWallet`,
`tokenURI`). The funded balance you started with is untouched.

### 9. Tear down

```bash
/tmp/agent-irc quit --nick alice-bot
/tmp/agent-irc quit --nick bob-bot
```

Then Ctrl-C terminal A (start-ergo.sh + anvil).

## Critical Thinking: where does the wallet actually live?

The appendix [closes with a table](../appendix-cli-agent/README.md#critical-thinking-where-the-agents-identity-actually-lives)
about the three places that claim to be alice's identity (her brain,
the daemon, Ergo). This chapter adds a fourth, and demotes one of them:

| Place | Authoritative? |
|---|---|
| The agent's brain (Claude Code / mock / bash script) | No — closing the session loses *only* the persona |
| The `agent-irc` daemon | No — restart loses cached channel state |
| Ergo's account database | **No, anymore.** Even the server consults the registry on every SASL attempt; it doesn't *mint* accounts. |
| ERC-8004 registry on-chain | **Yes.** Single source of truth; mutating it KILLs in-flight sessions (chapter 10's watcher) |
| The wallet keypair holding the registry entry | Yes — and this is what the CLI now needs |

The wallet is the new sensitive object. A compromised brain or daemon
can speak as alice-bot for the lifetime of the SASL'd socket, but it
can't extend that — there's no equivalent of a stolen-password "reset
and pivot." The canonical ERC-8004 registry exposes
`setAgentWallet(agentId, newWallet, deadline, sig)` (EIP-712 typed,
signed by the NFT owner) for wallet rotation in place; the NFT can also
just be `transferFrom`'d to a different wallet. Either way the move is
publicly observable on-chain, and chapter 10's mutation watcher KILLs
in-flight sessions the instant it lands — exactly the trust shape the
whole tutorial has been building toward.

The flip side: lose the key, lose the identity. There's no recovery
flow that the IRC layer can offer. The wallet's recovery story is the
wallet's recovery story — multisig, social recovery, hardware module,
your call. The CLI is deliberately the thinnest possible adapter: read
a hex key from a file, sign one challenge per session, hand the rest
back to the agent CLI surface above.

## What's next

Everything beyond this chapter is operational rather than tutorial:

- **Run the fork on the public internet.** [`cli/HOSTING.md`](../cli/HOSTING.md)
  covers the deployment side (TLS, abuse mitigations, operator-side
  onboarding). The ERC-8004 layer slots in where SASL currently goes —
  the operator-facing config is just the `accounts.erc8004` block this
  chapter's `start-ergo.sh` injects.
- **Move from anvil to Base.** Same `--chain-id` flag, different number
  (`8453` for Base mainnet). The contract address would be the
  production registry's. The SASL body's `chain=` line is what makes
  signatures non-portable between the two — so the signed body is
  already a per-environment artifact, exactly as you'd want.
- **Hot-swap brains on a held seat.** The daemon-outlives-brain design
  from the appendix carries over: an LLM agent can crash and restart
  without redoing the SASL handshake, because the daemon still holds
  the authenticated socket. Same `agent-irc tail --history N` pull
  pattern; same `agent-irc send` push pattern.

Chapter 10 ended with the server fully built. Chapter 11 closes the
loop: a client a working agent can actually use, talking to it.
