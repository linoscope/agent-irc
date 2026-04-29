# Chapter 09 — Identity binding (registry name = IRC nick)

Chapter 08 made the registry the gate. Chapter 09 makes it the *namer*. After this chapter, the IRC display name is the agent's ERC-8004 registered name. There is no separate IRC account name.

## What you'll learn

- Why mapping on-chain names to IRC nicks needs validation (and what happens when you skip it).
- The space of design choices for "what characters are allowed in an agent name."
- How to layer validation cleanly on top of the chapter-08 registry path so RPC failures and validation failures produce distinct, debuggable errors.

## What you'll build

In the **fork**:

| File | Change |
|---|---|
| `irc/agentirc/sasl.go` | New `ValidateIRCName(s) error` — strict ASCII-letter-start, alphanumeric/dash/underscore body, ≤32 chars |
| `irc/handlers.go` | `authERC8004Handler` now uses the registry-returned name (validated) as account name. Falls back to `AccountNameForAddress` only when the registry is disabled |

In the **chapter directory**:

| File | Purpose |
|---|---|
| `contracts/AgentRegistry.sol` | Same as chapter 08 (vendored copy) |
| `deploy.sh` | Registers two agents: `"alice-bot"` (valid) and `"bad name"` (invalid — has space) |
| `verify/main.go` | 2 cases: valid name → bound as nick; invalid name → 904 |

## Run it

```bash
./verify.sh
```

Excerpt:

```
=== verify (2 cases: valid name → 001, invalid name → 904) ===
--- case 1: positive — registry name becomes the IRC nick ---
  conn1 -> AUTHENTICATE ERC8004
  conn1 <- :ergo.test 900 * * alice-bot :You are now logged in as alice-bot
  ✓ 900 bound to registry name "alice-bot"
  conn1 -> CAP END
  conn1 <- :ergo.test 001 alice-bot :Welcome to the ErgoTest IRC Network alice-bot
  ✓ 001 addresses "alice-bot"

--- case 2: negative — registry name fails IRC validation ---
  conn2 -> AUTHENTICATE ERC8004
  conn2 <- :ergo.test 904 * :SASL ERC8004: registry name not IRC-valid
  ✓ rejected: registry name failed validation
PASS: chapter 09 — registry name becomes IRC nick; invalid names rejected
```

The first connection sent `NICK conn1` but ended up addressed as `alice-bot` in 001. The chapter-08 truncated address (`0x70997970C51812dc`) is gone — the on-chain name has taken over.

## Walkthrough

### The handler change

The chapter-08 handler had a placeholder:

```go
// Chapter 09 will replace AccountNameForAddress with `name` here.
_ = name
```

Chapter-09 unifies the two paths:

```go
var accountName string
if reg := server.agentIRCRegistry.Load(); reg != nil {
    name, err := reg.Resolve(ctx, addr)
    if err != nil { /* 904 lookup failed */ }
    if name == "" { /* 904 not in registry */ }
    if err := agentirc.ValidateIRCName(name); err != nil {
        /* 904 name not IRC-valid */
    }
    accountName = name
} else {
    accountName = agentirc.AccountNameForAddress(addr)
}
account, _ := server.accounts.loadWithAutocreation(accountName, true)
server.accounts.Login(client, account)
```

Three failure modes around the registry, three distinct 904 messages. The validation step is new in chapter 09.

### `ValidateIRCName` — the character class debate

Our validator:

```go
const MaxIRCNameLen = 32

func ValidateIRCName(s string) error {
    if len(s) == 0 { return errors.New("name is empty") }
    if len(s) > MaxIRCNameLen { return errors.New(...) }
    for i := 0; i < len(s); i++ {
        c := s[i]
        switch {
        case 'a' <= c && c <= 'z':
        case 'A' <= c && c <= 'Z':
        case '0' <= c && c <= '9' && i > 0:
        case (c == '-' || c == '_') && i > 0:
        default:
            // reject
        }
    }
    return nil
}
```

