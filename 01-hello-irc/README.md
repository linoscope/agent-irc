# Chapter 01 — Hello, IRC

Build the smallest IRC server that completes the registration handshake.

## Mental model: what is IRC?

Before any code, here's the picture in 5 minutes.

### One paragraph

IRC is a 1988 chat protocol that runs over a single long-lived TCP connection per client. Every line is a UTF-8 text command terminated by CR LF. The server is the source of truth for everything — who's connected, what channels exist, who's in each one. Clients send commands; the server validates, mutates state, and broadcasts the resulting events to whoever's listening. The protocol has no acknowledgments, no message IDs (until IRCv3), no built-in encryption (until you bolt on TLS), and no notion of "rooms exist on multiple servers" without a federation protocol the IRCs we care about don't use. It is gloriously simple, and that simplicity is exactly why we can teach it from byte zero.

### The four entities

```
                        ┌────────────────────┐
                        │       Server       │
                        │                    │
                        │  • clients (set)   │
                        │  • channels (map)  │
                        │  • who is in what  │
                        └─────────┬──────────┘
                  ┌───────────────┼───────────────┐
                  │               │               │
            ┌─────┴─────┐   ┌─────┴─────┐   ┌─────┴─────┐
            │  Client   │   │  Client   │   │  Client   │
            │  (alice)  │   │   (bob)   │   │  (carol)  │
            └───────────┘   └───────────┘   └───────────┘
                  │               │               │
                  └────── #room (a channel) ──────┘
                          (server-side state)
```

