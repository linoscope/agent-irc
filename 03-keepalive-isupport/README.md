# Chapter 03 — Keepalive, ISUPPORT, casemapping

The chapter-02 server worked, but no real IRC client would stay connected to it for long. It would never know if the client was idle, never tell the client what it can and can't do, and would silently mishandle nicknames containing `[`, `]`, `\`, or `~`. Chapter 03 closes those gaps so that `weechat`, `irssi`, and `mIRC` all behave correctly.

## Mental model: three things real clients expect

A polished IRC client (weechat, irssi, mIRC) talks to the chapter-02 server and behaves *almost* normally — until one of three things happens:

1. **The connection sits idle for two minutes.** A real server would have sent a `PING` to check liveness; ours doesn't. Some clients tolerate this; others assume the server died and reconnect.
2. **The client wants to know "what flavor of IRC is this?"** Ergo, Solanum, UnrealIRCd, mIRC's built-in server — they all behave slightly differently. Real servers tell clients up-front, via a numeric called `005 RPL_ISUPPORT`. Ours doesn't, so the client falls back to RFC 1459 defaults that may or may not be right.
3. **A user has a nick like `Alice[bot]`.** Per RFC 1459 §2.2, IRC's casemapping says `Alice[bot]` and `alice{bot}` are the *same* nick. Our chapter-02 server uses ASCII `strings.ToLower`, which says they're different. Chaos ensues.

Chapter 03 fixes all three. Each is a small change but they cover three completely different concerns: **liveness**, **capability discovery**, and **identity equivalence**.

### 1. Idle detection: PING / PONG

```
                client                                   server
                ───                                       ───
                                          (silence for IdleTimeout = 120s)
                                                  │
                                                  ▼
                                            send "PING :sometoken"
        receive "PING :sometoken"  ◄────────
                                          (start a second IdleTimeout window)
        send "PONG :sometoken"     ────────►
                                            reset idle clock
                                                  │
                                                  ▼
                                          (silence for another 120s)
                                                  │
                                                  ▼
                                            send "PING :anothertoken"
                  (client crashed; never responds)
                                                  │
                                                  ▼
                                          (silence for ANOTHER 120s)
                                                  │
                                                  ▼
                                            close TCP, log "ping timeout"
```

Server-initiated, deadline-based, asymmetric. The client doesn't have to ping the server — though defensive ones do, since TCP can quietly die through NAT boxes.

### 2. Capability discovery: numeric 005

Right after `001 RPL_WELCOME`, a real server sprays one or more `005` lines:

```
:irc.example 005 alice NETWORK=AgentIRC CASEMAPPING=rfc1459 CHANTYPES=# PREFIX=(qaohv)~&@%+ NICKLEN=30 CHANNELLEN=64 :are supported by this server
                       └────────────────────────────────────── KEY=VALUE tokens, terminated by ───────────────────────────────────────┘
```

Each `KEY=VALUE` token is something the client can ask "what does this server allow me to do?" Examples:

| Token | What clients use it for |
|---|---|
| `NETWORK=AgentIRC` | Display: "Connected to AgentIRC" |
| `CASEMAPPING=rfc1459` | Picks the right nick-comparison function locally |
| `CHANTYPES=#` | Tells the client `#name` is a channel; `&name` is not (on this server) |
| `PREFIX=(qaohv)~&@%+` | The op/voice mode-to-symbol map |
| `NICKLEN=30` | Reject too-long nicks before sending |

Without 005, the client falls back to RFC 1459 defaults, which on a modern server are wrong about half the time. This is why every modern client emits `CAP LS` immediately on connect — to find out what the server *actually* supports.

### 3. Casemapping: the Finnish quirk

The single sentence in RFC 1459 §2.2 that breaks every modern parser:

> Because of IRC's scandanavian origin, the characters `{}|` are considered to be the lower case equivalents of the characters `[]\`, respectively.

Concretely:

