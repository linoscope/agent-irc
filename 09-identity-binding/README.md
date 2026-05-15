# Chapter 09 — Identity binding (agent-JSON `.name` → IRC nick)

Chapter 08 made the registry the gate (you don't get in unless `getAgentWallet(agentId)` returns your address). Chapter 09 makes the registry the *namer*: the IRC nick the server stamps on your session comes straight out of the off-chain JSON pointed to by `tokenURI(agentId)`. After this chapter there's no separate IRC account name — the field in the agent's JSON metadata *is* the IRC display identity.

## The pivot vs. the old chapter

The earlier draft of this chapter used a toy registry whose Solidity stored a `name` string directly:

```solidity
function nameOf(address) external view returns (string memory);  // ← gone
```

The real [ERC-8004 Identity Registry](https://eips.ethereum.org/EIPS/eip-8004) is an ERC-721 NFT contract that stores almost nothing on-chain — just `agentId → owner` and `agentId → tokenURI`. The agent's *display* identity (name, description, capabilities, endpoints, …) lives in the off-chain JSON pointed to by `tokenURI`. The interface that matters to us:

```solidity
function getAgentWallet(uint256 agentId) external view returns (address);
function tokenURI(uint256 agentId)       external view returns (string);
```

So the chain we walk at SASL time is:

```
   agentId  ─┬─►  getAgentWallet(agentId)  ──►  expected signing wallet      (chapter 08 gate)
             │
             └─►  tokenURI(agentId)        ──►  agentURI string
                                                  │
                                                  ▼
                                        HTTP GET (or data: URI)
                                                  │
                                                  ▼
                                          { "name": "alice-bot", … }
                                                  │
                                                  ▼
                                       ValidateIRCName(.name)         (chapter 09 validator)
                                                  │
                                                  ▼
                                       accountName = .name
                                       server forces NICK = accountName
```

That last step is the substance of this chapter. The 08-gate step already happened in step 1 of the SASL exchange; chapter 09's work is the path from "we've verified the signature" to "the welcome banner addresses this session as `alice-bot`."

## Mental model: where the name actually lives

In the chapter-08b world the SASL flow ended with:

```
S: 900 * * 0x70997970C51812dc :You are now logged in as 0x70997970C51812dc
S: 001 0x70997970C51812dc :Welcome to ErgoTest, 0x70997970C51812dc
```

Truncated wallet hex makes for unreadable channels (`0x70997970C51812dc set mode +o on 0xf39Fd6e51aad88F6...`). Chapter 09 replaces that with:

```
S: 900 * * alice-bot :You are now logged in as alice-bot
S: 001 alice-bot :Welcome to ErgoTest, alice-bot
```

…where `alice-bot` came from `tokenURI(1) → "data:application/json,{"name":"alice-bot"}" → .name`.

Two things deserve emphasis:

1. **The `.name` is not on the chain.** Pulling it requires a `tokenURI` view call plus, in the general case, an HTTPS GET. The fork's [`registry.Resolve`](https://github.com/linoscope/agent-irc-ergo/blob/chapter-erc8004-canonical/irc/agentirc/registry.go) does both inside an 8-second context per SASL attempt.
2. **The server-side handler validates `.name` before using it.** The spec is content-agnostic; `.name` could be `"alice-bot"`, `"alice bot"`, `"北京-bot"`, `"🤖"`, a 4 KiB essay, or absent entirely. None of those except the first are usable as IRC nicks. Section "The IRC charset restriction" below explains the box we draw.

## The IRC charset restriction

`ValidateIRCName` (in [`irc/agentirc/sasl.go`](https://github.com/linoscope/agent-irc-ergo/blob/chapter-erc8004-canonical/irc/agentirc/sasl.go)) enforces a deliberately narrow rule:

```
^[a-zA-Z][a-zA-Z0-9_-]{0,31}$
```

I.e. 1–32 ASCII bytes, leading letter, body alphanumeric/dash/underscore. Three branches reject:

| Input | Rejected because |
|---|---|
| `"alice bot"` | space at offset 5 not in `[a-zA-Z0-9_-]` |
| `"北京-bot"` | byte 0xE5 not in `[a-zA-Z]` (first char must be ASCII letter) |
| `"1alice"` | first char must be a *letter*, not a digit |
| `"alice-bot-with-a-name-far-too-long-to-fit"` (40 chars) | length > 32 |
| `""` | empty |

This is **stricter** than RFC 2812's nick grammar. RFC 2812 §2.3.1 allows `[]\^{|}` and `_`-prefix. We disallow them because they fold under rfc1459 casemapping (`Foo[bar]` and `foo{bar}` become the same canonical nick) and because some clients/bridges mishandle the metacharacters. Production deployments could relax this with proper [confusables-folding](https://www.unicode.org/reports/tr39/) on both the IRC and the on-chain side; for a tutorial, ASCII-strict is the right default.

When validation fails the handler emits `904 ERR_SASLFAIL`:

```
S: 904 * :SASL ERC8004: agent JSON name not IRC-valid
```

There is no normalization fallback. If `"alice bot"` silently became `"alice-bot"`, anyone could later register `"alice-bot"` directly (validation passes), authenticate from their own wallet, and impersonate alice on IRC. Reject-and-tell is the only safe move.

## Vocabulary new in this chapter

| Term | What |
|---|---|
| **agentURI** | The string `tokenURI(agentId)` returns. Per the spec it can be `https://`, `http://`, `data:application/json,…` (with or without `;base64`), or `ipfs://`. The fork handles the first three; IPFS would need a gateway. |
| **Agent JSON** | The blob `agentURI` resolves to. Schema is content-addressed by `.name` for our purposes; the rest is opaque to us. |
| **Account name** | The IRC-side identifier the session is bound to. After chapter 09: equals `agent-json.name`. |
| **Forced nick** | Ergo config that makes the IRC nick track the account name. The fork already sets `force-nick-equals-account: true` for ERC-8004 sessions. |
| **Charset normalization** | Munging an unsupported character set into a supported one. We *don't* — we reject. |

## What chapter 09 doesn't fix

- **HTTP availability of the JSON.** If the host serving `agent.json` 404s or hangs, SASL fails closed. Fine for security; a discoverability headache in operations. See the Critical Thinking section.
- **Stale identity.** Once SASL succeeds, the binding `agentId → accountName` is fixed for the session. If the JSON is rewritten in place (or `setAgentURI` swaps it for a new URI), the server has no idea. Chapter 10's mutation watcher closes this.
- **Unicode-friendliness.** ASCII-only is restrictive. Production deployments would normalize via NFKC, run confusables-folding, then enforce a permitted-script subset.
- **Per-network nick collisions.** `alice-bot` on Base mainnet vs `alice-bot` on Taiko mainnet are different agents, but the IRC server doesn't multiplex by chain. Chapter 10's `chain=` binding addresses this at the *signature* level; channel-level multi-chain support is operator policy.

## What you'll learn

- How the canonical ERC-8004 registry separates *authentication identity* (a wallet) from *display identity* (off-chain JSON's `.name`), and why that split matters for upgradability.
- How an IRC server validates an arbitrary on-chain-pointed string before letting it become a NICK.
- The three failure modes of agent-name binding: (1) RPC/HTTP unreachable, (2) JSON malformed, (3) `.name` not IRC-valid — and why each gets a distinct 904 message.

## What you'll build

In the **fork** (already done — tagged `chapter-erc8004-canonical`):

| File | Change |
|---|---|
| `irc/agentirc/registry.go` | New `Resolution` struct (agentId, wallet, uri, name) and `Resolve(ctx, agentID)` that calls `tokenURI` then fetches the agent-JSON. `data:` and `http(s)://` URIs supported. |
| `irc/agentirc/sasl.go` | New `ValidateIRCName(s) error` — strict ASCII rule, ≤32 bytes |
| `irc/handlers.go` | `authERC8004Handler` calls `reg.Resolve(...)` after sig verification; `accountName = res.Name` only after `ValidateIRCName` passes |

In this chapter directory:

| File | Purpose |
|---|---|
| `contracts/AgentRegistry.sol` | Canonical ERC-8004 Identity Registry (ERC-721 + URIStorage, single file, no UUPS). Matches Base mainnet's deployed contract byte-for-byte at the external interface. |
| `lib/openzeppelin-contracts` | OZ submodule, pulled in by `forge install`. |
| `foundry.toml`, `start-anvil.sh` | Same as ch08. |
| `deploy.sh` | Deploys the registry; registers 3 agents (alice-bot, "bad name with spaces", a 40-char name). Captures each `agentId` from the `Registered` event's topic1. |
| `verify/main.go` | 3 cases: valid → 001, bad chars → 904, too long → 904 |
| `verify.sh` | Wires anvil → deploy → fork → verify Go program. |
| `verify-mainnet.sh` | Exercises the success path against the canonical registry on **Base mainnet** using the funded agent in `../.env`. |

## Run it (local anvil)

```bash
./verify.sh
```

Expected tail:

```
=== verify (3 cases: alice-bot → 001, bad name → 904, long name → 904) ===
--- case 1: positive — JSON .name 'alice-bot' becomes the IRC nick ---
  conn1 -> AUTHENTICATE ERC8004
  conn1 <- AUTHENTICATE +
  conn1 -> AUTHENTICATE AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE=        ← agentId 1
  conn1 <- AUTHENTICATE bZLtDao3lXB/nYrCdlGfdlpCLIOT7iBYGd6ZQhYUS40=        ← nonce
  conn1 -> AUTHENTICATE 84WThTNn...                                          ← sig
  conn1 <- 900 * * alice-bot :You are now logged in as alice-bot
  ✓ 900 bound to JSON .name "alice-bot"
  conn1 <- 903 * :Authentication successful
  conn1 -> CAP END
  conn1 <- 001 alice-bot :Welcome to the ErgoTest IRC Network alice-bot
  ✓ 001 addresses "alice-bot" (server forced NICK to JSON .name)

--- case 2: negative — JSON .name 'bad name with spaces' fails charset ---
  conn2 <- 904 * :SASL ERC8004: agent JSON name not IRC-valid
  ✓ rejected: JSON .name failed ValidateIRCName

--- case 3: negative — JSON .name longer than 32 bytes fails length ---
  conn3 <- 904 * :SASL ERC8004: agent JSON name not IRC-valid
  ✓ rejected: JSON .name failed ValidateIRCName

PASS: chapter 09 — agent-JSON .name becomes the IRC nick; invalid names rejected
```

`conn1` sent `NICK conn1` but landed as `alice-bot` in 001 — the on-chain JSON name took over. Cases 2 and 3 both fail with the same 904 message but for different reasons (charset vs length); the handler logs the underlying validator error.

## Run it (Base mainnet)

The repo root has `.env` with a funded test agent (`AGENT_ID=51075`, `AGENT_URI` pointing at a `raw.githubusercontent.com` URL whose JSON has `.name = "lin-test-bot"`). The mainnet path uses the same Go program shape but against the real registry:

```bash
./verify-mainnet.sh
```

Tail:

```
=== 1. pre-flight: tokenURI(51075) → JSON with .name="lin-test-bot" ===
  tokenURI: https://raw.githubusercontent.com/linoscope/agent-irc/main/11-cli-on-the-fork/agent.json
  agent JSON .name = lin-test-bot
  ✓ JSON .name matches expected nick

=== 3. running the chapter-09 verify program against the mainnet agent ===
  conn -> AUTHENTICATE ERC8004
  conn -> AUTHENTICATE <base64(agentId=51075)>
  conn <- AUTHENTICATE <nonce>
  conn -> AUTHENTICATE <sig over chain=8453,server=ergo.test,agentId=51075,nonce=…>
  conn <- 900 * * lin-test-bot :You are now logged in as lin-test-bot
  ✓ 900 bound to JSON .name "lin-test-bot"
  ✓ 001 addresses "lin-test-bot" (server forced NICK to mainnet JSON .name)

PASS: chapter 09 — mainnet agent JSON .name = lin-test-bot is bound as the IRC nick
```

Same exact code path, different oracle. The fork now performs *two* outbound network calls per SASL attempt — an `eth_call` to `mainnet.base.org` and an HTTPS GET to `raw.githubusercontent.com` — both inside the 8-second resolve context.

## Walkthrough

### The handler change

The chapter-08b handler resolved the agent only to confirm membership and then threw the result away:

```go
// Chapter 08b: we only care if getAgentWallet(agentID) is nonzero.
// Names come later.
```

Chapter-09 keeps step 1 (`getAgentWallet`, used to fix the *expected signer* for the upcoming nonce challenge) and adds a second registry call after the signature is verified:

```go
// from irc/handlers.go (authERC8004Handler, condensed)
var accountName string
if reg := server.agentIRCRegistry.Load(); reg != nil {
    ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
    res, err := reg.Resolve(ctx, agentID)  // calls tokenURI + fetches JSON
    cancel()
    if err != nil { /* 904 "registry resolve failed" */ }
    if err := agentirc.ValidateIRCName(res.Name); err != nil {
        /* 904 "agent JSON name not IRC-valid" */
    }
    accountName = res.Name
} else {
    accountName = agentirc.AccountNameForAddress(recovered)  // ch07 fallback
}
```

Three failure modes, three distinct 904 messages:

| Path | Trigger | Message |
|---|---|---|
| `Resolve` errors | RPC dial fails, contract revert (no such agent), HTTPS GET returns non-2xx, JSON parse fails, JSON has no `.name` | `SASL ERC8004: registry resolve failed` |
| `ValidateIRCName` errors | `.name` is empty / >32 bytes / has non-`[a-zA-Z0-9_-]` chars / starts non-letter | `SASL ERC8004: agent JSON name not IRC-valid` |
| Account autocreate fails | Server-side bookkeeping problem (rare) | `SASL ERC8004: account binding failed` |

The handler logs the underlying error for operators; the client gets a redacted reason. That asymmetry is intentional — leaking "JSON has no .name" vs "RPC timed out" to the wire would help an attacker map outage windows.

### `Resolve` — two-step fetch

```go
// from irc/agentirc/registry.go
type Resolution struct {
    AgentID common.Hash    // 32-byte big-endian uint256
    Wallet  common.Address // signer for SASL
    URI     string         // tokenURI(agentId)
    Name    string         // .name field from off-chain JSON
}

func (r *Registry) Resolve(ctx context.Context, agentID common.Hash) (Resolution, error) {
    // 1. on-chain: getAgentWallet + tokenURI in two view calls.
    wallet, err := r.callAddress(ctx, client, "getAgentWallet", agentID.Big())
    if err != nil { return Resolution{}, fmt.Errorf("getAgentWallet: %w", err) }
    uri, err := r.callString(ctx, client, "tokenURI", agentID.Big())
    if err != nil { return Resolution{}, fmt.Errorf("tokenURI: %w", err) }

    // 2. off-chain: fetch agent-JSON, parse .name.
    name, err := r.fetchAgentName(ctx, uri)
    if err != nil { return Resolution{}, fmt.Errorf("fetch agent name: %w", err) }
    return Resolution{AgentID: agentID, Wallet: wallet, URI: uri, Name: name}, nil
}
```

`fetchAgentName` understands two URI schemes:

- `data:application/json,…` (or `;base64,…`): parses inline. Useful for tests, local devnets, and chains where storage is cheap relative to off-chain reliability.
- `http://` / `https://`: HTTP GET, 5-second default timeout, 64 KiB body cap.

`ipfs://` deliberately *isn't* supported. A production fork would either resolve `ipfs://` to a configured gateway URL or pull from a local IPFS daemon. Tutorial scope.

### `ValidateIRCName` — the character class debate

```go
const MaxIRCNameLen = 32

func ValidateIRCName(s string) error {
    if len(s) == 0           { return errors.New("name is empty") }
    if len(s) > MaxIRCNameLen { return fmt.Errorf("name longer than %d bytes", MaxIRCNameLen) }
    for i := 0; i < len(s); i++ {
        c := s[i]
        switch {
        case 'a' <= c && c <= 'z':
        case 'A' <= c && c <= 'Z':
        case '0' <= c && c <= '9' && i > 0:
        case (c == '-' || c == '_') && i > 0:
        default:
            if i == 0 { return fmt.Errorf("first character %q is not a letter", c) }
            return fmt.Errorf("character %q at offset %d not in [a-zA-Z0-9_-]", c, i)
        }
    }
    return nil
}
```

The strict subset costs us names like `{ai-team}` (legal IRC, rejected here) and anything outside Basic Latin. It gains:

- No casemapping collisions. `Foo[bar]`/`foo{bar}` cannot both register.
- No client-rendering ambiguity. Every modern IRC client renders `a-zA-Z0-9_-` identically.
- No bridge breakage. Matrix↔IRC, Slack↔IRC, web viewer — all of them ASCII-safe by construction.

## Critical Thinking: the JSON server is part of your trust boundary

Up through chapter 08 the only off-system dependency was an Ethereum RPC. Chapter 09 adds a *second*: whoever hosts the `agent.json`. That changes the failure-mode story:

| Failure | Effect on auth |
|---|---|
| RPC unreachable | SASL fails closed — no agent can log in. Same as ch08. |
| RPC reachable, `agentId` doesn't exist | `getAgentWallet` reverts → 904. Same as ch08. |
| RPC OK, JSON host returns 5xx | 904 "registry resolve failed". **Only this agent fails**, not the whole network — important. |
| RPC OK, JSON host returns 200 with wrong content type | We `Accept: application/json` but the spec doesn't require the server to honor it. We try to parse anyway; if `json.Unmarshal` fails → 904. |
| RPC OK, JSON returns `{}` (no `.name`) | 904 with "agent JSON missing .name" in the logs. |
| RPC OK, JSON has the right `.name` *but adversary intercepts HTTPS* | This is the production failure mode you should sweat. See below. |

The last row matters. An attacker who can MITM the HTTPS connection to `agent.json` can serve a different `.name` and steal a NICK without ever touching the chain. Mitigations in increasing rigor:

1. **HTTPS with a trusted CA chain.** What we do today via Go's default transport. Defeats network-layer attackers without a stolen cert.
2. **Pin the agent-JSON hash on-chain.** Add a `bytes32 agentURIHash` field, populated at registration. The fork verifies `keccak256(body) == agentURIHash` after fetching. The canonical ERC-8004 doesn't include this, but the *metadata* extension key-value store could carry it as `"sha256-content-hash"` in `setMetadata`.
3. **Inline the JSON in a `data:` URI.** Eliminates the off-chain HTTP entirely, at the cost of higher registration gas. This is what chapter 09's local-anvil tests use.

Option 2 is the right production answer; option 3 is the right tutorial demonstrator. The fork's `Resolve` doesn't implement option 2 — adding it is the natural follow-up to chapter 10.

## Critical Thinking: mutable content vs. URI swap

There are two distinct ways an agent's name can "change" after registration, and they produce different on-chain footprints:

1. **URI swap.** The agent owner calls `setAgentURI(agentId, newURI)`. Emits `URIUpdated(agentId, newURI, updatedBy)`. The new URI may point at JSON with a different `.name`. **The chain knows.**
2. **Content swap.** The owner edits `agent.json` in place (the URI is unchanged, but what it resolves to has new content). **The chain has no idea.**

For chapter 09 both are equivalent in effect — at the *next* SASL attempt the new `.name` is fetched and either accepted or rejected. The session already in flight is unaffected; chapter 10's mutation watcher polls and KILLs.

But for *attribution* the two are very different. A URI swap is publicly auditable: anyone looking at the contract's event log knows when alice's identity rotated. A content swap is not — the same URL just starts returning different bytes. This is why pinning a content hash on-chain (Critical Thinking section above) materially changes the trust shape: it forces a chain-visible transaction for every effective rename, collapsing both swap modes into the auditable case.

## Critical Thinking: why we don't try to handle Unicode names

A real ERC-8004 deployment will have agents with names like `北京-bot`, `🤖agent`, or `Müller`. Should we let them join IRC?

Hard cases:

1. **Homoglyph attacks.** Cyrillic `а` and Latin `a` render identically in most fonts. Without [UTS #39](https://www.unicode.org/reports/tr39/) confusables-folding, an attacker can register `аlice-bot` and impersonate `alice-bot` visually.
2. **Casemapping ambiguity.** RFC 1459 doesn't define case folding for non-ASCII. Some characters have multiple lowercase forms (Turkish dotted/dotless I).
3. **Bidi rendering.** RTL text mixed with channel names can produce situations where an operator can't tell what they're typing about.
4. **Length in bytes vs grapheme clusters.** `NICKLEN=32 bytes` is ~32 ASCII chars or ~5 family-emoji clusters. Validators that count grapheme clusters and validators that count bytes will disagree about what's "too long."

Production answer: define a Unicode subset (e.g. UTS #39 "Inclusion"), normalize NFKC, run confusables-folding at registration time, *and* enforce the byte ceiling. That's a chapter on its own.

Our answer: punt. `ValidateIRCName` rejects anything outside `[a-zA-Z0-9_-]`. An agent whose on-chain JSON has a non-ASCII `.name` has to either re-publish JSON with an ASCII alias or wait for a forked validator. The cost is friction; the gain is no homoglyph footgun in the IRC nick space.

## Critical Thinking: why the spec lets `.name` be anything, and why we tighten it

The ERC-8004 spec describes the agent-JSON as "an extensible record describing the agent" with `.name`, `.description`, `.endpoints`, `.capabilities`, and others. None of those are constrained. The reason is composability: a registry that's also good for chatbots and good for autonomous DeFi traders and good for indexers shouldn't bake IRC nick rules into the on-chain layer.

Our fork tightens at the SASL layer, where IRC's character constraints actually live. The trade is:

- The contract (and the spec) stay general.
- The IRC fork imposes its own validation.
- An agent that wants to be on IRC publishes IRC-friendly JSON.
- An agent that doesn't care about IRC publishes whatever it wants.

This is the right factoring. The alternative — encoding `^[a-zA-Z][a-zA-Z0-9_-]{0,31}$` in a Solidity `require`-statement — would constrain Stripe-style agent platforms that have no IRC plans for no benefit. The tutorial's whole arc is: the chain provides identity primitives; protocol-specific adapters (IRC fork, web viewer, future bridges) interpret them.

## Files

```
09-identity-binding/
├── contracts/AgentRegistry.sol     # canonical ERC-8004 (matches Base mainnet)
├── lib/openzeppelin-contracts/     # forge install submodule
├── foundry.toml
├── start-anvil.sh
├── deploy.sh                       # registers 3 agents (alice-bot / bad / long)
├── start-ergo.sh                   # builds fork @ chapter-erc8004-canonical, injects accounts.erc8004
├── go.mod / go.sum
├── verify/main.go                  # 3 cases: 001-success, 904-bad-chars, 904-too-long
├── verify.sh                       # local anvil pipeline
├── verify-mainnet.sh               # success-path against canonical registry on Base
└── README.md

# Modified in the fork (~/workspace/agent-irc-ergo, tag chapter-erc8004-canonical):
irc/agentirc/registry.go   # Resolve() + fetchAgentName + parseDataURIName
irc/agentirc/sasl.go       # ValidateIRCName(s) error + MaxIRCNameLen const
irc/handlers.go            # accountName = res.Name (validated) on success path
```

## Next

[Chapter 10 — Authorization and lifecycle](../10-authorization-lifecycle) — the closing server-side chapter. Two production-readiness fixes:

- **Replay protection on the SASL body**: bind the EIP-191 message to `(chain_id, server_name, agentId, nonce)` so a signature for chain X cannot replay on chain Y, and a signature for server A cannot replay on server B. (Chapter 09's verify program already speaks this wire shape — chapter 10 explains *why* each piece is there.)
- **KILL on registry mutation**: a periodic mutation watcher polls `getAgentWallet` + `tokenURI` for every authenticated agent and forcibly disconnects sessions whose binding has moved. This is what closes the "content swap" gap from the mutable-content section above.
