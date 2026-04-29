# Chapter 07 — Custom SASL mechanism (off-chain)

The chapter where `agent-irc-ergo` becomes meaningfully different from upstream Ergo. We add a new SASL mechanism — `ERC8004` — that authenticates an IRC session by having the client sign a server-issued nonce with their Ethereum wallet keypair. No on-chain check yet (chapter 08 brings that); we just prove the signature path works end-to-end.

After this chapter, **wallet keypair = IRC identity**. No passwords, no certs. The wallet *is* the credential.

## Mental model: a wallet is a credential

Chapter 06 ran SASL PLAIN: client sends `\0username\0password`, server checks bcrypt, success. The credential is a *shared secret* the user picked, the server stored, and both sides can compare.

Chapter 07 swaps the credential type. Instead of a password, the credential is **the ability to produce a signature with a specific Ethereum private key**. The server never holds the secret — it can only check signatures.

| | SASL PLAIN | SASL ERC8004 (chapter 07) |
|---|---|---|
| Credential | password | wallet private key |
| What the client sends | `\0user\0password` | `address` then `signature(nonce)` |
| What the server stores | bcrypt hash of password | nothing; verifies via `ecrecover` |
| What server compromise leaks | every password | nothing — server can observe but not impersonate |
| Who picks the identity | user (during register) | the chain (each address is a fixed, derived identity) |
| Rotation | password reset | new wallet, on-chain `setAgentWallet` |

The mechanism's wire shape is the same standard SASL: `AUTHENTICATE ERC8004` → server `+`, client data, server response, client data, … → `903 RPL_SASLSUCCESS`. We just put different bytes in the data fields.

### Why a 3-step exchange (and not 2)

The simplest possible challenge-response is:

```
client signs nonce → sends sig
server verifies
```

But where does the nonce come from? If the *client* picks it, an attacker who once observed alice's signature can replay it forever. The nonce **must** be server-issued, which forces the protocol to be at least:

```
server: here's a nonce
client: signs nonce, sends sig
server: verifies
```

…and that's two messages from the server before the client can sign anything. Plus we need the client to tell the server *which address* they're claiming to be (so the server knows what to verify against). So the natural shape is three steps:

```
                       client                              server
                       ───                                  ───
                                                ─►  AUTHENTICATE +
   step 1: claim addr  ─►   AUTHENTICATE <b64(20-byte address)>
                                              [server stores addr]
                                              [server generates nonce]
                                                ─►  AUTHENTICATE <b64(32-byte nonce)>
   step 2: sign + send sig ─►  AUTHENTICATE <b64(65-byte sig)>
                                              [ecrecover(sig) ?= addr]
                                                ─►  900 RPL_LOGGEDIN
                                                ─►  903 RPL_SASLSUCCESS
```

This mirrors how SCRAM-SHA-256 also runs as a multi-step SASL exchange in Ergo (`scramConv` in `irc/handlers.go`). The dispatch machinery already supports multi-step flows — we just need to plug in our handler.

### What gets signed: the EIP-191 envelope

The client doesn't sign the raw nonce. They sign the keccak256 hash of:

```
\x19Ethereum Signed Message:\n<decimal length><body>
```

…where `body` is `agent-irc-sasl-v1\nnonce=<hex>`. This is **EIP-191 personal_sign**, the standard envelope every Ethereum wallet exposes (MetaMask, hardware wallets, Frame, etc.) for signing app-level messages.

Why bother:

1. **Wallets render it nicely.** When MetaMask sees an EIP-191 message, it shows the user a "Sign this message" dialog with the readable body. Without EIP-191, you'd be asking them to sign opaque hashes — which most wallets refuse for safety reasons.
2. **The `\x19` prefix is non-ASCII**, which guarantees the signed bytes can never collide with a valid Ethereum transaction's RLP encoding. So a SASL signature can't be replayed as an on-chain transaction. This is the primary security justification: it isolates the SASL credential from the wallet's transaction-signing authority.

EIP-712 (typed structured data) is the modern alternative; chapter 10 discusses why we'd switch to it for production.

### Where this lands in the fork

The chapter modifies six places in `~/workspace/agent-irc-ergo`:

```
irc/agentirc/sasl.go          (new)  — crypto: nonce, EIP-191 hash, ecrecover
irc/agentirc/sasl_test.go     (new)  — unit tests
irc/client.go                          — saslStatus.agentIRC field for per-conn state
irc/handlers.go                        — authERC8004Handler (~50 lines)
irc/accounts.go                        — register "ERC8004" in EnabledSaslMechanisms
irc/config.go                          — advertise it in the SASL cap value
go.mod / vendor/                       — add github.com/ethereum/go-ethereum
```

