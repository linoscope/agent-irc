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

**SASL** — **S**imple **A**uthentication and **S**ecurity **L**ayer (RFC 4422) — is the standard "plug an auth mechanism into a stateful protocol" framework. SMTP, IMAP, LDAP, XMPP, and IRC all use it. SASL by itself doesn't define how authentication works; it defines the **plumbing** for swapping in different authentication mechanisms (PLAIN, EXTERNAL, SCRAM-SHA-256, our `ERC8004`, etc.). The host protocol — IRC in our case — gives SASL a way to ferry opaque bytes back and forth; the chosen mechanism handles the actual credential check.

In IRC, the SASL exchange runs *inside* the CAP-LS-held registration window from chapter 05a. The whole flow:

```
 1. CAP LS 302                                # trapdoor opens (chapter 05a)
 2. NICK / USER
 3. CAP REQ :sasl message-tags account-tag ...
 4. CAP * ACK
                                              # ----- SASL exchange -----
 5. AUTHENTICATE PLAIN                        # body depends on mechanism;
 6. AUTHENTICATE +    (server: ok)            # for PLAIN it's just username
 7. AUTHENTICATE <base64(creds)>              # + password.
 8. 900 RPL_LOGGEDIN as alice
 9. 903 RPL_SASLSUCCESS
                                              # ----- end SASL -----
10. CAP END                                   # trapdoor closes
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

### Watching it interactively (weechat as alice, nc as bob)

The chapter's experiment requires alice's session to live across multiple steps; weechat is the right tool for her side because it auto-replies to keepalive PINGs. Bob is one-shot (sends one PRIVMSG), so nc is fine for him.

The verify program is fine for asserting correctness. To watch SASL and `account-tag` happen by hand, run two terminals: one weechat as authenticated alice, one `nc` as anonymous bob. Each side sees what the other becomes.

```bash
# Terminal A — start the fork.
./start-ergo.sh
```

#### Step 0: register Alice (one-time setup)

The fork's data dir is wiped on every `start-ergo.sh`, so we need to create Alice's account once at the start of the session. Easiest way: a one-shot `nc`:

```bash
# Terminal B — register Alice and exit.
( printf 'CAP LS 302\r\n'
  printf 'NICK Alice\r\n'
  printf 'USER Alice 0 * :Alice\r\n'
  sleep 0.3
  printf 'CAP REQ :sasl draft/account-registration\r\n'
  sleep 0.2
  printf 'REGISTER * * hunter2\r\n'
  sleep 0.5
  printf 'QUIT\r\n'
) | nc -q1 localhost 16672
```

Look for `REGISTER SUCCESS Alice :Account successfully registered` in the output. Alice's account now exists in the fork's BoltDB until the next `start-ergo.sh` wipes it.

#### Step 1: connect Alice via weechat with SASL

Alice's session needs to stay alive across steps 3–5 (chat, observe tags, attempt nick change). For that we want a real client — `nc` doesn't auto-reply to the server's `PING` keepalive and the session would silently die after a few minutes of slow reading. weechat handles PING/PONG transparently.

Configure weechat to authenticate as Alice using SASL PLAIN, request the relevant caps, then connect:

```
weechat
/server add agentirc localhost/16672 -notls
/set irc.server.agentirc.sasl_mechanism plain
/set irc.server.agentirc.sasl_username Alice
/set irc.server.agentirc.sasl_password hunter2
/set irc.server.agentirc.capabilities account-tag,server-time,message-tags,echo-message
/connect agentirc
```

Press **`Alt+R`** (or `/server raw`) to open the raw IRC buffer. Scroll to the top to see the full SASL handshake:

```
--> CAP LS 302
<-- :ergo.test CAP * LS * :... sasl=PLAIN,EXTERNAL,SCRAM-SHA-256,ERC8004 ...
--> CAP REQ :sasl account-tag server-time message-tags echo-message
<-- :ergo.test CAP * ACK :sasl account-tag server-time message-tags echo-message
--> AUTHENTICATE PLAIN
<-- AUTHENTICATE +                                         # server ready
--> AUTHENTICATE AEFsaWNlAGh1bnRlcjI=                      # base64("\0Alice\0hunter2")
<-- :ergo.test 900 Alice Alice!~u@host Alice :You are now logged in as Alice
<-- :ergo.test 903 Alice :SASL authentication successful
--> CAP END
<-- :ergo.test 001 Alice :Welcome ...
```

The 900/903 numerics are the deliverable of the chapter. Past 903, this session is permanently bound to account `Alice`.

> **Want to see exactly what weechat is sending?** The same handshake driven by `nc` is a 7-line paste block:
>
> ```bash
> nc -C localhost 16672
> ```
>
> ```
> CAP LS 302
> NICK Alice
> USER Alice 0 * :Alice
> CAP REQ :sasl account-tag server-time message-tags echo-message
> AUTHENTICATE PLAIN
> AUTHENTICATE AEFsaWNlAGh1bnRlcjI=
> CAP END
> ```
>
> The base64 blob is `\0Alice\0hunter2` — the SASL PLAIN credential format from RFC 4616. (Compute it yourself: `printf '\0Alice\0hunter2' | base64`.) You'll see `AUTHENTICATE +`, `900`, `903`, `001` come back. Use this to demonstrate the protocol; switch to weechat for the long-lived session in steps 3–5 because nc won't auto-reply to keepalive PINGs.

#### Step 2: connect Bob via nc (no auth)

Bob doesn't authenticate, but he *does* opt into the same message tags as alice. Without `account-tag` in his cap set, the server wouldn't stamp the tag on incoming messages — and we'd never see step 3's payoff.

```bash
# Terminal C
nc -C localhost 16672
CAP LS 302
NICK bob
USER bob 0 * :Bob
CAP REQ :account-tag message-tags server-time
                            # wait for CAP * ACK :account-tag message-tags server-time
