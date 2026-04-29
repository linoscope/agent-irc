# Chapter 05a — Capability negotiation

## What you'll do, in plain English

You'll learn what IRCv3 capabilities are by *poking at them by hand* against the Ergo server you built in chapter 04. No source modifications. No code generation. No rebuild.

By the end of this chapter you'll be able to:

1. Ask any IRC server "what features do you support?" and read the reply.
2. Opt your session into a feature with `CAP REQ` and see the server `ACK` it.
3. Tell which lines on the wire come from chapter 03's mechanism (`005 RPL_ISUPPORT`, server-side facts) and which come from CAP (per-session feature flags).

Chapter 05b will then *add a new feature* to Ergo's source so a custom one shows up in the cap list. This chapter just teaches you how to read the cap surface that's already there.

A bit of vocabulary, in plain language:

| Term | Plain English |
|---|---|
| **Capability** (or "cap") | A feature flag — a named optional behavior the server *can* do that the client has to opt into. Like HTTP's `Accept-Encoding: gzip` but for IRC. |
| **CAP LS** | The command a client sends to ask "what features do you support?" |
| **CAP REQ** | The command a client sends to opt into a feature. |
| **CAP ACK / NAK** | The server's reply: "ok, granted" / "no, can't do that." |
| **CAP END** | The command a client sends to say "I'm done negotiating, please complete registration." |
| **`CAP LS 302`** | The same `CAP LS` command with an integer arg saying "I speak version 302 of CAP — send me the modern format." |
| **Vendor cap** | A non-standard, namespaced cap (e.g. `znc.in/self-message`, `ergo.chat/nope`). Owners can add their own. |
| **Wire** | The actual TCP bytes flowing between client and server. "Watch it on the wire" = look at the literal bytes a real client sees. |

## Mental model: how IRC adds features without breaking anyone

IRC has been deployed for 30+ years. There are clients still in use that were written in 2003. There are servers still running C code from 1998. Adding new features (account-tag, message IDs, server-time, SASL, message tags, multiline messages, replies, edits, deletions, …) without breaking that installed base is the puzzle CAP solves.

The trick: **clients opt in.** A modern feature is invisible to a client that doesn't know to ask for it; visible the moment they do. The server advertises what it knows; the client requests the subset it understands; both sides agree before any new behavior turns on. A 2003 client that has never heard of `account-tag` connects to a 2026 server, the server doesn't shove tags at it, the client behaves normally — both sides happy.

### Where CAP lives in the connection lifecycle

Recall chapter 01's registration handshake: `NICK` + `USER` → `001 RPL_WELCOME`. CAP wedges itself *into* that handshake by holding it open:

```
[trapdoor opens; registration is now held until CAP END]

  C -> CAP LS 302
  C -> NICK alice
  C -> USER alice 0 * :Alice
       (server must NOT emit 001 yet)

  S -> CAP * LS :cap1 cap2 cap3 ...        # here's everything I support
  C -> CAP REQ :cap1 cap2                   # give me these (atomic)
  S -> CAP * ACK :cap1 cap2                 # granted

       (auth, configuration, anything that needs to happen
        pre-001 goes here -- chapter 06's SASL lives in this
        window)

  C -> CAP END

[trapdoor closes; registration completes normally]

  S -> 001 alice :Welcome...
```

The `302` is the **CAP version** the client claims to speak. Version 302 enables continuation lines (`CAP * LS *` followed by another `CAP * LS`), capability values (`sasl=PLAIN,EXTERNAL`), and `cap-notify` for changes after registration. Without `302`, you get the older one-line dump and the server breaks gracefully if it overflows.

### What an "advertised capability" actually looks like

A capability is a *named string* like `account-tag`, `multi-prefix`, or `sasl`. Names follow conventions:

| Form | Meaning | Example |
|---|---|---|
| Plain name | Standard IRCv3 capability | `account-tag`, `server-time`, `batch` |
| `draft/<name>` | On the IRCv3 standardization track but not finalized | `draft/chathistory`, `draft/multiline` |
| `<dotted-domain>/<name>` | Vendor-specific; namespaced by a domain you control | `znc.in/self-message`, `ergo.chat/nope` |

A capability can also carry a *value*: `sasl=PLAIN,EXTERNAL,SCRAM-SHA-256` tells the client which SASL mechanisms are available. Plain caps have no value.

### How is CAP different from 005?

Reasonable question after chapter 03. Both are "what does the server support." Different problems though:

| | 005 / `RPL_ISUPPORT` | CAP |
|---|---|---|
| What it conveys | passive facts: limits, namespaces, casemapping | active behaviors that change how the protocol acts |
| Direction | server announces; client just adapts | bidirectional — client must REQ before behavior turns on |
| Per-session state | none — same for everyone | each session opts into a different set |
| Example token | `NICKLEN=30` (a limit you must respect) | `account-tag` (a behavior; tags only appear if you opted in) |
| Era | added ~1999 | IRCv3, 2012+ |

The shorthand: **005 is passive facts about the server. CAP is active features you opt into.** A useful test — does enabling this *change behavior for this session*? If yes, it's CAP. If it's just a limit or a fact, it's 005.

`sasl=PLAIN,EXTERNAL,SCRAM-SHA-256` looks parameter-ish (like a 005 token) but it's a CAP value because *whether the server lets you `AUTHENTICATE` at all* depends on the REQ. The value just lists which mechanisms are available *if you opt in*.

## What you'll learn

