# Chapter 04 — Retiring the toy, switching to Ergo

Three chapters in, our server is ~1,200 lines of Go that handles a real IRC client well enough to chat. That's enough wire-level intuition to last the rest of the tutorial. From here on, we use [Ergo](https://github.com/ergochat/ergo) as the base — and modify it for our needs.

## Why we stop building from scratch

Look at the line counts.

| Component | Our toy (ch01-03) | Ergo (`irc/`) |
|---|---:|---:|
| Total Go LOC | 1,188 | 29,761 |
| Largest single file | `commands.go` (336 lines) | `handlers.go` (4,508 lines) |
| `client.go` | combined into `state.go` (~110 lines) | 2,116 lines |
| `channel.go` | inside `commands.go` (~80 lines) | 1,730 lines |
| `accounts.go` | none | 2,532 lines |

`handlers.go` alone is roughly 4× our entire toy. That single file is *just* the command dispatch table — JOIN/MODE/NICK/PRIVMSG/etc. handlers, none of the supporting infrastructure.

What 30,000 lines buys you, that our 1,200 cannot:

| Subsystem | Our toy | Ergo |
|---|---|---|
| Persistence | none | embedded BoltDB (`bunt/`, `datastore/`) for accounts, channels, vhosts |
| Account registration | none | first-class (`accounts.go`, NickServ built in) |
| Channel registration | none | ChanServ, persistent ACLs, ban/quiet/invex lists |
| IRCv3 capabilities | none | full `caps/` package with code-generated capability table |
| SASL | none | PLAIN, EXTERNAL, SCRAM-SHA-256 (`accounts.go`, with crypt support) |
| Message tags / `account-tag` / `server-time` | none | native, on every applicable line |
| `chathistory` (replayable history) | none | in-memory + optional MySQL/Postgres backend (`history/`) |
| Always-on accounts | none | `accounts.go` — server holds presence even with no socket attached |
| Modes (chan + user) | none | `modes/` package; +q +a +o +h +v with full PREFIX semantics |
| Connection limits / fakelag | none | `connection_limits/`, `fakelag.go` |
| Cloaks (host obfuscation) | none | `cloaks/` |
| WebSockets, Tor listeners, PROXY protocol | none | yes |
| TLS with SNI, OCSP, SSL client certs | none | yes |
| i18n | none | full message catalog system, 30+ languages |
| Push notifications, JWT, OAuth2 | none | yes |

We could spend six months extending the toy and still not match this. More importantly, **none of what we'd build is the interesting part of agent-irc**. The interesting part — ERC-8004 gated SASL, on-chain identity binding — fits in a few hundred lines on top of Ergo. Everything before that is plumbing we don't need to write.

## What you'll do in this chapter

1. **Build Ergo.** Single Go binary, ~16 MB. Ergo's `go.mod` requests Go 1.26. If your local Go is older, `start-ergo.sh` sets `GOTOOLCHAIN=go1.26.2` to download the right toolchain explicitly — the default `GOTOOLCHAIN=auto` looks for `1.26.0` and fails because the published patch release is `1.26.2`.
2. **Run it with a minimal config** (`ircd.yaml`) that listens on `:16670` plaintext-only, no TLS, in-memory data dir.
3. **Run the same broadcast smoke test from chapter 02** against the real binary, and observe what you get for free.
4. **Take a guided tour** of `~/workspace/ergo/irc/` so you know where to look in chapters 05–10.

## Run it

```bash
# verify.sh starts Ergo, runs the smoke test, tears down.
./verify.sh

# or manually — server in one terminal, client in another:
./start-ergo.sh
weechat -t
/server add ergo localhost/16670 -notls -autoconnect
/connect ergo
/join #room
```

Compare what Ergo emits at registration with chapter 03's output:

