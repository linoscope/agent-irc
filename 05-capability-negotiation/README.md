# Chapter 05 — Capability negotiation

## What you'll do, in plain English

Until now we've been *running* Ergo. This chapter is the first time we *change* it.

The change is small on purpose: we'll teach Ergo to advertise one new "feature flag" called `agent-irc.example/hello`. The flag does nothing useful — that's the point. We're rehearsing the workflow on something whose absence of behavior makes any failure obvious. The actually-interesting customizations (chapters 07–10) reuse the exact same workflow.

The four steps you'll perform:

1. **Edit one file** in the Ergo fork — `gencapdefs.py`. Add a 5-line block naming our new feature.
2. **Run a code generator.** That Python file *generates* a Go file (`irc/caps/defs.go`). Run `python3 gencapdefs.py > irc/caps/defs.go`; the Go file gets rewritten to include our new cap.
3. **Rebuild Ergo.** `go build` produces a new binary that knows about the cap.
4. **Watch it on the wire.** Connect a test client, ask the server "what features do you support?" — confirm `agent-irc.example/hello` shows up in the answer.

That's the chapter.

A bit of vocabulary you'll see throughout, in plain language:

| Term | Plain English |
|---|---|
| **Capability** (or "cap") | A feature flag — a named optional behavior the server *can* do that the client has to opt into. Like HTTP's `Accept-Encoding: gzip` but for IRC. |
| **Vendor capability** | A cap we made up ourselves, not part of any standard. We name it under a domain (`agent-irc.example/...`) so it doesn't clash with standard ones. |
| **No-op** | Doesn't do anything. Empty behavior. We're proving the plumbing works before adding actual behavior in chapter 07. |
| **CAP LS** | The command a client sends to ask "what features do you support?" |
| **CAP REQ** | The command a client sends to opt into a feature. |
| **CAP ACK / NAK** | The server's reply: "ok, granted" / "no, can't do that." |
| **`CAP LS 302`** | The same `CAP LS` command with an integer arg saying "I speak version 302 of CAP, send me the modern format." |
| **Code generation** | Ergo's list of caps lives in a Python file that generates Go code. Edit Python, run a script, the Go file is overwritten. We don't edit `defs.go` directly. |
| **Wire** | The actual TCP bytes flowing between client and server. "Watch it on the wire" = look at the literal bytes a real client sees. |

If any of those still feels foggy, the next section ("Mental model") explains the underlying problem CAP solves and why this design.

## Mental model: how IRC adds features without breaking anyone

IRC has been deployed for 30+ years. There are clients still in use that were written in 2003. There are servers still running C code from 1998. Adding new features (account-tag, message IDs, server-time, SASL, message tags, multiline messages, replies, edits, deletions, …) without breaking that installed base is the puzzle CAP solves.

The trick: **clients opt in.** A modern feature is invisible to a client that doesn't know to ask for it; visible the moment they do. The server advertises what it knows; the client requests the subset it understands; both sides agree before any new behavior turns on. A 2003 client that has never heard of `account-tag` connects to a 2026 server, the server doesn't shove tags at it, the client behaves normally — both sides happy.

### Where CAP lives in the connection lifecycle

Recall chapter 01's registration handshake: `NICK` + `USER` → `001 RPL_WELCOME`. CAP wedges itself *into* that handshake by holding it open:

```
client → CAP LS 302         ┐  trapdoor opens — server now knows
                            │  registration is held while caps are negotiated
client → NICK alice         │
client → USER alice 0 * :…  │
                            │  (server must NOT emit 001 yet)
server → CAP * LS :cap1 cap2 cap3 …       ← here's everything I support
                            │
client → CAP REQ :cap1 cap2               ← give me these (atomic)
                            │
server → CAP * ACK :cap1 cap2             ← granted
                            │
                            │  (auth, configuration, anything that
                            │   needs to happen pre-001 goes here —
                            │   chapter 06's SASL lives in this window)
                            │
client → CAP END            ┘  trapdoor closes
                            
server → 001 alice :Welcome…    registration completes normally
```