- The `CAP LS / CAP REQ / CAP ACK / CAP END` handshake and how it holds registration open.
- IRCv3 `CAP LS 302` continuation lines: the `*` marker and how clients reassemble.
- How to probe any IRC server's caps from your everyday client.
- How to read the difference between standard, draft, and vendor caps.

## What you'll build

Nothing in code. The deliverable of this chapter is **muscle memory**: connect to a running IRC server, drive CAP by hand from your everyday client, and read the responses fluently.

No fork modifications, no rebuild, no Go program. Uses upstream Ergo (`~/workspace/ergo`) — the same binary chapter 04 built.

## Run it

```bash
# Start upstream Ergo on :16671, then walk the interactive recipe below.
./start-ergo.sh
```

The chapter has no automated `verify.sh`. Either Ergo is running and you can `CAP LS 302` it, or it isn't — and weechat's connection-time CAP handshake is the same protocol exchange as any verify program would do, just observable in the raw buffer. **The interactive recipe IS the verification.**

### Watching it interactively in weechat

The verify program is fine for asserting correctness. To *poke at the cap surface by hand*, drop into the raw IRC buffer.

```bash
# Terminal A — start upstream Ergo.
./start-ergo.sh
```

```
# Terminal B — connect and open the raw buffer.
weechat
/server add agent-irc localhost/16671 -notls
/connect agent-irc
```

Press **`Alt+R`** to open weechat's raw IRC buffer. This is the closest thing to the `nc` view — every line in both directions, in chronological order.

#### Step 1: see the LS that weechat already did at connect time

Scroll up in the raw buffer. Weechat sent `CAP LS 302` automatically as part of its connection sequence; the response is right there:

```
<-- :ergo.test CAP * LS * :account-notify account-tag away-notify batch cap-notify chghost ...
<-- :ergo.test CAP * LS :server-time setname standard-replies userhost-in-names znc.in/self-message
```

About 30 caps total. Find a few you recognize (`sasl`, `account-tag`, `server-time`) and a few you don't (`draft/multiline`, `znc.in/self-message`). Note that **none of them have `agent-irc.` prefix** — that's a deliberate setup for chapter 05b.

#### Step 2: ask for the cap list yourself

You can re-issue `CAP LS` at any time, even post-registration. Weechat's raw command is `/quote`:

```
/quote CAP LS 302
```

Another pair of `CAP * LS` lines comes back. (Post-registration the LS doesn't open the trapdoor — you're already past `001`. The server just dumps the current cap list as a reply.)

#### Step 3: REQ a standard cap manually

```
/quote CAP REQ :account-tag
```

The server acknowledges with `CAP * ACK`:

```
--> CAP REQ :account-tag
<-- :ergo.test CAP * ACK :account-tag
```

Now your session has `account-tag` enabled. Chapter 06 will explore what that means observably.

#### Step 4: see what's currently enabled

`CAP LIST` returns the caps that *this session* has opted into (vs. `CAP LS` which lists what's *available*):

```
/quote CAP LIST
```

You'll see weechat's defaults (the ones it negotiated automatically at connect time) plus `account-tag` (added by step 3).

#### Step 5: try a non-existent cap (atomicity demo)

```
/quote CAP REQ :nonexistent-cap-name
```

Server NAKs:

```
<-- :ergo.test CAP * NAK :nonexistent-cap-name
```

Try a *mixed* REQ to see the atomicity rule:

```
/quote CAP REQ :account-tag nonexistent-cap-name
```

The whole REQ NAKs because one of the two is bad — even though `account-tag` is real and was just ACK'd in step 3. **All-or-nothing**: the server enables every cap in a REQ list or none of them.

#### What the equivalent looks like in irssi

Same idea, but irssi's raw command is `/raw`:

```
/raw CAP LS 302
/raw CAP REQ :account-tag
/raw CAP LIST
```

irssi's raw output appears in a `/raw on` window — toggle with `/raw on`.

## Critical Thinking: capability advertisement is a fingerprint

`CAP LS 302` is a server-side capability dump. Every cap you advertise tells anyone who connects (including pre-authenticated attackers) something about your stack:

- Stock Ergo caps → "this is Ergo, version newer-than-X (introduced cap Y)"
- `znc.in/self-message` → "ZNC bouncer or Ergo emulating ZNC" (compatibility shim Ergo carries)
- `ergo.chat/nope` → "definitely Ergo" (Ergo-specific cap that disables auto-DM-from-strangers)
- `sasl=PLAIN,EXTERNAL,SCRAM-SHA-256` → "auth methods enumerated"

For our agent-irc threat model this is mostly fine — the deployment isn't trying to be anonymous. But two implications worth noting:

1. **Capability advertisement is the easiest IRC server fingerprint.** Anyone scanning port 6697 can `CAP LS 302` and identify the implementation. Operators who need anonymity (Tor-only servers) often disable identifying caps.
2. **Adding a vendor cap is a public commitment.** Once you advertise it, clients depend on it; deprecating it requires `cap-notify DEL` and graceful client handling. The cost of adding a cap is therefore not just the implementation — it's the maintenance contract.

## Files

```
05a-capability-negotiation/
├── ircd.yaml               # minimal Ergo config (same as chapter 04, port :16671)
├── start-ergo.sh           # builds *upstream* Ergo (~/workspace/ergo) — no fork
└── README.md
```

No fork modifications, no Go program — the chapter is the recipe.

## Next

[Chapter 05b — Adding a vendor capability](../05b-vendor-capability) — the second half. Now that you understand CAP, fork Ergo and add your own vendor cap (`agent-irc.example/hello`) so it appears in the LS list. This is the workflow chapters 07–10 reuse for the real customizations.
