# Chapter 06 — SASL and account-tag

The chapter where authenticated identity becomes part of the wire format. After this chapter, every PRIVMSG from an authenticated client carries `@account=<name>` as an IRCv3 message tag — and chapters 07–10 build their authorization on that tag.

## Mental model: from nick to identity

Chapters 01–05 had a problem: when bob receives `:alice!alice@host PRIVMSG #room :hi`, who actually said it?

The honest answer is: *we don't know*. Three substrings claim to be identity, none are reliable:

| Where | What it is | Trustable? |
|---|---|---|
| `alice` (the nick) | The display name alice typed in `NICK alice` | ❌ Anyone can set NICK. After alice disconnects, anyone can grab `alice`. |
| `alice` (the user, middle of `nick!user@host`) | What alice typed in `USER alice 0 * :…` | ❌ Client-controlled. Forged routinely by bots. |
| `host` | alice's IP address (or a cloak) | ⚠️ Server-controlled and reliable as a network identifier, but not as a person identifier — IPs are shared, NATed, rotated. |

So pre-chapter-06 IRC has **no honest answer** to "who is this message from?" beyond "the network endpoint that sent it." Authorization decisions ("alice is op of #room", "only alice can join #alice-private") have to fall back on heuristics: maintain a session whitelist, key on hostname, etc. All workarounds.

### The fix: a verified account name, on every message

Chapter 06 introduces a fourth string into every relayed message:

```
@account=alice;msgid=...;time=… :alice!alice@host PRIVMSG #room :hi
└──────┬──────┘                                                   
   server-stamped IRCv3 message tag                                
```

The `account=alice` tag is **server-attested**. The client did not write it. The server stamped it onto the message *because* this session previously authenticated as account `alice` via SASL. If alice quits and someone else NICK-grabs `alice`, *that session has no SASL state*, so the server doesn't stamp `account=alice`. The tag is unspoofable from the client side.

### How does the session "previously authenticate"?

SASL — the authentication framework — runs *inside* the CAP-LS-held registration window from chapter 05. The whole flow:

```
1. CAP LS 302   ←─── trapdoor opens (chapter 05)
2. NICK / USER
3. CAP REQ :sasl message-tags account-tag …
4. CAP * ACK
5. AUTHENTICATE PLAIN              ┐
6. AUTHENTICATE +    (server: ok)  │  the SASL exchange.
7. AUTHENTICATE <base64(creds)>    │  body depends on mechanism;
8. 900 RPL_LOGGEDIN as alice       │  for PLAIN it's just username + password.
9. 903 RPL_SASLSUCCESS             ┘
10. CAP END   ←─── trapdoor closes
11. 001 RPL_WELCOME
```

After step 9, the session is permanently bound to account `alice`. Every message it sends thereafter carries `account=alice`. If SASL fails (904), no binding happens; the session continues as anonymous, and its messages carry no `account=` tag.

### The three mechanisms (and which we'll use)

| Mechanism | Credential | Where to use |
|---|---|---|
| **PLAIN** | username + password, base64'd | Behind TLS only. Simplest. We use this in chapter 06 to demonstrate the flow. |
| **EXTERNAL** | nothing — server uses TLS client cert | Identity follows a keypair via cert. Cleaner cryptographically; couples to cert lifecycle. |
| **SCRAM-SHA-256** | challenge/response over a salted hash | Defense against passive observers without TLS. Server never sees the password. |

For agent-irc (chapter 07+), **none of these are quite right** — agents have wallet keypairs, not passwords or X.509 certs. So we'll define our own mechanism, `ERC8004`, with the same wire shape as the standard ones. The dispatch machinery, the 900/903/904 numerics, the `account-tag` plumbing — all of that is what chapter 06 makes us internalize.

### Where account-tag shows up

Once SASL succeeds and the client has the `account-tag` capability, the server stamps the `account=<name>` tag on **every** message it relays from this session:

- `PRIVMSG`, `NOTICE`, `TAGMSG` — chat
- `JOIN`, `PART`, `QUIT`, `KICK`, `INVITE` — channel state changes
- `NICK`, `MODE`, `AWAY`, `TOPIC` — user/channel state
- `CHATHISTORY` replays — even past events

Authorization on the receiving side keys on the tag. A bot that gives ops based on "the joiner's nick" is hijackable; a bot that gives ops based on `account=` is not.

### What chapter 06 doesn't change in the fork

This chapter is a walkthrough, not a modification. Ergo already implements all of this. The chapter's `verify` program drives Ergo through a 4-phase sequence (REGISTER → SASL PLAIN → anonymous client → observe `account-tag`), and we read the wire transcript to make the mechanics concrete. The fork only changes in chapter 07 when we add the new SASL mechanism.

## What you'll learn

- The IRCv3 SASL handshake — how auth runs *inside* the CAP-LS-held registration window.
- The difference between SASL PLAIN, EXTERNAL, and SCRAM-SHA-256 (and which we'd pick for an agent network).
- The IRCv3 `draft/account-registration` REGISTER command.
- Why `account-tag` is the only authentication signal you should make authorization decisions on (and why `nick!user@host` is not).

## What you'll build

We don't modify Ergo in this chapter — it already implements all of this. Instead, we write a 4-phase verify program that exercises Ergo's existing SASL and account-tag support to make the mechanisms concrete:

| Phase | What | What you observe on the wire |
|---|---|---|
| 1 | Register Alice (`REGISTER * * hunter2`) | `REGISTER SUCCESS Alice`, then `900 RPL_LOGGEDIN` (auto-auth) |
| 2 | Reconnect; authenticate via SASL PLAIN | `AUTHENTICATE +`, `AUTHENTICATE <base64>`, `903 RPL_SASLSUCCESS` |
| 3 | Connect Bob without auth | `001` only |
| 4 | Both join `#room`; exchange PRIVMSGs | Alice's tagged `@account=Alice`, Bob's untagged |

## Run it

```bash
./verify.sh
```

Highlights from the wire transcript:

```
# Phase 2: SASL PLAIN handshake
  Alice -> AUTHENTICATE PLAIN
  Alice <- AUTHENTICATE +
  Alice -> AUTHENTICATE AEFsaWNlAGh1bnRlcjI=         # base64("\0Alice\0hunter2")
  Alice <- :ergo.test 900 Alice ... :You are now logged in as Alice
  Alice <- :ergo.test 903 Alice :SASL authentication successful

# Phase 4: account-tag on PRIVMSG
  bob   <- @account=Alice;msgid=...;time=2026-04-29T09:53:23.516Z
            :Alice!~u@host.irc PRIVMSG #room :hello from authenticated Alice
  Alice <- @msgid=...;time=2026-04-29T09:53:23.517Z
            :bob!~u@host.irc PRIVMSG #room :hello from anonymous bob
```

Notice Alice's message carries `account=Alice`; Bob's has no `account=` tag at all.

## Walkthrough

### SASL inside the CAP-LS-held window

The whole point of CAP LS 302 holding registration open (chapter 05) is to give SASL somewhere to run. The flow:

```
client → CAP LS 302
client → NICK alice / USER alice 0 * :Alice
server → CAP * LS :sasl=PLAIN,EXTERNAL,SCRAM-SHA-256 ...
client → CAP REQ :sasl message-tags server-time account-tag
server → CAP * ACK :sasl ...
                                                       ┐ SASL exchange
client → AUTHENTICATE PLAIN                            │
server → AUTHENTICATE +     (server is ready for data) │
client → AUTHENTICATE <base64(\0user\0pass)>           │
server → 900 RPL_LOGGEDIN                              │
server → 903 RPL_SASLSUCCESS                           ┘
client → CAP END
server → 001 RPL_WELCOME alice :...
```

The numerics that matter:

| Numeric | Meaning |
|---|---|
| 900 | `RPL_LOGGEDIN` — your account is now bound to this session |
| 901 | `RPL_LOGGEDOUT` — used after `AUTHENTICATE *` to abort an in-flight auth |
| 902 | `ERR_NICKLOCKED` — account is locked, can't be auth'd to |
| 903 | `RPL_SASLSUCCESS` — terminal: SASL exchange completed successfully |
| 904 | `ERR_SASLFAIL` — terminal: bad creds or unsupported mechanism |
| 905 | `ERR_SASLTOOLONG` — base64 payload too big |
| 906 | `ERR_SASLABORTED` — client sent `AUTHENTICATE *` |
| 907 | `ERR_SASLALREADY` — already logged in (we see this when REGISTER's auto-auth races a follow-up AUTHENTICATE) |

You must complete (903/904/906/907) before `CAP END` or you get registered unauthenticated.

### What goes in the AUTHENTICATE blob

For PLAIN (RFC 4616): `\x00<authcid>\x00<passwd>` base64-encoded. `authcid` is the account name; an optional `<authzid>\x00` prefix lets you authenticate as user A but act as user B (used for impersonation by privileged accounts; agent-irc doesn't need it).

If the base64 payload exceeds 400 bytes, you must chunk it into 400-byte pieces sent across multiple `AUTHENTICATE` lines. A final exact-400-byte chunk requires a trailing `AUTHENTICATE +` sentinel so the server knows it's done. Real PLAIN payloads are short enough that this never matters; SCRAM-SHA-256 hits it occasionally.

To abort an in-flight auth (e.g., user typed the wrong password and noticed): send `AUTHENTICATE *`. The server replies `906 ERR_SASLABORTED` and you can start over with a fresh `AUTHENTICATE <mech>`.

### The three mechanisms — which to pick for agents

| Mechanism | What it sends | When you'd use it |
|---|---|---|
| `PLAIN` | username + password, base64'd, in clear | Always over TLS; simplest. Fine for the tutorial. |
| `EXTERNAL` | nothing (single `AUTHENTICATE +`) | Server uses the *transport* to authenticate — typically TLS client certificate fingerprint pre-registered on the account. Identity follows the keypair, not a password. |
| `SCRAM-SHA-256` | challenge-response over a salted hash | Defense against passive observation even without TLS. Server never sees the password. |

For agent-irc:

- **PLAIN** is what we'll use for the `ERC8004` mechanism in chapter 07 — but we'll override the meaning of the "password" field. Instead of a bcrypt'd password, we'll put a wallet signature there, since the SASL framing is identical and the per-mechanism payload is opaque to the protocol.
- **EXTERNAL** is the alternative path: TLS cert with the wallet address in the SAN, server matches the cert's signing key against an ERC-8004 entry. This is cleaner cryptographically (no nonce/replay window inside SASL) but couples agent identity to TLS cert lifecycle, which is its own problem.
- **SCRAM** isn't relevant for our use case; agents have keypairs, not passwords.

We chose PLAIN-style for chapter 07 because it makes the on-chain check happen *inside* SASL where it belongs, rather than punting it to a TLS layer that may or may not be present.

### `account-tag`: the only honest identity signal

Once SASL succeeds, every server-relayed message Alice generates includes `@account=Alice`:

```
@account=Alice;msgid=...;time=2026-04-29T09:53:23.516Z :Alice!~u@host.irc PRIVMSG #room :hi
```

Three identity-bearing strings in that line:

1. `@account=Alice` — set by the server, not the client. Only present if SASL succeeded *for this session*. Authoritative.
2. `:Alice!~u@host.irc` — the source prefix. The `Alice` part is the **nick**, which can change (`/nick newname`) and is reusable after disconnect. The `~u@host` part is the user/host. **None of this is authoritative** — clients control their nick, and Ergo's `~u` is a placeholder when ident lookup is disabled. Bridges and bots routinely lie about all three.
3. `:Alice!...` (in the prefix again) — redundant with 2; the same string.

For authorization, **always** use `account-tag`. Never `nick`. The reason: nick changes, account names are stable. A bot that accidentally allows `IF source.nick == "alice" THEN op` can be hijacked by anyone who NICK-grabs the moment alice disconnects. With `account-tag`, the auth derives from "did this session SASL-auth as Alice", which is unforgeable from the client side.

For an unauthenticated client (Bob in our test), no `account=` tag appears. Some servers emit `@account=` with an empty value to make this explicit; Ergo elides it.

### `account-tag` extends to JOIN, NICK, MODE, KICK

Look at Alice's JOIN line:

```
@account=Alice;msgid=...;time=... :Alice!~u@host.irc JOIN #room
```

Account-tag isn't PRIVMSG-only. Every relayed message from an authenticated session carries it: JOIN, PART, QUIT, NICK, MODE, KICK, INVITE, AWAY. This means a channel ACL that looks up "is the joiner authorized to be in this channel" can use the account-tag on the JOIN itself, before the JOIN is broadcast. Chapter 10 lists ERC-8004 channel gating as future work — the mechanism is straightforward (a custom channel mode that consults the registry on JOIN), but we ran out of chapter scope.

### Persistence across reconnect

We registered Alice once. The next time her client connects (phase 2), it just SASLs in — the credentials are persistent in Ergo's BoltDB. Restart Ergo, and Alice's account is still there. This is the *account-as-database-row* model that bouncers (ZNC, soju) historically bolted on top of unmodified IRC servers; in Ergo it's first-class.

For agent-irc this is the foundation of the always-on agent design. An agent registers once, gets the IRCv3 `draft/persistence` capability if it wants always-on (ergo-specific), and its channel memberships and history survive across crashes/restarts/redeploys. Chapter 10 leans on this when its mutation watcher KILLs an always-on session — the watcher uses Ergo's `killClients` (Logout + Quit + destroy) to force the wire close even when the always-on identity would otherwise persist.

## Critical Thinking: SASL PLAIN over TLS, but where's the TLS?

We're testing on plaintext `:16672`. A real deployment would never run SASL PLAIN over a clear channel. Two options:

1. **TLS terminates at Ergo.** Ergo speaks TLS on `:6697`. Pros: simplest, end-to-end encrypted between client and server. Cons: Ergo holds the cert; cert rotation is operational work.

2. **TLS terminates at a reverse proxy** (HAProxy, nginx, Caddy). Ergo runs plaintext on a Unix socket or loopback. Pros: cert rotation is the proxy's job; better metrics/observability. Cons: requires PROXY protocol so Ergo sees the real client IP.

The agent-irc deployment we're heading toward (chapter 10) probably wants option 2 — the proxy gives us a clean place to terminate TLS and forward client cert info via PROXY protocol v2 for chapters that use SASL EXTERNAL.

There's a deeper threat-model question lurking here: **how much do we trust the IRC server with credentials?**

For password-based SASL, the server has to see the password to verify it (bcrypt-on-the-server). Compromise the server, you lose every password. That's the same trust assumption every web service makes; usually fine.

For agent-irc with ERC-8004, **the server never holds a credential it could exfiltrate** — agents prove identity by signing a server-issued nonce with their wallet keypair. Server compromise lets an attacker observe nonces and signatures, but cannot forge a signature for a wallet they don't control. This is strictly stronger than password-based SASL, and is one of the design's intrinsic upsides.

We get to that mechanism in chapter 07.

## Files

```
06-sasl-and-account-tag/
├── ircd.yaml             # auto-copied from chapter 05 by start-ergo.sh
├── start-ergo.sh         # builds the agent-irc fork, runs on :16672
├── go.mod
├── verify/main.go        # 4-phase: REGISTER, SASL PLAIN, anon, account-tag
├── verify.sh
└── README.md
```

(No fork modifications in this chapter — we exercise existing Ergo behavior.)

## Next

[Chapter 07 — Custom SASL mechanism (off-chain)](../07-custom-sasl-erc8004) — we add a new SASL mechanism `ERC8004` to the fork. Server emits a nonce, client signs with a wallet keypair, server `ecrecover`s and accepts. No on-chain check yet — that's chapter 08. This is where the `agent-irc-ergo` fork starts diverging structurally.