```diff
# chapter-03 toy:
- :irc.example 001 alice :Welcome to AgentIRC, alice
- :irc.example 005 alice NETWORK=AgentIRC CASEMAPPING=rfc1459 CHANTYPES=# PREFIX= NICKLEN=30 CHANNELLEN=64 TOPICLEN=390 :are supported by this server

# Ergo:
+ :ergo.test 001 alice :Welcome to the ErgoTest IRC Network alice
+ :ergo.test 002 alice :Your host is ergo.test, running version ergo-2.19.0-unreleased
+ :ergo.test 003 alice :This server was created ...
+ :ergo.test 004 alice ergo.test ergo-2.19.0-unreleased BERTZios CEIMRUabefhiklmnoqstuv Iabefhkloqv
+ :ergo.test 005 alice AWAYLEN=390 BOT=B CASEMAPPING=ascii CHANLIMIT=#:100 CHANMODES=Ibe,k,fl,CEMRUimnstu ... :are supported by this server
+ :ergo.test 005 alice MAXLIST=beI:100 MAXTARGETS=4 MODES MONITOR=100 MSGREFTYPES=msgid,timestamp ... PREFIX=(qaohv)~&@%+ ... :are supported by this server
+ :ergo.test 005 alice UTF8ONLY WHOX :are supported by this server
+ :ergo.test 251 alice :There are 0 users and 1 invisible on 1 server(s)
+ :ergo.test 252 alice 0 :IRC Operators online
+ :ergo.test 253-266 alice ... (LUSERS counts)
+ :ergo.test 422 alice :MOTD File is missing
+ :ergo.test 221 alice +Zi   (RPL_UMODEIS — your user modes)
```

Things to notice:

- **Three 005 lines, not one.** Ergo has too many tokens to fit on one line. Real clients reassemble.
- **`PREFIX=(qaohv)~&@%+`.** Five privilege tiers: owner/admin/op/halfop/voice. Our toy's `PREFIX=` is empty.
- **`MSGREFTYPES=msgid,timestamp`.** This is the IRCv3 message-id mechanism — every message gets a stable identifier so you can edit/delete/reply-to it. We're going to use this in chapters 06+.
- **`CASEMAPPING=ascii`** (not `rfc1459`). Ergo deliberately deviates from the legacy default; modern clients handle either.
- **MOTD, LUSERS, user-mode** numerics. Our toy elides them; many older clients hang waiting.

## A guided tour of `~/workspace/ergo/irc/`

The tree:

```
irc/
├── client.go         (2116 lines) — the per-connection state machine
├── channel.go        (1730 lines) — channel state, modes, broadcast
├── channelmanager.go (515)         — server-wide channel registry
├── handlers.go       (4508)        — every IRC command's handler
├── commands.go       (~)           — verb → handler dispatch table
├── accounts.go       (2532)        — NickServ, SASL, registration
├── chanserv.go        — ChanServ pseudo-client
├── hostserv.go        — HostServ pseudo-client
├── histserv.go        — HistServ (chathistory)
├── ircconn.go         — TCP / TLS / WebSocket frames
├── caps/              — IRCv3 capability table (code-generated from gencapdefs.py)
├── isupport/          — RPL_ISUPPORT (005) builder
├── modes/             — channel + user mode parser/applier
├── history/           — message history (chathistory backend)
├── datastore/         — BoltDB persistence layer
├── flatip/            — IP range matching for K-LINEs
├── connection_limits/ — per-IP and per-account rate limiting
├── cloaks/            — vhost / host obfuscation
├── jwt/               — token-based auth (used by extjwt CAP)
├── webpush/           — mobile push notifications
├── languages/         — i18n catalog loader
├── mysql/, postgresql/, sqlite/   — optional persistent history backends
└── ...
```

If you've absorbed chapters 01–03, the right way to read this is:

1. **Start with `ircconn.go` + `client.go`**. This is the equivalent of our `main.go` + `state.go`. The `Client` struct is what we called `session`. Find the `Run(...)` method — that's our `readLoop`.
2. **Then `handlers.go`**. This is our `commands.go`, with ~80 commands instead of 7. Skim it; you'll see your friends from chapter 02 (`joinHandler`, `privmsgHandler`, `partHandler`).
3. **`channel.go` + `channelmanager.go`** are our `channel` struct + `Server.channels` map, but with persistence, ban/invex/exception lists, modes, history, and timestamp-based merge logic (which TS6 uses for federation; Ergo uses it internally for join races).
4. **`caps/defs.go`** is generated by `python3 gencapdefs.py`. It enumerates every IRCv3 capability Ergo supports. We'll add an entry there in chapter 05.
5. **`accounts.go`** is the SASL surface. The function we'll be hooking in chapter 07 is `Server.RegisterAccount(...)` and the SASL mechanism implementations near the top of the file.

A useful first exercise: open `~/workspace/ergo/irc/handlers.go`, find `func privmsgHandler`, and trace the broadcast path through `(*Channel).SendMessage`. The shape will feel familiar — it's our chapter-02 fan-out, but with rate limiting, history persistence, and `account-tag` injection layered on.

## Configuration: what we kept and what we cut

Our `ircd.yaml` is `ergo defaultconfig` with three changes:

| What | Why |
|---|---|
| Listener: `:16670` plaintext only | We aren't testing TLS in this chapter; chapter 06 will. |
| `languages.enabled: false` | Avoids needing the i18n catalog directory; doesn't change behavior on the wire. |
| Datastore + lock paths under `./data` | Keeps state local to the chapter; `start-ergo.sh` wipes it on each run. |

We **kept** Ergo's defaults for fakelag, history, account registration, and SASL. None of those run yet (no client requests them), but they're configured and ready for chapters 05–06.

## Critical Thinking: when forking is the right move

We are about to *fork* Ergo for the agent-irc customizations. That choice is non-trivial and worth thinking through.

**Alternatives we are rejecting:**

1. **Modules / plugins.** Ergo doesn't have a runtime module system (InspIRCd does). Our changes have to be in-tree.
2. **Sidecar via S2S.** Run Ergo unmodified, link a custom services daemon over TS6 that does the ERC-8004 check. Possible but expensive: TS6 is a federation protocol, the services daemon would need a full IRC server implementation, and the auth window happens before TS6 is even established.
3. **Reverse-proxy front-door.** A custom proxy on port 6697 that does the on-chain check before forwarding to Ergo. Possible, but you lose context: Ergo doesn't know the SASL mechanism succeeded for ERC-8004 reasons, so it can't bind the account-tag.
4. **Mechanism via SASL EXTERNAL only.** Use TLS client certs whose CN encodes a wallet address. Defer the on-chain check to a wrapper. This is the *least invasive* option but couples agent identity to cert lifecycle, which is its own headache.

**Why fork:**

- The change surface is small: one new SASL mechanism (`ERC8004`) and one new package for the registry client. ~500 lines.
- It puts the on-chain check in the same trust boundary as the rest of authentication — no fragile cross-process signaling.
- Upstream rebases stay tractable as long as we don't touch unrelated files.

**The cost we're accepting:** a fork to maintain. We will inevitably want to pick up Ergo upstream changes (security fixes, IRCv3 spec advances). The discipline is: keep the fork tiny, isolated to clearly-labeled files, never modify generic ircd code if a SASL mechanism would do.

This same trade-off is what made TS6 federation a 30-year-old codebase. Forks of forks of patches. We start with a clean fork and aim to keep it that way.

## Files

```
04-retiring-the-toy/
├── ircd.yaml             # minimal Ergo config (defaultconfig + 3 tweaks)
├── start-ergo.sh         # build (if needed), reset state, run Ergo
├── go.mod                # for the verify program
├── verify/main.go        # alice + bob smoke test against real Ergo
├── verify.sh             # start Ergo, run verify, tear down
└── README.md
```

## Next

[Chapter 05 — Capability negotiation](../05-capability-negotiation) — we trace `CAP LS 302` end-to-end and add a no-op vendor capability `agent-irc.example/hello` to Ergo. This is the warm-up before the real customization in chapter 07.
