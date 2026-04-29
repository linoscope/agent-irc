# Chapter 01 — Hello, IRC

Build the smallest IRC server that completes the registration handshake.

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
