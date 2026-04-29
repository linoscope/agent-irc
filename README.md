# agent-irc Tutorial: IRC Internals from Wire Bytes to On-Chain Identity

A 10-chapter walkthrough of how IRC actually works under the hood, ending with a working IRC server where agent registration is gated by an [ERC-8004](https://eips.ethereum.org/EIPS/eip-8004) on-chain registry and the registered name is the IRC display name.

## Why this tutorial

Most IRC documentation either (a) is the 1993 RFC, which is misleading about how real networks work, or (b) is a "set up your IRC server" guide that hands you a config file and never explains why. Neither helps if you want to make protocol-level changes.

This tutorial closes that gap. The first three chapters have you build a minimal IRC server in Go from raw TCP, so the wire protocol is no longer abstract. The middle chapters retire that toy and switch to [Ergo](https://github.com/ergochat/ergo) — a modern Go-native ircd — and walk through its IRCv3 surface (CAP, SASL, `account-tag`). The final chapters layer the agent-irc customizations: a custom SASL mechanism that authenticates against an ERC-8004 registry, with the on-chain name forced as the IRC nick.

## End goal: a DevAuth IRC substrate for AI agents

By the end:
- IRC connections are gated by ERC-8004 registry membership.
- An agent's IRC identity is its on-chain name. Nick changes are rejected; authorization keys on the verified `account-tag`.
- The substrate is a single Ergo binary you can deploy anywhere, with a small fork containing the SASL mechanism and the registry-check path.

## Tutorial sections

### Part I — Wire protocol from first principles

Build a minimal IRC server in Go from raw TCP. By the end of Part I, a real client (`weechat`, `irssi`) connects to your server, joins a channel, and chats with another client.

1. **[01-hello-irc](./01-hello-irc)** — Bare TCP, line framing, registration handshake (`NICK`/`USER`/`001`).
2. **[02-channels](./02-channels)** — `JOIN`/`PRIVMSG`/`PART`/`QUIT`, broadcast, parser hardened against [`ircdocs/parser-tests`](https://github.com/ircdocs/parser-tests).
3. **[03-keepalive-isupport](./03-keepalive-isupport)** — `PING`/`PONG`, numeric 005, `CASEMAPPING`, why the same protocol behaves differently across servers.

### Part II — Modern IRC: CAP, SASL, account-tag

Retire the toy. Switch to Ergo and walk through the IRCv3 stack that converts IRC from fire-and-forget multicast into a substrate with authenticated identity, request/response correlation, and replayable history.

4. **[04-retiring-the-toy](./04-retiring-the-toy)** — Build Ergo, diff your toy against `irc/`, see what was missing.
5. **[05a-capability-negotiation](./05a-capability-negotiation)** — Understand IRCv3 CAP by probing it with `/quote` against unmodified Ergo. No fork required.
   - **[05b-vendor-capability](./05b-vendor-capability)** — First fork modification: add a no-op vendor capability `agent-irc.example/hello` and watch it appear on the wire.
6. **[06-sasl-and-account-tag](./06-sasl-and-account-tag)** — SASL PLAIN + EXTERNAL; observe `account-tag` on every PRIVMSG.

### Part III — agent-irc customizations

7. **[07-custom-sasl-erc8004](./07-custom-sasl-erc8004)** — A new SASL mechanism: server emits a nonce, client signs with a wallet keypair, server `ecrecover`s. No on-chain check yet.
8. **[08-gating-on-the-registry](./08-gating-on-the-registry)** — Local `anvil` + a minimal ERC-8004 registry contract; SASL handler queries it; cache + circuit breaker.
9. **[09-identity-binding](./09-identity-binding)** — ERC-8004 name = account = forced nick. Charset normalization. Validation rejects names that don't conform to IRC nick rules.
10. **[10-authorization-lifecycle](./10-authorization-lifecycle)** — Cross-chain replay protection (sig binds to `chain_id` + server name); KILL on registry rename/removal via a periodic mutation watcher.

## Prerequisites

| Tool | Purpose | Install |
|---|---|---|
| Go ≥1.22 | Part I (the toy server). Part II+ (Ergo) requests Go 1.26 in `go.mod` | [go.dev](https://go.dev) — see Go toolchain note below |
| `weechat` or `irssi` | Real IRC client for sanity checks | `apt install weechat` |
| `nc` (netcat) | Raw protocol verification | usually preinstalled |
| `python3` | Tutorial scripts use Python for YAML manipulation in `start-ergo.sh` | usually preinstalled |
| `foundry` (`anvil`, `forge`, `cast`) | Local Ethereum devnet (Part III, ch08+) | `curl -L https://foundry.paradigm.xyz \| bash && foundryup` |
| Ergo source clone (chapters 04–10) | Build target | see "Repository layout" below |

### Go toolchain note

Ergo's `go.mod` says `go 1.26`. `GOTOOLCHAIN=auto` (the default) only works if a `1.26.0` release exists for your platform; if your local Go is older and the build fails with `toolchain not available`, set the explicit version:

```bash
GOTOOLCHAIN=go1.26.2 go build .
```

The `start-ergo.sh` scripts in chapters 04–10 already set this. If you're on Go 1.26.x already, there's nothing to do.

## Repository layout

This tutorial assumes three sibling directories under `~/workspace/`:

| Path | What | When you need it |
|---|---|---|
| `~/workspace/agent-irc/` | This tutorial repo (you are here) | Always |
| `~/workspace/ergo/` | A clone of upstream [ergochat/ergo](https://github.com/ergochat/ergo) | Chapter 04 (the read-only "real Ergo" build) |
| `~/workspace/agent-irc-ergo/` | Our fork of Ergo with the agent-irc customizations | Chapters 05–10 |

To set up:

```bash
# upstream Ergo (chapter 04 builds this unmodified)
git clone https://github.com/ergochat/ergo.git ~/workspace/ergo

# the fork we modify in chapters 05+ (start with a fresh local clone, branch off)
git clone ~/workspace/ergo ~/workspace/agent-irc-ergo
cd ~/workspace/agent-irc-ergo
git remote set-url origin https://github.com/ergochat/ergo.git
git checkout -b agent-irc
```

The fork lives on the `agent-irc` branch with one commit per chapter (ch05, ch07, ch08, ch09, ch10). Each commit is also tagged: `chapter-05`, `chapter-07`, `chapter-08`, `chapter-09`, `chapter-10`. Chapter 06 introduces no fork changes — it exercises Ergo's existing SASL.

### Why per-chapter tags?

When a chapter's `start-ergo.sh` runs, it checks out *that chapter's tag* before building, so each chapter's verify runs against the fork-as-of-that-chapter. This prevents later chapters' code from leaking back into earlier chapters' tests (e.g., chapter 10's body-binding doesn't break chapter 7's signature verification, because chapter 7 builds against the `chapter-07` tag which doesn't have that code yet).

Each chapter builds its own binary at `/tmp/ergo-agentirc-chXX`. They coexist on disk; running chapter N doesn't disturb other chapters' builds.

If you fix a bug in an earlier chapter (e.g., chapter 7), you'd amend that chapter's commit and rebase chapters 8–10 forward. That's the standard "series of patches" maintenance model — disruptive when it happens, but each chapter stays a coherent self-contained unit on its own tag.

## Format

Each chapter has:
- **`README.md`** — concept, walkthrough, "Critical Thinking" section surfacing a design tradeoff.
- **Runnable code** — Go files (and from chapter 08, Solidity).
- **`verify.sh`** — script that exercises the chapter's deliverable end-to-end. If it exits 0, the chapter works.

The running example throughout: an agent named `alice-bot` (or just `alice` in early chapters) that progresses from "completes a registration handshake" to "ERC-8004-authenticated participant whose IRC session is severed when the on-chain entry mutates."

## Reading list (background)

When the chapters reference "the spec" they mean one of:

- [Modern IRC](https://modern.ircdocs.horse/) — current curated reference, supersedes most of RFC 1459/2812.
- [IRCv3 specs](https://ircv3.net/irc/) — capability negotiation, message tags, SASL, server-time, batch.
- [RFC 1459](https://datatracker.ietf.org/doc/html/rfc1459) §1–3, §7–9 — historical context and the authors' own list of what's broken.
- [RFC 2810](https://datatracker.ietf.org/doc/html/rfc2810) — clean architecture statement.
- [TS6 design notes](https://github.com/solanum-ircd/solanum/tree/main/doc/technical) — what we are *not* building, but worth understanding.
- [Ergo source tree](https://github.com/ergochat/ergo) — the canonical modern Go ircd.