```
RFC 1459 lowercase folding:
   A → a    [ → {    \ → |    ] → }    ~ → ^

Examples that fold to the same casefolded form:
   "Foo[bar]"  ←→  "foo{bar}"
   "Hello~"    ←→  "hello^"
   "ALICE\bot" ←→  "alice|bot"
```

ISO-646-FI (Finnish ASCII) put `Ä Ö Å` where US-ASCII puts `[ \ ]`, so when an IRC user typed lowercase, those characters folded to `{ | }`. The folding rule remains in the protocol forever as backwards compat.

If you use plain `strings.ToLower`:

- An attacker registers `alice`. The legitimate user `Alice[bot]` then collides — but with `ToLower` they hash to `alice[bot]`, while ChanServ records have `alice{bot}`. The two halves of the system disagree, silently. Nobody throws an error; auth just fails in confusing ways.

The chapter-03 server implements proper rfc1459 casefold. Chapter 03 also documents that real Ergo deliberately uses `CASEMAPPING=ascii` instead — modern clients support either, and ascii is less surprising.

### What chapter 03 deliberately skips

- **Anti-flood / fakelag**: every command costs server-side "lag credits"; too-fast clients get queued or killed. Real public networks live and die on this. We mention it; we don't implement it.
- **Connection limits**: per-IP, per-class. Vital in production; out of scope.
- **TLS, IRCv3 caps, SASL** — chapters 04+.

By the end of this chapter, the toy server from chapter 02 is *compatible* with `weechat`, `irssi`, and most older mIRC builds. They connect, see the right metadata, stay alive, and don't get confused about who's who.

## What you'll learn

- The PING/PONG keepalive contract: server-initiated, deadline-driven, asymmetric.
- The ISUPPORT (numeric 005) advertisement: how servers tell clients what casemapping, channel types, prefixes, and length limits are in effect.
- RFC 1459 casemapping: why `Foo[bar]` and `foo{bar}` must be treated as the same nick, and what breaks if you use `strings.ToLower` instead.

## What you'll build

Three changes on top of chapter 02, all incremental:

| File | Change |
|---|---|
| `state.go` | `casefold(s)` implementing rfc1459 lowercasing; `lowerNick`/`lowerChan` switched to it. |
| `main.go` | `IdleTimeout` (env-configurable). Read loop uses `SetReadDeadline` to detect silence; one PING then drop. |
| `commands.go` | `PONG` handler (silent — the read loop already cleared idle state). `005 RPL_ISUPPORT` line emitted at the end of registration. |

## Run it

```bash
# Verify everything (parser + 5 runtime steps):
./verify.sh

# Or watch the wire traffic with a real client:
go run .
weechat -t
/server add toy localhost/6667 -notls -autoconnect
/connect toy
/quote ISUPPORT             # see the 005 we sent
```

The full verify run produces output like:

```
=== layer 2: runtime (5 steps, IDLE_TIMEOUT=1s) ===
--- step 1: ISUPPORT (005) ---
  ann <- :irc.example 005 ann NETWORK=AgentIRC CASEMAPPING=rfc1459 CHANTYPES=# PREFIX= NICKLEN=30 CHANNELLEN=64 TOPICLEN=390 :are supported by this server
--- step 2: rfc1459 casemapping ---
  Foo[bar] <- :irc.example 001 Foo[bar] :Welcome to AgentIRC, Foo[bar]
  raw <- :irc.example 433 * foo{bar} :Nickname is already in use
--- step 3: ping timeout ---
  idler <- :irc.example PING :di5j…
  (server drops the unresponsive socket within ~1s of the unanswered PING)
--- step 4: ping reply keeps connection alive ---
  respond <- :irc.example PING :…
  respond -> PONG :…
  (still connected after another idle window)
--- step 5: PRIVMSG broadcast (regression) ---
  bob <- :alice!alice@127.0.0.1 PRIVMSG #room :hello bob
PASS: chapter 03 — ISUPPORT, casemapping, PING/PONG, broadcast
```

## Walkthrough

### Keepalive driven by `SetReadDeadline`