CAP END
                            # wait for 001
JOIN #demo
```

This makes the asymmetry sharp: bob and alice both REQ `account-tag`. The *only* difference is alice did SASL inside her CAP window and bob didn't.

#### Step 3: alice joins, sends a message

Back in weechat:

```
/join #demo
hi everyone
```

Look at Bob's nc (terminal C). He sees:

```
@account=Alice;msgid=...;time=2026-04-29T...   :Alice!~u@... PRIVMSG #demo :hi everyone
```

The `@account=Alice` tag is **server-stamped**. Alice didn't write it; the server added it because her session SASL'd as Alice. **This is the entire deliverable of the chapter.**

(In weechat's raw buffer (`Alt+R`) alice also sees her own message echoed back — that's the `echo-message` cap she REQ'd in step 1. Same wire format with `@account=Alice`, useful for confirming the server accepted what she sent.)

#### Step 4: bob sends a message

In Bob's nc (terminal C):

```
PRIVMSG #demo :hi back
```

In weechat's raw buffer (`Alt+R`), alice's view of bob's message:

```
@msgid=...;time=2026-04-29T...   :bob!~u@... PRIVMSG #demo :hi back
```

**No `account=` tag at all.** Bob is anonymous; the server has nothing authoritative to stamp.

That asymmetry — alice's messages are authenticated-attributed, bob's aren't — is what makes `account-tag` the *only* honest identity signal. Authorization on the receiving side keys on `account=`, never on the nick part of the prefix.

#### Step 5 (bonus): alice tries to change nick

In weechat:

```
/nick alice2
```

Ergo (with `force-nick-equals-account` on, the default) refuses:

```
:ergo.test 400 Alice NICK :You must use your account name as your nickname
```

(The exact numeric varies by ircd — Ergo emits `400`; other implementations may use `432 ERR_ERRONEUSNICKNAME` or `447 ERR_NONICKCHANGE`. The behavior is what matters: authenticated sessions can't escape their account name. Chapter 09 makes this property load-bearing for ERC-8004 binding.)

#### Step 6 (bonus): bob tries to impersonate alice

The deepest test of `account-tag` is "what stops bob from claiming to be alice?" Three vectors, all rejected by the server.

**Vector 1: bob `NICK Alice` while alice is connected.**

In Bob's nc:

```
NICK Alice
```

Server:

```
:ergo.test 433 bob Alice :Nickname is already in use
```

`433 ERR_NICKNAMEINUSE`. The nick is uniquely held by alice's session.

**Vector 2: bob `NICK Alice` after alice disconnects.**

Have alice quit weechat. Then bob retries:

```
NICK Alice
```

Server:

```
:ergo.test FAIL NICK NICKNAME_RESERVED Alice :Nickname is reserved by a different account
```

This is the IRCv3 [standard-replies](https://ircv3.net/specs/extensions/standard-replies) form — verb `FAIL` with code `NICKNAME_RESERVED`. With Ergo's `force-nick-equals-account` (the default), every registered account permanently owns its nick; nobody else can take it even when the owner is offline.

**Vector 3: bob does SASL PLAIN with the wrong password.**

```
CAP LS 302
NICK bob
USER bob 0 * :Bob
CAP REQ :sasl
AUTHENTICATE PLAIN
AUTHENTICATE AEFsaWNlAGJhZA==        # base64("\0Alice\0bad")
```

Server:

```
:ergo.test 904 * :SASL authentication failed: Invalid account credentials
```

`904 ERR_SASLFAIL`. Without alice's password, no SASL success, no `account=Alice` binding.

## Walkthrough

### SASL inside the CAP-LS-held window

The whole point of CAP LS 302 holding registration open (chapter 05a) is to give SASL somewhere to run. The flow:

```
  C -> CAP LS 302
  C -> NICK alice / USER alice 0 * :Alice
  S -> CAP * LS :sasl=PLAIN,EXTERNAL,SCRAM-SHA-256 ...
  C -> CAP REQ :sasl message-tags server-time account-tag
  S -> CAP * ACK :sasl ...

  # ----- SASL exchange -----
  C -> AUTHENTICATE PLAIN
  S -> AUTHENTICATE +                          # server ready for data
  C -> AUTHENTICATE <base64(\0user\0pass)>
  S -> 900 RPL_LOGGEDIN
  S -> 903 RPL_SASLSUCCESS
  # ----- end SASL -----

  C -> CAP END
  S -> 001 RPL_WELCOME alice :...
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
├── ircd.yaml             # auto-copied from chapter 05b by start-ergo.sh
├── start-ergo.sh         # builds the agent-irc fork, runs on :16672
├── go.mod
├── verify/main.go        # 4-phase: REGISTER, SASL PLAIN, anon, account-tag
├── verify.sh
└── README.md
```

(No fork modifications in this chapter — we exercise existing Ergo behavior.)

## Next

[Chapter 07 — Custom SASL mechanism (off-chain)](../07-custom-sasl-erc8004) — we add a new SASL mechanism `ERC8004` to the fork. Server emits a nonce, client signs with a wallet keypair, server `ecrecover`s and accepts. No on-chain check yet — that's [chapter 08b](../08b-gating-on-the-registry), with [chapter 08a](../08a-erc8004-by-hand) as the hands-on tour of the registry contract. This is where the `agent-irc-ergo` fork starts diverging structurally.
