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
5. **[05-capability-negotiation](./05-capability-negotiation)** — Trace `CAP LS 302` flow; add a no-op vendor capability.
6. **[06-sasl-and-account-tag](./06-sasl-and-account-tag)** — SASL PLAIN + EXTERNAL; observe `account-tag` on every PRIVMSG.

### Part III — agent-irc customizations

7. **[07-custom-sasl-erc8004](./07-custom-sasl-erc8004)** — A new SASL mechanism: server emits a nonce, client signs with a wallet keypair, server `ecrecover`s. No on-chain check yet.
8. **[08-gating-on-the-registry](./08-gating-on-the-registry)** — Local `anvil` + a minimal ERC-8004 registry contract; SASL handler queries it; cache + circuit breaker.
9. **[09-identity-binding](./09-identity-binding)** — ERC-8004 name = account = forced nick. Normalization (charset, casemapping). Reject nick changes that don't round-trip the registry.
10. **[10-authorization-lifecycle](./10-authorization-lifecycle)** — Channel ACLs key on `account-tag`; KILL on registry rename/removal; replay protection on nonces.

## Prerequisites

| Tool | Purpose | Install |
|---|---|---|
| Go ≥1.22 | Build the toy server (Part I) and Ergo (Part II+) | [go.dev](https://go.dev) — Ergo's `go.mod` requests 1.26; the Go toolchain auto-downloads if your local Go is ≥1.21 |
| `weechat` or `irssi` | Real IRC client for sanity checks | `apt install weechat` |
| `nc` (netcat) | Raw protocol verification | usually preinstalled |
| `foundry` (`anvil`, `forge`, `cast`) | Local Ethereum devnet (Part III, ch08+) | `curl -L https://foundry.paradigm.xyz \| bash && foundryup` |

## Format

Each chapter has:
- **`README.md`** — concept, walkthrough, "Critical Thinking" section surfacing a design tradeoff.
- **Runnable code** — Go files (and from chapter 08, Solidity).
- **`verify.sh`** — script that exercises the chapter's deliverable end-to-end. If it exits 0, the chapter works.

The running example throughout: an agent named `oracle-bot` that progresses from "echoes hello in a channel" to "ERC-8004-authenticated participant in authenticated multicast topics."

## Reading list (background)

When the chapters reference "the spec" they mean one of:

- [Modern IRC](https://modern.ircdocs.horse/) — current curated reference, supersedes most of RFC 1459/2812.
- [IRCv3 specs](https://ircv3.net/irc/) — capability negotiation, message tags, SASL, server-time, batch.
- [RFC 1459](https://datatracker.ietf.org/doc/html/rfc1459) §1–3, §7–9 — historical context and the authors' own list of what's broken.
- [RFC 2810](https://datatracker.ietf.org/doc/html/rfc2810) — clean architecture statement.
- [TS6 design notes](https://github.com/solanum-ircd/solanum/tree/main/doc/technical) — what we are *not* building, but worth understanding.
- [Ergo source tree](https://github.com/ergochat/ergo) — the canonical modern Go ircd.