Many IRC implementations spawn a separate timer goroutine per connection that fires PINGs. We don't need it. Go's `net.Conn.SetReadDeadline` lets us *use the read syscall itself as a timer*:

```go
_ = s.conn.SetReadDeadline(time.Now().Add(IdleTimeout))
raw, err := rd.ReadString('\n')

if err != nil {
    var ne net.Error
    if errors.As(err, &ne) && ne.Timeout() {
        if pinged {
            log.Printf("[%s] ping timeout", s.conn.RemoteAddr())
            return
        }
        s.sendRaw(":irc.example PING :" + token + "\r\n")
        pinged = true
        continue
    }
    return // EOF or other unrecoverable error
}
pinged = false  // any inbound traffic resets the idle state
```

State machine:

```
                 inbound line
                   │
                   ▼
        ┌─────────────────────┐
        │      ACTIVE         │◄────────────┐
        │  pinged = false     │             │
        └─────────────────────┘             │
                   │                        │
            IdleTimeout (no inbound)        │
                   │                        │
                   ▼                        │
        ┌─────────────────────┐  any line   │
        │     PING SENT       │─────────────┘
        │  pinged = true      │
        └─────────────────────┘
                   │
            IdleTimeout (still nothing)
                   │
                   ▼
              DROP (Ping timeout)
```

The asymmetry — server pings, client replies — is by convention; the protocol itself is symmetric. Defensive clients also send their own PINGs, which is why `case "PING":` in `dispatch()` echoes a PONG back. Real clients (`irssi`, `weechat`) rarely do this, but agents on flaky networks should.

### ISUPPORT (numeric 005)

`005` is the original capability-discovery handshake, predating IRCv3 CAP by a decade. Servers spray one or more 005 lines at the end of registration, each with up to ~13 `KEY=VALUE` tokens, terminated by `:are supported by this server`.

```
:irc.example 005 ann NETWORK=AgentIRC CASEMAPPING=rfc1459 CHANTYPES=# PREFIX= NICKLEN=30 CHANNELLEN=64 TOPICLEN=390 :are supported by this server
```

What clients do with each token:

| Token | What it controls |
|---|---|
| `NETWORK` | Display: "Connected to AgentIRC" |
| `CASEMAPPING` | Drives every nick/channel string comparison the *client* makes |
| `CHANTYPES` | Which leading characters indicate a channel; clients won't tab-complete unfamiliar prefixes |
| `PREFIX` | Membership-mode-to-symbol map for ops/voice (we have none yet) |
| `NICKLEN` / `CHANNELLEN` / `TOPICLEN` | Input validation; clients reject too-long inputs locally |