About 200 lines of new code, 4 lines of changes to existing files, plus the go-ethereum vendor.

### What chapter 07 still leaves open

Three things, addressed in chapters 08-10:

1. **Any wallet works.** A keypair generated 200ms before the connection authenticates fine. There's no notion of "registered agent" — that's chapter 08.
2. **Account name is just the truncated address.** `0xC502FEA9b3477878` is unmemorable. Chapter 09 replaces this with the on-chain registered name.
3. **Nonce isn't bound to the deployment.** A signature for one server-id could replay against another. Chapter 10 binds the body to `(chain_id, server_name, nonce)`.

By the end of chapter 07: our verify program (which has go-ethereum/crypto baked in) produces a valid signature, and Ergo accepts it without ever storing a password. There's no off-the-shelf weechat or irssi plugin that does ERC8004 SASL — clients have to grow wallet support to participate, which is a real adoption barrier discussed in chapter 10.

## What you'll learn

- How to add a new SASL mechanism to Ergo's `EnabledSaslMechanisms` table.
- The 3-step challenge-response pattern (mirroring SCRAM, but using EIP-191 signatures instead of HMAC-derived keys).
- EIP-191 (`personal_sign`) and why we use it for SASL.
- Why the `account-tag` from chapter 06 is now load-bearing — it carries an unforgeable identity.

## What you'll build

Three commits' worth of work in `~/workspace/agent-irc-ergo`, all on the `agent-irc` branch:

| File in fork | Change |
|---|---|
| `irc/agentirc/sasl.go` (new) | Crypto helpers: `NewNonce`, `ChallengeBody`, `EIP191Hash`, `RecoverAddress`, `VerifyChallenge`, `AccountNameForAddress` |
| `irc/agentirc/sasl_test.go` (new) | Unit tests: round-trip, wrong-address rejection, wrong-nonce rejection |
| `irc/client.go` | New field `agentIRC []byte` on `saslStatus`, cleared in `Clear()` |
| `irc/handlers.go` | New `authERC8004Handler` (~50 lines) |
| `irc/accounts.go` | Register `"ERC8004": authERC8004Handler` in `EnabledSaslMechanisms` |
| `irc/config.go` | Append `"ERC8004"` to the SASL cap value (advertise it) |
| `go.mod` / `go.sum` / `vendor/` | Add `github.com/ethereum/go-ethereum` |

The chapter directory itself contains the verify program (in pure Go, with go-ethereum/crypto for client-side signing) and a launcher script.

## Run it

```bash
./verify.sh
```

What you'll see (excerpt):

```
=== verify (positive + negative ERC8004 SASL cases) ===
--- positive: alice signs with the key matching her claim ---
  alice -> AUTHENTICATE ERC8004
  alice <- AUTHENTICATE +
  alice -> AUTHENTICATE xQL+qbNHeHjQrguu6E4gNuPC3Uo=                      # base64(20-byte addr)
  alice <- :ergo.test AUTHENTICATE ge8cVeRLScNHl5OD0Dmjvsa9Flsq9MfKDUYXcD+p4oE=  # base64(32-byte nonce)
  alice -> AUTHENTICATE +XMeRH7I+SQBdGsJFXIrVPTpMLznytACp9U6rELpwDB0E…    # base64(65-byte sig)
  alice <- :ergo.test 900 * * 0xC502FEA9b3477878 :You are now logged in as 0xC502FEA9b3477878
  alice <- :ergo.test 903 * :Authentication successful
  alice -> CAP END
  alice <- :ergo.test 001 0xC502FEA9b3477878 :Welcome to the ErgoTest IRC Network 0xC502FEA9b3477878
  ✓ session bound to account 0xC502FEA9b3477878

--- negative: bob signs with key A, claims address B ---
  bobby -> AUTHENTICATE <base64(addr B)>
  bobby <- AUTHENTICATE <base64(nonce)>
  bobby -> AUTHENTICATE <base64(sig signed by key A)>
  bobby <- :ergo.test 904 * :SASL ERC8004: signature verification failed
  ✓ rejected as expected
PASS: ERC8004 SASL succeeds; signature mismatch is rejected
```

## Walkthrough

### The 3-step protocol

