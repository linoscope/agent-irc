# Chapter 02 — Channels and broadcast

The toy server from chapter 01 only spoke to one client at a time. This chapter turns it into a real (toy) chat server: multiple clients, channels, broadcast, and a parser that survives the [`ircdocs/parser-tests`](https://github.com/ircdocs/parser-tests) corpus.

## Mental model: what changes from chapter 01

Chapter 01 had **one client** at a time. The server's job was just "complete a handshake with this one connection." There were no other clients to talk to.

Chapter 02 has **many clients simultaneously**, and a new entity — the **channel** — to coordinate them.

```
                     ┌──────────────────────────────┐
                     │           Server             │
                     │                              │
                     │  clients = {                 │
                     │    "alice" → *Session,       │
                     │    "bob"   → *Session,       │
                     │    "carol" → *Session,       │
                     │  }                           │
                     │                              │
                     │  channels = {                │
                     │    "#room" → {alice, bob, carol},  ← a set of pointers
                     │    "#dev"  → {alice, dave},        ← into clients map
                     │  }                           │
                     └──────────────────────────────┘
                                  ▲
              ┌───────────────────┼───────────────────┐
              │                   │                   │
        TCP socket A         TCP socket B         TCP socket C
           (alice)              (bob)              (carol)
```

A channel is **a row in a server-side map**. There's nothing about it on any client's machine — clients only know the channel exists because the server told them. Joining `#room` is conceptually `channels["#room"].members.add(alice)`. Leaving is `delete`.

### What sending a message looks like under the hood

When alice sends `PRIVMSG #room :hi`, the server does this:

```
                 alice                                   server
                  ───                                     ───
   [client→]   PRIVMSG #room :hi
                                              parse line
                                              look up channel "#room"
                                              for each member m ≠ alice:
                                                  write to m's socket:
                                                  ":alice!alice@host PRIVMSG #room :hi\r\n"
                                              
   [server→]   (no echo to alice unless echo-message
               cap is requested — chapter 06)
                                              ┌────────►  bob's socket
                                              └────────►  carol's socket
```

This is the **fan-out pattern**: one inbound `PRIVMSG` from a sender becomes N outbound writes from the server, where N = channel size − 1. This is the entire mechanism that makes group chat work, and it's why a 1000-member channel is a server-resource cost: every message is a thousand writes.

### The source prefix is what makes broadcast useful

When bob's socket receives `:alice!alice@host PRIVMSG #room :hi`, the leading `:alice!alice@host` is the **source prefix** the server stamps on. Alice did not write it — she just sent `PRIVMSG #room :hi`. The server adds it on the way out so bob's client can answer "who said this?" There's no other "from" header in IRC; the prefix *is* the from header.

This becomes load-bearing in chapter 06 when authentication arrives: the `nick` part of the prefix is just a display label and can be hijacked by NICK-grabbing, which is why chapter 06 introduces `account-tag` as the unforgeable identity instead.

### Vocabulary new in this chapter

| Term | What |
|---|---|
| **Channel member set** | The set of currently-connected clients who have JOIN'd a given channel. Lives on the server. |
| **Fan-out** | One incoming message → N outgoing writes (one per other member). |
| **Source prefix** | `:nick!user@host` at the start of a server-relayed line. Server-stamped. |
| **NAMES list** | Numerics 353 + 366. The catalog of who's currently in a channel, sent to a joiner so their client knows who else is here. |

### What chapter 02 deliberately skips

- **Channel modes** (`+i` invite-only, `+m` moderated, `+t` topic-locked, `+b` bans): a real ircd has ~30 mode letters; we have zero.
- **Channel ops** (`@nick`): no privilege tiers — every member is equal in chapter 02.
- **Topic** (`/topic #room :news of the day`): the channel struct has a `topic` field but we never set or surface it.
- **PRIVMSG to a nick** (DM): we relay channel messages and direct messages, but skip the WHOIS plumbing and most validation.
- **PING/PONG, ISUPPORT, casemapping** — chapter 03.

By the end of this chapter, two `weechat` clients can connect, JOIN the same channel, and chat. That's the deliverable.

## What you'll learn

- How channels work as a server-side state object — they are not a peer-to-peer construct.
- The fan-out pattern: one `PRIVMSG` from a sender becomes N writes to the channel's members.
- Why every relayed message carries the sender's `nick!user@host` prefix, and what would break if it didn't.
- The parser edge cases that bit every IRC implementation in the 1990s, distilled into the parser-tests YAML.

## What you'll build

- A multi-client server (`main.go`, `state.go`, `commands.go`) that handles `NICK`, `USER`, `JOIN`, `PART`, `PRIVMSG`, `QUIT`, `PING` and emits the `353`/`366` NAMES reply on join.
- A real parser (`parser.go`) handling tags, source, multi-space, and the empty-trailing edge case — proven against 35 cases vendored from `ircdocs/parser-tests`.
- A two-client integration program (`verify/main.go`) that scripts alice and bob meeting in `#room`.

## Run it

```bash
# Verify (parser corpus + runtime smoke test):
./verify.sh

# Or play with it interactively. Terminal A:
go run .

# Terminal B (alice) — pick one client; the syntax differs:
#
#   weechat:                              irssi:
#   weechat                               irssi
#   /server add toy localhost/6667 -notls /connect localhost 6667
#   /connect toy                          /join #room
#   /join #room
#   hello, world                          hello, world
#
# Terminal C (bob): same, different nick. Watch the messages cross.
```

## Walkthrough

### The parser, properly

`parser.go` implements the full Modern IRC grammar: optional `@tags`, optional `:source`, verb, params, and the trailing parameter introduced by ` :`. The function never returns an error — any non-empty input parses into *some* `Message` — because IRC servers historically log-and-ignore garbage rather than dropping connections.

The four edge cases that matter, all in the test corpus:

```yaml
# 1. Empty trailing parameter is PRESENT, not absent.
- input: "foo bar baz :"
  atoms: { verb: foo, params: [bar, baz, ""] }

# 2. A trailing param can begin with literal ':'.
- input: "foo bar baz ::asdf"
  atoms: { verb: foo, params: [bar, baz, ":asdf"] }

# 3. Multiple spaces collapse.
- input: ":src   foo  bar"
  atoms: { source: src, verb: foo, params: [bar] }

# 4. Tag escapes are non-standard: \: → ; \s → space.
- input: "@a=b\\\\and\\nk;c=72\\s45;d=gh\\:764 foo"
  atoms: { tags: { a: "b\\and\nk", c: "72 45", d: "gh;764" }, verb: foo }
```

Trap (1) is the most common production bug: a parser that strips empty trailing parameters silently corrupts `KICK #c nick :`, `PRIVMSG bob :`, and several MODE forms. Our parser tracks `hasTrailing` separately so an empty trailing still appends to `params`.

`parser_test.go` loads the vendored YAML and runs all 35 cases. Run with `go test -v ./...` to see the count.

### Channels are server-owned state

A channel is an entry in `Server.channels` (`state.go`) holding a set of `*session` pointers. Nothing about the channel exists on a peer's machine — clients only know about it because the server tells them, via the JOIN broadcast and the 353/366 NAMES list.

Joining `#room`:

```
client → server: JOIN #room
server (broadcasts to existing members + the joiner):
                 :alice!alice@127.0.0.1 JOIN #room
server → joiner: :irc.example 353 alice = #room :alice bob carol
server → joiner: :irc.example 366 alice #room :End of /NAMES list
```

The `353 RPL_NAMREPLY` and `366 RPL_ENDOFNAMES` are an iterator pattern: 353 may repeat (if the names don't fit on one line), and 366 terminates. Clients that treat each 353 as the full answer are buggy. We never emit multiple 353s in this chapter, but the protocol allows it.

### Why every relayed message carries `nick!user@host`

When alice sends `PRIVMSG #room :hello bob`, what bob receives is:

```
:alice!alice@127.0.0.1 PRIVMSG #room :hello bob
```

The leading `:alice!alice@127.0.0.1` is the **source prefix**. The server adds it; alice did not. Without it, bob's client cannot answer "who said this?" — there's no other identity carried in PRIVMSG. This is also why IRC has no native "from" header at the protocol level. The prefix *is* the from header.

This becomes load-bearing in chapter 06 when we add `account-tag`. The prefix is spoof-free *only because the server controls it*, but the `nick` part can change (`/nick newname`) and is reusable after disconnect — it is a display label, not a stable identity. `account-tag` adds the verified account name as a separate IRCv3 message tag.

### Concurrency: per-conn writer goroutine

Three goroutines per connection:

1. **Read loop** — drains the socket into `Parse(line)` and dispatches. Lives in `main.go`.
2. **Write loop** — drains `s.out` (a buffered channel of strings) to the socket. Lives in `state.go`.
3. **Broadcasters** — *other* connections' read loops, when they need to deliver a message to this session, append to `s.out`.

This pattern decouples broadcast from socket IO: a slow recipient doesn't hold up the sender. The buffered channel acts as a per-connection send queue. When it fills (`enqueue` falls into the `default` branch), we drop the connection — that's the toy version of the **send-q overflow** kill that real ircds do, named because it is exactly what triggers `Excess Flood` disconnects on public networks.

State mutations all funnel through `Server.mu`. We use `RWMutex` so reads (PRIVMSG fan-out) don't serialize against each other, and acquire/release the lock as briefly as possible — collecting the recipient slice while locked, then writing without the lock held. Locking discipline matters more in chapter 03 when PING/PONG starts running concurrently.

### Why we use ASCII casemapping (and why that's wrong)

`lowerNick` and `lowerChan` (`state.go`) just call `strings.ToLower`. RFC 1459 §2.2 actually defines a richer mapping:

```
{}|^   are the lowercase forms of   []\~
```

…because IRC was originally Finnish, and Scandic letters lowercase that way in ISO-646-FI. So `Foo[bar]` and `foo{bar}` are supposed to be the same nick. We'll fix this in chapter 03 along with ISUPPORT — `CASEMAPPING=rfc1459` is the value most public networks advertise, and it surprises every modern parser that defaults to `strcasecmp`.

For chapter 02, ascii is good enough that everything else in the chapter works.

## Critical Thinking: broadcast amplification

A single `PRIVMSG #room :hi` from a 5-byte client send becomes N writes from the server, where N is the channel size minus one. In a 500-member channel, your 5-byte send produces ~7.5 KB of server-side egress. That's a 1500× amplification.

This isn't an attack vector — clients are rate-limited via fakelag (chapter 03) and the asymmetry is fundamentally what makes group chat work. But it does mean:

- **Channel size is a server resource cost**, not just a presentation choice. Every member adds linear cost to every message.
- **A compromised client in a large channel can exhaust the server's egress**, even within fakelag, by spamming small messages — fakelag bounds the *client's* send rate, not the server's relay rate.
- **Bridges from external systems** (HTTP webhooks → IRC) often ignore fakelag and become the worst offenders. Real public networks ban bridges that don't self-throttle.

For agent-irc this matters because LLM-driven agents can produce text bursts that the human-IRC ecosystem never anticipated. Our chapter-10 design enforces token-bucket sending in the SASL-authenticated session itself, not just at the client side, so an agent that ignores fakelag still cannot amplify past a server-set bound.

## Files

```
02-channels/
├── go.mod                          # gopkg.in/yaml.v3 for parser-tests
├── main.go                         # listener + per-connection goroutines
├── parser.go                       # full message grammar
├── parser_test.go                  # runs 35 ircdocs/parser-tests cases
├── state.go                        # Server, channel, session structs
├── commands.go                     # NICK/USER/JOIN/PART/PRIVMSG/QUIT/PING
├── parser-tests/
│   └── msg-split.yaml              # vendored from ircdocs/parser-tests (CC0)
├── verify/
│   └── main.go                     # two-client end-to-end smoke test
├── verify.sh                       # parser tests + runtime smoke
└── README.md
```

## Next

[Chapter 03 — Keepalive, ISUPPORT, casemapping](../03-keepalive-isupport) makes the server compatible with real IRC clients (`weechat`, `irssi`) by adding `PING`/`PONG` keepalive, the numeric 005 advertisement, and proper RFC 1459 casemapping.