A real chat network would advertise more — `CHANMODES`, `MODES`, `TARGMAX`, `STATUSMSG`, `EXTBAN`, `MONITOR`, `WHOX`, `ELIST`. The full registry lives at [`defs.ircdocs.horse/defs/isupport.html`](https://defs.ircdocs.horse/defs/isupport.html).

### RFC 1459 casemapping

The single sentence in RFC 1459 §2.2 that breaks every modern parser:

> Because of IRC's scandanavian origin, the characters `{}|` are considered to be the lower case equivalents of the characters `[]\`, respectively.

ISO-646-FI puts `Ä Ö Å` where ASCII puts `[ \ ]`, and lowercase `ä ö å` go where `{ | }` do. Folding makes `Foo[bar]` and `foo{bar}` the same nick. A later convention also folded `~` ↔ `^` (the non-strict `rfc1459` variant), and that's what we implement:

```go
func casefold(s string) string {
    b := make([]byte, len(s))
    for i := 0; i < len(s); i++ {
        c := s[i]
        switch {
        case c >= 'A' && c <= 'Z': c += 'a' - 'A'
        case c == '[':             c = '{'
        case c == ']':             c = '}'
        case c == '\\':            c = '|'
        case c == '~':             c = '^'
        }
        b[i] = c
    }
    return string(b)
}
```

What breaks if you use `strings.ToLower` instead:

- An attacker registers `alice`. They then collide with the legitimate user `Alice[bot]` because, with `strings.ToLower`, `Alice[bot]` casefolds to `alice[bot]`, but the legitimate user's *channel ops* and *nickserv* records use `alice{bot}`. They diverge silently — no error, just two different lookup keys for what should be the same identity.
- Channel name collisions on JOIN look like new channels rather than re-joining the existing one.

Step 2 of `verify/main.go` proves the fix: after `Foo[bar]` connects, `foo{bar}` is rejected with `433 ERR_NICKNAMEINUSE`.

### Why we don't add a separate timer goroutine

A common naive design: spawn `go pingLoop(session)` per connection. It runs `time.NewTicker(IdleTimeout)` and calls `s.send("PING ...")` on each tick.

This is wrong in two ways:

1. **Idle is defined by inbound traffic, not wall-clock**. A client that's chatting actively shouldn't get a PING just because IdleTimeout elapsed since connect. A timer goroutine has to listen on a "kick" channel from the read loop to reset, which doubles the synchronization surface.
2. **Goroutine leaks on disconnect**. If you forget to signal the timer goroutine to stop, you leak one per closed connection. Easy to write, trivially exploitable.

`SetReadDeadline` collapses both concerns into the read loop. There's no second goroutine, no second locking discipline, no leak. The deadline is reset by the read syscall returning successfully — exactly the "any inbound traffic" definition we want.

## Critical Thinking: keepalive is a side channel

Two design decisions in this chapter — *who initiates*, and *what counts as activity* — quietly determine your server's NAT/firewall behavior, your ability to detect cleanly-killed clients, and your latency-to-failure for an agent that crashed mid-session.

**Who initiates.** We chose server-initiates because it works for the common case (a chat client behind a NAT). But if your IRC server is itself behind a load balancer that idles connections at 60s, server-initiated PING after 120s never arrives — the LB has already RST'd the socket. The right move there is `IdleTimeout` < LB idle timeout, *or* both sides ping, *or* TCP keepalive at the OS level (`SO_KEEPALIVE` with `TCP_KEEPIDLE`).

**What counts as activity.** We treat any inbound byte as activity, including PRIVMSG echoes. That's permissive. A stricter server could require an actual PONG to a recent PING (and not credit any other line as proof-of-life). The trade-off is that a client which is actively sending traffic but somehow has TCP-level wedge will go undetected by the looser policy until the OS times out the socket. For an agent network, where "is this agent still alive" is a real authorization question (chapter 10 ties registry-mutation to KILL on the next idle cycle), the stricter policy is probably right.

**Detection latency.** A crashed agent will keep its TCP connection in `ESTABLISHED` (no RST sent) until either side notices. With our 120s default, that's up to 240s of "agent appears online but isn't." For an authorization-bearing substrate, that's too long. Two production fixes:

1. **Tighter idle window for agent connections.** Drop to ≤30s with PING after 15s. Egress cost grows linearly in `1/IdleTimeout`.
2. **Authority-bound liveness.** Chapter 10 introduces a separate mechanism — a registry mutation watcher that polls the on-chain state every N seconds and KILLs sessions whose authority has been revoked. That isn't quite the same as crash detection, but it composes: a session whose agent has been removed from the registry mid-crash will be cleaned up at the next watcher tick regardless of whether the TCP socket has been declared dead yet.

## Files

```
03-keepalive-isupport/
├── main.go                       # + idle detection via SetReadDeadline
├── state.go                      # + rfc1459 casefold()
├── commands.go                   # + 005 RPL_ISUPPORT, + PONG handler
├── parser.go, parser_test.go     # unchanged from chapter 02
├── parser-tests/msg-split.yaml   # unchanged
├── verify/main.go                # 5-step end-to-end (ISUPPORT/case/PING/PONG/broadcast)
├── verify.sh
└── README.md
```

## Next

[Chapter 04 — Retiring the toy](../04-retiring-the-toy) — we stop building from scratch. Build Ergo locally, diff what we have against `~/workspace/ergo/irc/`, and learn what a production ircd actually contains (account store, history, modules) that 700 lines of toy can never approach.