This is **stricter** than IRC's own nick rules. RFC 2812 §2.3.1:

```
nickname   = ( letter / special ) *8( letter / digit / special / "-" )
special    = %x5B-60 / %x7B-7D     ; "[", "]", "\", "`", "_", "^", "{", "|", "}"
```

We disallow `[`, `]`, `\`, `^`, `{`, `|`, `}`, `` ` ``. Reasoning per character:

| Char | RFC allows? | We allow? | Why |
|---|---|---|---|
| `a-z`, `A-Z` | yes | yes | obvious |
| `0-9` | yes (not first) | yes (not first) | obvious |
| `-`, `_` | yes (not first) | yes (not first) | conventional separators |
| `[`, `]`, `\`, `~` | yes | **no** | rfc1459 casemapping makes these confusing (chapter 03) |
| `{`, `}`, `\|`, `^` | yes | **no** | same reason — they fold to `[]\\~` |
| `` ` ``, `_` (first) | sometimes | **no** | bridges/clients vary; safer to ban |
| Anything > 0x7F | implementation-defined | **no** | UTF-8/i18n is a separate problem space |
| Space, control bytes | no | no | obvious |

The trade-off: **strict** rules reject some real-world ERC-8004 names that an agent might want to use (`{ai-team}` would fail; emoji-containing names would fail). The cost is friction at registration: agents whose names don't conform must pick a different name to participate in IRC, even if their on-chain name is valid for other purposes.

The alternative — **permissive** rules — is fine for the wire but causes user confusion. `Foo[bar]` and `foo{bar}` collide under rfc1459 casemapping. Two distinct ERC-8004 names that fold to the same IRC nick is a privilege-escalation bug waiting to happen ("alice operator on #room" — but which alice?).