The `302` is the **CAP version** the client claims to speak. Version 302 is what enables continuation lines (`CAP * LS *` followed by another `CAP * LS`), capability values (`sasl=PLAIN,EXTERNAL`), and `cap-notify` for changes after registration. Without `302`, you get the older one-line dump and the server breaks gracefully if it overflows.

### What an "advertised capability" actually looks like

A capability is a *named string* like `account-tag`, `multi-prefix`, or `sasl`. Names follow conventions:

| Form | Meaning | Example |
|---|---|---|
| Plain name | Standard IRCv3 capability | `account-tag`, `server-time`, `batch` |
| `draft/<name>` | On the IRCv3 standardization track but not finalized | `draft/chathistory`, `draft/multiline` |
| `<dotted-domain>/<name>` | Vendor-specific; namespaced by a domain you control | `znc.in/self-message`, `ergo.chat/nope`, `agent-irc.example/hello` |

A capability can also carry a *value*: `sasl=PLAIN,EXTERNAL,SCRAM-SHA-256` tells the client which SASL mechanisms are available. Plain caps have no value.

### What chapter 05 changes

The chapter-04 Ergo fork has all the standard caps. Chapter 05 adds **one new vendor cap**:

- **Identifier in Go**: `caps.AgentIRCHello`
- **Wire name**: `agent-irc.example/hello`
- **Behavior**: nothing yet. It just appears in `CAP LS 302` and accepts `CAP REQ`.

The point is not the cap itself — it's the *mechanism*. Once you've seen how a capability gets defined, advertised, requested, and acknowledged, you've seen the entire IRCv3 extension surface. Chapter 07's `ERC8004` SASL mechanism uses the exact same machinery, just with a meaningful payload.

### Why CAP is interesting beyond IRC

Every protocol that has to evolve under an installed base hits the same problem. HTTP/2 settles for `Upgrade:` headers and content negotiation. TLS uses extensions in the ClientHello. IMAP has `CAPABILITY`. SSH has `kex-algorithms`. They all do roughly the same thing — "advertise what you have, let the peer pick what they understand, both sides commit."

IRC's CAP is unusually clean for two reasons:

1. **Asymmetric.** The server lists; the client picks. Not "negotiate." This avoids the combinatorial explosion of "well, I'll do A if you do B unless C" that plagues TLS extension dependencies.
2. **Atomic REQ.** A client asks for a *set* of caps; the server enables all of them or none. No partial commits. Half-enabled feature sets cause more bugs than missing features.

### Vocabulary new in this chapter

| Term | What |
|---|---|
| **Capability** (or "cap") | A named, optional protocol feature both sides opt into |
| **CAP LS / REQ / ACK / NAK / END** | The CAP subcommand verbs |
| **CAP version** | The `302` in `CAP LS 302`. What CAP machinery the client supports |
| **Vendor cap** | A non-standard, namespaced cap (`vendor.example/feature`) |
| **Draft cap** | A cap on the standardization track but not finalized (`draft/...`) |
| **Continuation line** | A CAP LS line ending with `*` indicating more is coming |

## What you'll learn

- The `CAP LS / CAP REQ / CAP ACK / CAP END` handshake and how it holds registration open.
- IRCv3 `CAP LS 302` continuation lines: the `*` marker and how clients reassemble.
- How Ergo's capability table is structured: a Python source-of-truth (`gencapdefs.py`) that generates Go (`irc/caps/defs.go`).
- How to register a vendor capability and see it appear in the wild.

## What you'll build

A single commit on the `agent-irc` branch of `~/workspace/agent-irc-ergo`:

```
a79e0065 chapter 05: add agent-irc.example/hello vendor capability
```

It modifies two files:

| File | Change |
|---|---|
| `gencapdefs.py` | One new `CapDef(...)` entry for the vendor cap |
| `irc/caps/defs.go` | Auto-regenerated by `python3 gencapdefs.py > irc/caps/defs.go` |