```
                       client                      server
                       ──────                      ──────

1) mechanism select     AUTHENTICATE ERC8004    →
                                                ←   AUTHENTICATE +

2) claim                AUTHENTICATE <b64(addr)> →     ┐ store addr
                                                       │ generate nonce
                                                ←  AUTHENTICATE <b64(nonce)>  ┘ store nonce

3) prove (response)     ┐ msg = "agent-irc-sasl-v1\nnonce=" + hex(nonce)
                        │ hash = keccak256("\x19Ethereum Signed Message:\n" + len(msg) + msg)
                        │ sig = personal_sign(hash, privkey)
                        │
                        AUTHENTICATE <b64(sig)>  →     ┐ ecrecover(hash, sig) == addr ?
                                                       │ if yes:
                                                ←  900 RPL_LOGGEDIN as 0x<addr-truncated>
                                                ←  903 RPL_SASLSUCCESS
                                                       │ if no:
                                                ←  904 ERR_SASLFAIL
```

### Why 3 steps and not 2

You might wonder why we don't fold step 1 (claim) into step 3 (sig). A pure 2-step would have the client send `addr || sig` together. The reason we don't:

- The **nonce must be server-issued** (not client-issued), or replays are trivial: an attacker who once observed alice's signature could replay it forever.
- For the server to issue the nonce *before* the client can sign, the server has to send the challenge as an intermediate step. That's step 2.

This is exactly why SCRAM is multi-step: server-issued challenges are the only way to prevent passive replay.

### The EIP-191 wrapping

We don't sign the raw bytes `agent-irc-sasl-v1\nnonce=...`. We sign the keccak256 of:

```
\x19Ethereum Signed Message:\n<decimal length of body><body>
```

This is the EIP-191 "personal_sign" envelope. Why bother:

- **Wallets implement `personal_sign` natively** (MetaMask, Frame, Rainbow, hardware wallets via WalletConnect). The user clicks "sign this message" and gets a clean dialog. Without EIP-191, you'd be asking them to sign arbitrary 32-byte hashes, which most wallets refuse.
- **The `\x19` prefix is non-ASCII**, which guarantees the byte sequence cannot collide with a valid Ethereum transaction's RLP encoding. So a signature produced by personal_sign cannot be replayed as a transaction. This is the primary security justification — it keeps the SASL credential isolated from the on-chain authority.

EIP-712 (typed structured data) is the modern alternative. We could absolutely use it; the message structure becomes:

```typescript
{
  domain: { name: "agent-irc", version: "1", chainId: 8453 },
  types: { Login: [{ name: "nonce", type: "bytes32" }] },
  primaryType: "Login",
  message: { nonce }
}
```

EIP-712 wallets render this as a structured prompt ("Login to agent-irc with nonce 0x...") rather than EIP-191's raw text. For a production agent network, EIP-712 is the better choice. For chapter 07, EIP-191 keeps the implementation tight and the wire payload small (no JSON, no domain separator). Chapter 10 revisits this trade-off.

### Account name ↔ address mapping

The server needs to bind the verified address to an Ergo account. Our chapter-07 mapping is:

```go
func AccountNameForAddress(addr common.Address) string {
    full := addr.Hex()           // "0xABC...12" 42 chars, EIP-55 checksum
    return "0x" + full[2:18]     // "0xABC...12" 18 chars, partial EIP-55 case
}
```

**This is a placeholder.** Three problems:

1. **Truncation collisions.** Two distinct 20-byte addresses will collide on the first 8 bytes with probability ~`N²/2⁶⁵`. For a few thousand agents, fine. For a million, you'd want more bits.
2. **EIP-55 mixed case** is preserved but is fragile to clients that lowercase nicks (Ergo's `CASEMAPPING=ascii` doesn't lowercase digits, so `0xABCD...` and `0xabcd...` are different account names — but historical IRC clients may not honor that).
3. **The name has no human meaning.** `0xC502FEA9b3477878` is unmemorable.

Chapter 09 replaces this entire function with a lookup against the ERC-8004 registry: the agent's *registered name* (a human-meaningful string) becomes the IRC account name, with on-chain semantics for who's allowed to claim it.

### `loadWithAutocreation` — auto-registering on first auth

PLAIN and EXTERNAL both require the account to *already exist* on the server. ERC8004 doesn't — the wallet keypair is the credential. So we use Ergo's `loadWithAutocreation`:

```go
account, err := server.accounts.loadWithAutocreation(accountName, true)
```