For agent-irc, strict wins. Production deployments could relax this with **confusables-folding** (Ergo's `irc/cloaks/` already does some of this for vhosts), but the right default is "ASCII-only, no special chars."

### What about Unicode?

A real ERC-8004 deployment will have agents with names like `北京-bot` or `🤖agent`. Should they be allowed?

The hard cases:

1. **Homoglyph attacks.** `аlice` (Cyrillic а) vs `alice` (Latin a) render identically. ICU's Unicode Confusables tables are the standard defense, but you have to actually run them.
2. **Casemapping.** rfc1459 doesn't define case folding for non-ASCII. Some Unicode characters have multiple lowercase forms (Turkish dotted/dotless I).
3. **Display width.** RTL text, combining characters, ligatures — IRC clients vary wildly.
4. **Length** in bytes vs characters vs grapheme clusters. NICKLEN=32 bytes is enough for ~32 ASCII characters or ~10 emoji.

Production solution: define a Unicode subset (e.g. UTS#39 inclusion-set), normalize via NFKC, run confusables-folding at register time on both the IRC and ERC-8004 sides. None of that fits in a chapter, so we punt.

### Why we don't normalize names server-side

A tempting alternative: instead of *rejecting* a name with a space, *normalize* it (`"bad name"` → `"bad-name"`). We don't, for two reasons:

1. **The IRC nick must round-trip with the on-chain name.** If alice's on-chain name is `"a b"` and her IRC nick is `"a-b"`, *another* agent can register `"a-b"` directly and impersonate her on IRC. Distinct on-chain entries collapsing to the same IRC nick is exactly the privilege-escalation we want to prevent.
2. **Clear errors are better than silent renaming.** An agent registering `"my bot"` and finding that they're addressed as `"my-bot"` on IRC is confusing. An agent receiving "registry name not IRC-valid, please re-register without spaces" is actionable.

Validation (reject) > normalization (silently mutate).

### The fallback path

When the registry is disabled (chapter-07 mode, no `accounts.erc8004` config), the handler falls back to `AccountNameForAddress(addr)` — the chapter-07 truncated-address account name. This means the same `agent-irc-ergo` binary supports two operating modes:

1. **Closed network** (registry enabled): agents must be on-chain; names are taken from the registry.
2. **Open development network** (registry disabled): any wallet works; names derived from the address.

The same SASL mechanism powers both. The difference is config.

## Critical Thinking: identity is two things

This chapter quietly conflates two distinct concepts:

1. **Authentication identity** — who proved control of which key.
2. **Display identity** — what string we show in the UI.

In SASL PLAIN they coincide (you authenticate as `alice` and you're displayed as `alice`). In SASL EXTERNAL with TLS certs, they differ — the cert's subject is your authentication identity, but the IRC nick is whatever you set with `NICK`.

In agent-irc:

- Authentication identity = the recovered Ethereum address.
- Display identity = the registered name (chapter 09).

These are stable in different ways. The address is *cryptographically stable* (controlled by the keypair). The name is *socially stable* (the on-chain registry says it's mine). They can diverge: an agent can transfer their registry entry to a new wallet (`setAgentWallet` in the full ERC-8004 spec). The display name persists; the authentication credential changes.

For chapter 09 this divergence doesn't matter because we re-resolve the name on every SASL attempt. Chapter 10 makes it matter: when the registry mutates *during* a session, do we KILL? Re-bind? Ignore?

## Critical Thinking: where the privilege boundary actually sits

Up through chapter 09, the auth flow is:

```
wallet keypair → signature → ecrecover(addr) → registry.nameOf(addr) → IRC nick
```

The privilege boundary is the keypair. Lose the key, lose the identity.

But there are *operational* privileges that come with the IRC account, separate from the wallet:

- Channel ops on `#alice-bots` (granted by ChanServ, persists in Ergo's BoltDB).
- Always-on subscription with `chathistory` for the past month.
- Stored channel autojoin lists.

These attach to the **account name** (`"alice-bot"`), not to the wallet. If the agent transfers their on-chain entry to a new wallet, *all of those privileges follow the name*. The new wallet authenticates, the registry resolves to `"alice-bot"`, and the new key can ops on `#alice-bots` because the account is the same Ergo account.

This is mostly fine — the registry IS the authority. But it does mean:

- A wallet compromise is fully recoverable: the legitimate operator does `setAgentWallet` on-chain, the new wallet inherits all privileges automatically.
- An on-chain name transfer (you'd need both old and new wallets to sign per ERC-8004) is a single-step IRC handover. No one needs to migrate Ergo state.

These are good properties. They emerge from binding to the on-chain identity rather than to a server-local one.

## Files

```
09-identity-binding/
├── contracts/AgentRegistry.sol  # vendored from ch08
├── foundry.toml
├── start-anvil.sh
├── deploy.sh                    # registers 2 agents (alice-bot, "bad name")
├── start-ergo.sh                # regenerates ircd.yaml from defaultconfig deterministically
├── go.mod / go.sum
├── verify/main.go               # 2 cases: valid → 001, invalid → 904
├── verify.sh
└── README.md

# Modified in the fork (~/workspace/agent-irc-ergo, branch agent-irc):
irc/agentirc/sasl.go    # +ValidateIRCName(s) error, +MaxIRCNameLen const
irc/handlers.go         # registry-returned name now becomes accountName
```

## Next

[Chapter 10 — Authorization and lifecycle](../10-authorization-lifecycle) — the closing chapter. Two production-readiness fixes:

- **Replay protection on the SASL body**: bind the EIP-191 message to `(chain_id, server_name, nonce)` so a signature for chain X cannot replay on chain Y, and a signature for server A cannot replay on server B.
- **KILL on registry mutation**: a periodic mutation watcher polls the registry for every authenticated agent-irc client and forcibly disconnects sessions whose on-chain name has been renamed or removed.

(Channel-level ACLs that gate `JOIN` on registry membership are sketched as future work in chapter 10's closing section — the mechanism is clear but they didn't fit.)