The chapter directory itself contains:

| File | Purpose |
|---|---|
| `start-ergo.sh` | Build the fork, reset state, run on `:16671` |
| `ircd.yaml` | Same minimal config from chapter 04 |
| `verify/main.go` | A 5-step CAP LS 302 handshake from scratch (see below) |
| `verify.sh` | Start fork, run verify, tear down |

## Run it

```bash
./verify.sh
```

Output:

```
=== CAP LS 302 → REQ → ACK → 001 ===
  -> CAP LS 302
  -> NICK alice
  -> USER alice 0 * :Alice
  <- :ergo.test CAP * LS * :account-notify account-tag agent-irc.example/hello away-notify ...
  <- :ergo.test CAP * LS :server-time setname standard-replies userhost-in-names znc.in/self-message
  parsed 31 capabilities
  -> CAP REQ :agent-irc.example/hello
  <- :ergo.test CAP * ACK agent-irc.example/hello
  -> CAP END
  <- :ergo.test 001 alice :Welcome to the ErgoTest IRC Network alice
PASS: agent-irc.example/hello advertised, REQ acknowledged, registration completes
```

## Walkthrough

### CAP LS 302 holds registration open

Without CAP, registration is `NICK + USER → 001`. With `CAP LS 302`, the trapdoor opens:

```
client → server: CAP LS 302              ┐
client → server: NICK alice               │ — server *must not* emit 001
client → server: USER alice 0 * :Alice    │   while CAP negotiation is in flight
                                          │
server → client: CAP * LS * :cap1 cap2 …  │ — `*` after LS = continuation
server → client: CAP * LS :capN capN+1    │ — no `*` = final batch
                                          │
client → server: CAP REQ :cap1 cap2       │ — atomic: all-or-nothing
server → client: CAP * ACK cap1 cap2      │   (or NAK if any unsupported)
                                          │
client → server: CAP END                  ┘ — release the trapdoor
                                          
server → client: 001 alice :Welcome …       — registration completes
```

The state machine in Ergo lives in `irc/handlers.go` (`capHandler`) and `irc/client.go` (registration progression). The `302` integer is the **CAP version** the client claims to speak — version 302 is the one that supports continuation lines, capability values (`sasl=PLAIN,EXTERNAL`), and `cap-notify` for post-registration cap changes. Without `302`, the server emits a single line and breaks if it overflows.

### Why `CAP REQ` is atomic

`CAP REQ :cap-a cap-b cap-c` is a transaction: the server enables *all* of them or *none*. If any one is unsupported, the response is `CAP NAK :cap-a cap-b cap-c` and the client's session has the same caps it had before. This atomicity is load-bearing for caps with mutual dependencies — `labeled-response` is meaningless without `batch`, `chathistory` is meaningless without `server-time`. Atomic REQ lets clients express "I want all of these or none."

### How Ergo's cap table works

The directory `irc/caps/` holds:

```
defs.go          # WARNING: this file is autogenerated. DO NOT EDIT MANUALLY.
constants.go     # version constants (302 etc.)
set.go           # bitset operations on a fixed-size array
set_test.go
```

`defs.go` is generated by `python3 gencapdefs.py`. The generator reads a Python list of `CapDef(identifier, name, url, standard)` tuples and emits a Go file containing:

```go
const numCapabs = 39  // we added one
const bitsetLen = 2

const (
    AccountNotify Capability = iota   // index 0
    AccountTag                        // index 1
    AgentIRCHello                     // ← us, sorted alphabetically by name
    AwayNotify
    Batch
    ...
)

var capabilityNames = [numCapabs]string{
    "account-notify",
    "account-tag",
    "agent-irc.example/hello",   // ← us
    "away-notify",
    ...
}
```

Why source-generated? Because the *bitset width* (`numCapabs`, `bitsetLen`) has to be a compile-time constant for the per-session `caps.Set` to be a fixed-size array — IRC servers run with millions of sessions in production, and that means caring about per-session memory. Adding a cap requires recompiling, not config.