- **Server.** Holds all state. One process, one binary. (Multi-server federation exists — RFC 2813, TS6 — but it's a 30-year-old codebase of pain we deliberately avoid.)
- **Client.** A long-lived TCP connection that has identified itself as someone. Identifies via two strings: a *nick* (display name, e.g. `alice`) and a *user@host* part (mostly cosmetic).
- **Channel.** A named, server-managed broadcast group, conventionally prefixed with `#` (e.g. `#room`). Joining is `JOIN #room`; leaving is `PART #room`. Messages sent to `#room` are fanned out by the server to every other current member. There is **no** peer-to-peer channel; the channel is a row in a server-side map.
- **Message.** A line. Either client→server (a *command*: `NICK`, `JOIN`, `PRIVMSG`, …) or server→client (a *numeric reply* like `001` or `353`, or a *relayed event* like `:alice!… JOIN #room`).

### The whole conversation, end to end

What happens when alice connects, joins `#room`, says "hi", and disconnects:

```
TCP connect to irc.example:6667        # bytes start flowing

C: NICK alice                          ┐ "registration": tell the
C: USER alice 0 * :Alice the Agent     ┘  server who you are
                                       
S: :irc.example 001 alice :Welcome…    ← server's "you're in" greeting
S: :irc.example 002 alice :…           ← a few more numeric replies (chapter 03)
S: :irc.example 005 alice CASEMAPPING=…  ← capability advertisement (chapter 03)

C: JOIN #room                          ┐ alice wants in
S: :alice!alice@host JOIN #room        │ ← server confirms by relaying the
                                       │   JOIN event (this is what other
                                       │   members in #room will see too)
S: :irc.example 353 alice = #room :…   │ ← list of current members (NAMES)
S: :irc.example 366 alice #room :End…  ┘ ← end of NAMES list

C: PRIVMSG #room :hi everyone          ← alice sends a message
                                       
   [server fans this out to every
    other current member of #room as:]
                                       
S→bob:   :alice!alice@host PRIVMSG #room :hi everyone
S→carol: :alice!alice@host PRIVMSG #room :hi everyone

C: QUIT :bye                           ← alice leaves
S→bob:   :alice!alice@host QUIT :bye   ← everyone in shared channels learns
S→carol: :alice!alice@host QUIT :bye

TCP close.                             # connection's gone
```

Five things this picture surfaces that come back constantly:

1. **Every message is a single line.** No multi-line frames, no length-prefixed bodies. CR LF terminates everything.
2. **The server stamps a source prefix on relayed events** (`:alice!alice@host`). The client never writes this; the server adds it so the recipient knows who the message is from. The prefix is **the** identity carrier on the wire.
3. **Numeric replies (001, 353, 366, …) are how the server answers commands.** They have a fixed shape: `:server NNN nick params... :human-readable-text`. Chapters 03 and beyond explain why there are so many.
4. **State changes propagate by event broadcast, not by query.** Bob doesn't poll "is alice in #room?" — the server told him `JOIN #room` from alice when she joined and will tell him `QUIT` when she leaves. Bob's client maintains a local mirror. Servers are authoritative; clients are projections.
5. **There's no acknowledgment that bob received alice's PRIVMSG.** TCP delivered it to bob's *socket*. Whether bob's *client process* read it is invisible to alice. (IRCv3 `labeled-response` adds optional acknowledgement for request/response semantics — chapter 06.)

### Vocabulary you'll see throughout

| Term | What it is |
|---|---|
| **Nick** (or nickname) | Your display name on the network. Unique while you're connected. Mutable via `/nick`. |
| **User**, **host** | The middle and tail of `nick!user@host`. `user` is whatever you sent in `USER`; `host` is your IP or a cloaked variant. Largely cosmetic. |
| **Channel** | `#name`. Server-managed broadcast group. Joining = adding yourself to a set on the server. |
| **Registration** | The act of completing the initial handshake (`NICK` + `USER` → `001`). Once "registered," you can issue most commands. |
| **Numeric reply** | A 3-digit code from server to client. `001`–`099` are connection-level, `200`–`399` are info, `400`–`599` are errors. The full registry: [defs.ircdocs.horse](https://defs.ircdocs.horse/defs/numerics.html). |
| **PRIVMSG** | The "send a message" verb. Targets a channel (`#room`) or a nick (`bob`). |
| **NOTICE** | Like PRIVMSG but, by convention, no auto-reply. Used for system messages and bot notifications. |
| **PING / PONG** | Keepalive (chapter 03). |
| **CAP** | "Capability negotiation" — IRCv3's mechanism for clients and servers to opt into newer features without breaking old ones (chapter 05). |
| **SASL** | The authentication framework, run inside the CAP-held registration window (chapter 06). |
| **`account-tag`** | An IRCv3 message tag that carries a verified account name on every message — the only honest identity signal (chapter 06). |

### What we are *not* implementing in chapter 01

To keep the first server under 150 lines, we ignore:

- **Channels** (chapter 02).
- **PING/PONG keepalive** — your test client will disconnect cleanly, so we don't need it yet (chapter 03).
- **ISUPPORT (numeric 005)** — telling the client what flavor of IRC this is (chapter 03).
- **Casemapping rules** — `Foo[bar]` and `foo{bar}` are the same nick on most networks (chapter 03).
- **Error replies** — sending the wrong command should produce a numeric, but chapter 01 just ignores unknowns.
- **TLS, IRCv3, SASL** — all later chapters.

What we *do* implement: enough to take a fresh TCP connection, accept `NICK` + `USER`, and emit `001 RPL_WELCOME`. That's the door into the protocol. Once it works, every later feature is a layer on top.

Now to the code.

## What you'll learn

- The IRC line framing (CR LF, 512-byte cap, the trailing `:` parameter).
- The two-step registration handshake: `NICK` then `USER`, ending with numeric `001 RPL_WELCOME`.
- Why the protocol is line-oriented and stateful in a way that HTTP isn't.

## What you'll build

A ~120-line Go program that listens on `:6667`, accepts one or more clients, and replies to each with `001` once they've sent both `NICK` and `USER`. No channels, no PING/PONG, no error replies — those land in chapters 02 and 03. By the end of this chapter, you will have *typed IRC by hand into netcat* and seen the server respond.

## Run it

```bash
# Terminal A — start the server
go run .

# Terminal B — talk to it raw
nc -C localhost 6667
NICK alice
USER alice 0 * :Alice the Agent
```

You should see:

```
:irc.example 001 alice :Welcome to the chapter-01 IRC server, alice
```

The server log shows the parsed messages on each side:

```
[127.0.0.1:38300] connected
[127.0.0.1:38300] <- NICK [alice]
[127.0.0.1:38300] <- USER [alice 0 * Alice the Agent]
[127.0.0.1:38300] -> :irc.example 001 alice :Welcome to the chapter-01 IRC server, alice
```

To verify automatically: `./verify.sh` — exits 0 iff the handshake completes.

## Walkthrough

### The wire format

Every IRC message is a single line terminated by **CR LF**, capped at 512 bytes including the CR LF. That cap is load-bearing: nick lengths, channel-name limits, kick reasons, MOTD lines all bow to it. (IRCv3 message tags get a separate ~4 KB budget — chapter 05.)

The shape of a line is:

```
[@tags] [:source] command [param1 param2 ...] [:trailing parameter with spaces]
```

Chapter 01 ignores tags and source — clients don't send them, and we don't emit them yet. What's left:

- **`command`** — either an alphabetic verb (`NICK`, `USER`, `PRIVMSG`) or a 3-digit numeric (`001`, `353`).
- **Parameters** — space-separated tokens.
- **The trailing parameter** is special: introduced by `:` after the previous space, it consumes the rest of the line *including spaces*. This is how `USER alice 0 * :Alice the Agent` puts "Alice the Agent" into one parameter.

The naive parser in `main.go`:

```go
if i := strings.Index(line, " :"); i >= 0 {
    trailing = line[i+2:]
    line = line[:i]
    hasTrailing = true
}
fields := strings.Fields(line)
```

Two known bugs we leave in for chapter 02 to fix:

1. `strings.Fields` collapses runs of spaces — but RFC 1459 §2.3.1 allows multiple spaces between params, and `strings.Fields` happens to do the right thing there. The real bug: it also strips an empty trailing param. `KICK #c nick :` is supposed to mean "kick with empty reason," but our parser drops it.
2. We don't handle a leading `:source` prefix. Servers always send one (`:irc.example 001 alice :...`); clients almost never do, but a federated link absolutely would.

We'll exercise both against the [`ircdocs/parser-tests`](https://github.com/ircdocs/parser-tests) corpus in chapter 02.

### The registration handshake

A fresh TCP connection to an IRC server is in a "pre-registration" state. The server doesn't know who you are yet. You declare yourself with two commands:

```
NICK alice
USER alice 0 * :Alice the Agent
```

`NICK` is the desired display name. `USER` carries:
- `<user>` — historically your local Unix username (today it's whatever the client wants).
- `<mode>` — a bitmask, mostly historical.
- `<unused>` — really called `unused` in the spec; servers ignore it.
- `<:realname>` — free-form display string in the trailing param.

The server doesn't reply to either individually. Once *both* have arrived, it emits `001 RPL_WELCOME`:

```
:irc.example 001 alice :Welcome to the chapter-01 IRC server, alice
```

Format: `:<server-source> <numeric> <recipient-nick> :<human-readable text>`. Numeric replies always carry the recipient's nick as the first parameter — this is how a client knows which of its in-flight connections a reply belongs to.

After `001`, you're "registered" and can issue any other command. Real servers also send `002 RPL_YOURHOST`, `003 RPL_CREATED`, `004 RPL_MYINFO`, then a flurry of `005 RPL_ISUPPORT` (chapter 03). Some clients hang waiting for them.

### Why this state machine matters

The pre-registration state is the trapdoor that makes IRCv3 capability negotiation possible. We'll see in chapter 05 that `CAP LS` *holds the door open* — the client says "wait, I have things to negotiate" and registration doesn't complete (no `001`) until the client sends `CAP END`. Meanwhile SASL authentication runs inside that hold. Without the two-stage handshake, there'd be nowhere to graft authentication on.

## Critical Thinking: line-framing as a security boundary

> *Throughout the tutorial, each chapter ends with a "Critical Thinking" section that surfaces a design tradeoff or trust assumption. Borrowed from the dstack tutorial format.*

The 512-byte line cap is a sane lower bound, but our parser doesn't enforce it. A client can send 100 KB of garbage with no LF and our `bufio.Reader.ReadString('\n')` will happily allocate that. Worse, a client can send a line that contains an embedded `\r` or `\n` *inside* a parameter — IRC's line framing has no escaping mechanism.

This is the *injection* bug class for IRC. If you ever build an IRC bridge that takes user input from elsewhere (HTTP webhook, Slack, an LLM) and forwards it into an IRC channel, an embedded `\n` lets the attacker emit arbitrary additional commands as you. There is no "JSON.stringify" for IRC; sanitization is *strip CR/LF* before send, every time.

A defensive parser:

1. Caps reads at 512 bytes; lines longer get truncated or the connection dropped.
2. On send, strips `\x00`, `\r`, `\n` from every parameter before serialization.
3. Strips `\x01` if you don't want bridged content to accidentally frame [CTCP](https://modern.ircdocs.horse/ctcp).

Chapter 02 hardens the parser; chapter 06 brings sanitization back when we're forwarding LLM-produced text.

## Files

```
01-hello-irc/
├── main.go      # ~120 lines: listener, parser, registration state machine
├── go.mod
├── verify.sh    # automated end-to-end check
└── README.md
```

## Next

[Chapter 02 — Channels and broadcast](../02-channels) introduces multi-client routing: `JOIN`, `PRIVMSG`, `PART`, `QUIT`, and the parser hardening promised above.