If the account exists, load it. If not, register it (no password, no email — the wallet is the only credential), then load it. The internal `SARegister` path Ergo provides for service-admin auto-registration is exactly the right fit.

**Security note:** lazy auto-registration means the *first* successful signature for an address binds the account. There's no way for someone else to "claim" an existing address — the only way to authenticate as `0xC502FEA9b3477878` is to control the keypair. Chapter 08 layers ERC-8004 registry membership on top, so even valid signatures from non-registered addresses are rejected.

### Why the `agentIRC` field on `saslStatus`

`saslStatus` already had:
- `mechanism` — which SASL mech is in flight
- `value` — the base64 accumulator for chunked responses
- `scramConv` — SCRAM's per-conversation state
- `oauthConv` — OAUTHBEARER's per-conversation state

We added:
- `agentIRC []byte` — `address || nonce`, set after step 1's address arrives, consumed in step 3.

The handler uses `len(s.agentIRC) == 0` as the "step 1 vs step 3" discriminator. We could have made this a struct (`type erc8004State struct{ addr common.Address; nonce [32]byte }`), but the byte slice is simpler and the layout invariant (20 + 32) is hard to confuse.

### What advertising `sasl=...,ERC8004` actually does

In `irc/config.go` we appended `"ERC8004"` to `saslCapValues`. This makes the CAP LS line read:

```
sasl=PLAIN,EXTERNAL,SCRAM-SHA-256,ERC8004
```

Clients that don't understand `ERC8004` simply pick PLAIN or EXTERNAL — there's no breakage. Clients that do understand it (like our verify program) request it explicitly via `AUTHENTICATE ERC8004`.

This is the IRCv3 contract for adding a SASL mechanism: announce it in the cap value, register a handler, done. It composes cleanly with everything else (`account-tag`, `message-tags`, `chathistory`, `multiline`).

## Critical Thinking: what does this proof prove?

A successful ERC8004 SASL exchange proves: **the session at this moment is operated by something that has access to the private key for address X, here at time T**.

Three things it does not prove:

1. **Past sessions.** A signature in this nonce is not evidence that the same key signed yesterday. Each session re-proves freshness.
2. **The "owner."** Wallets can be hot, custodied, multi-sig, or compromised. Possession of the signing key is not a statement about who, in human terms, controls it.
3. **Anything on-chain.** Chapter 07 doesn't read the chain. The address can be a fresh keypair generated 200 ms before the connection — there's no notion of "registered agent" yet.

This last point is what chapter 08 fixes. The threat model after chapter 07 is "anyone with any keypair can join" — useful for a development substrate, useless for a production network with paying agents. Chapter 08 raises the bar to "anyone whose address appears in the ERC-8004 registry can join."

A second-order concern: **timing.** Our nonce is 32 random bytes, but we don't bind it to anything else — server identity, connection ID, expiry. A malicious entity proxying connections could route alice's challenge to bob, get bob's signature, and present it back — *if* alice and bob were both expected to sign the same domain `agent-irc-sasl-v1`. To prevent this we'd want to encode the server identity into the body:

```
agent-irc-sasl-v1
domain=agent-irc.example.com
chain=8453
expires=1730000000
nonce=...
```

Chapter 10's threat model section formalizes this and tightens the body.

## Files

```
07-custom-sasl-erc8004/
├── ircd.yaml              # auto-copied from chapter 06 by start-ergo.sh
├── start-ergo.sh          # builds the agent-irc fork, runs on :16673
├── go.mod / go.sum
├── verify/main.go         # positive (matched key) + negative (wrong key) test
├── verify.sh
└── README.md

# Modified in the fork (~/workspace/agent-irc-ergo, branch agent-irc):
irc/agentirc/sasl.go         # crypto helpers + protocol constants
irc/agentirc/sasl_test.go    # 3 unit tests
irc/client.go                # +agentIRC field on saslStatus
irc/handlers.go              # +authERC8004Handler
irc/accounts.go              # +"ERC8004" in EnabledSaslMechanisms
irc/config.go                # +"ERC8004" in saslCapValues
go.mod / go.sum / vendor/    # +go-ethereum
```

## Next

[Chapter 08 — Gating on the registry](../08-gating-on-the-registry) — we wrap the `VerifyChallenge` call with an on-chain ERC-8004 lookup against Base mainnet (via a forked anvil for local testing). Successful signatures from non-registered addresses get 904 ERR_SASLFAIL. We deploy a reference Identity Registry contract to a local Base fork, register one agent, and test end-to-end.