### Our change

We added one entry to the CAPDEFS list in `gencapdefs.py`:

```python
CapDef(
    identifier="AgentIRCHello",
    name="agent-irc.example/hello",
    url="https://github.com/lin/agent-irc",
    standard="agent-irc vendor",
),
```

Then ran:

```bash
python3 gencapdefs.py > irc/caps/defs.go
```

That's it. `caps.NewCompleteSet()` (in `irc/caps/set.go`) iterates over `numCapabs` and enables every index, so our cap is automatically advertised at boot.

### IRCv3 vendor namespace conventions

The naming `agent-irc.example/hello` follows the IRCv3 vendor convention: `<dotted-domain>/<feature-name>`. The dotted domain part is meant to be a domain you control (we'd use a real one in production); the slash-separated suffix names the feature. Vendor caps coexist with standard ones — no central registry, no risk of collision with future standard caps.

The `draft/` prefix is a related convention for caps that are on track to become standard but haven't yet (`draft/multiline`, `draft/chathistory`, `draft/account-registration`). Once standardized, the `draft/` prefix is dropped and a `cap-notify` is sent to inform clients.

For chapter 07 we'll add a *valued* vendor cap announcing the SASL mechanism: something like `agent-irc.example/sasl=ERC8004`, parallel to how Ergo currently advertises `sasl=PLAIN,EXTERNAL,SCRAM-SHA-256`. The pattern is identical — one `CapDef` entry, plus a `capValues` assignment in `irc/config.go`.

## Critical Thinking: capability advertisement is a fingerprint

`CAP LS 302` is a server-side capability dump. Every cap you advertise tells anyone who connects (including pre-authenticated attackers) something about your stack:

- Stock Ergo caps → "this is Ergo, version newer-than-X (introduced cap Y)"
- `znc.in/self-message` → "ZNC bouncer or Ergo emulating ZNC" (compatibility shim Ergo carries)
- `ergo.chat/nope` → "definitely Ergo" (Ergo-specific cap that disables auto-DM-from-strangers)
- `agent-irc.example/hello` → "this is an agent-irc deployment"
- `sasl=PLAIN,EXTERNAL,SCRAM-SHA-256` → "auth methods enumerated"

For our agent-irc threat model this is mostly fine — the deployment isn't trying to be anonymous. But two implications worth noting:

1. **Capability advertisement is the easiest IRC server fingerprint.** Anyone scanning port 6697 can `CAP LS 302` and identify the implementation. Operators who need anonymity (Tor-only servers) often disable identifying caps.
2. **Adding a vendor cap before you've shipped its behavior leaks intent.** We just announced `agent-irc.example/hello` to the world before chapters 07–10 ship anything that uses it. In a real production rollout, you'd ship behavior first and announce only when supported. For a tutorial, announcing first lets us prove the cap pipeline works without waiting on the auth surface.

The flip side: a capability can also be a *commitment*. Once you advertise `chathistory`, clients depend on it; deprecating it requires `cap-notify DEL` and graceful client handling. The cost of adding a cap is therefore not just the implementation — it's the maintenance contract.

## Files

```
05-capability-negotiation/
├── ircd.yaml             # same as chapter 04, listener moved to :16671
├── start-ergo.sh         # builds the agent-irc fork (not upstream ergo)
├── go.mod
├── verify/main.go        # CAP LS 302 → REQ → ACK → 001 e2e
├── verify.sh
└── README.md

# Modified in the fork (~/workspace/agent-irc-ergo, branch agent-irc):
gencapdefs.py             # one new CapDef entry
irc/caps/defs.go          # regenerated
```

## Next

[Chapter 06 — SASL and account-tag](../06-sasl-and-account-tag) — we use Ergo's existing SASL PLAIN flow to register an account, then watch `account-tag` appear on every PRIVMSG. This is the chapter where authenticated identity becomes part of the wire format.
