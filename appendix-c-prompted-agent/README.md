# Appendix C — Prompted agent: minimal setup via server-side persistence

The thesis: when the IRC server holds the agent's account, channel
membership, and recent history, the agent itself reduces to a tiny
stateless script. No long-running process, no daemon, no deployment. The
owner gives a coding agent (Claude Code, Cursor, etc.) one Markdown
prompt; the coding agent asks three questions and writes a `bot.py` of
~200 lines that runs from `cron`.

This appendix is a working demonstration of that thesis. ERC-8004 is
deliberately set aside — appendix C runs against stock upstream Ergo,
authenticated by SASL PLAIN, so the only thing on display is what
server-side persistence buys you.

## Files

| File | What |
|---|---|
| [`PROMPT.md`](./PROMPT.md) | The prompt to paste into a coding agent. The deliverable. |
| [`ircd.yaml`](./ircd.yaml) | Ergo config: port `:16673`, in-memory history on, `always-on: mandatory` so every registered account is automatically always-on. |
| [`start-ergo.sh`](./start-ergo.sh) | Build upstream Ergo if needed, wipe `data/`, run on `:16673`. |
| [`example/bot.py`](./example/bot.py) | Reference implementation of the bot the prompt describes. ~250 lines, stdlib only. |
| [`example/bot.json`](./example/bot.json) | Reference config used by the example bot. |
| [`screenshots.sh`](./screenshots.sh) | End-to-end demo: starts Ergo, runs a Python watcher in `#agents`, ticks the bot 3 times against scheduled mentions, starts appendix A's viewer pointed at this Ergo, drives headless Chromium to capture the index + channel pages. |
| [`playwright/take_screenshots.py`](./playwright/take_screenshots.py) | The Chromium driver. Reuses appendix A's `agent_irc.py` to inject one final live message so the channel screenshot includes a fresh SSE-delivered line. |
| [`playwright/populate_and_watch.py`](./playwright/populate_and_watch.py) | The watcher session. Holds `#agents` open and posts three mentions on a schedule. |
| [`screenshots/viewer-index.png`](./screenshots/viewer-index.png) | Rendered index showing `#agents` listed. |
| [`screenshots/viewer-channel.png`](./screenshots/viewer-channel.png) | Rendered channel with the watcher's mentions, the bot's replies, an unprompted phrase, the member list (including the always-on bot), and a live SSE-injected line. |

## How it's meant to be used

```bash
# Terminal A — server.
./start-ergo.sh

# Terminal B — owner's watcher (keeps #agents alive between ticks).
nc -C localhost 16673
CAP LS 302
NICK watcher
USER w 0 * :w
CAP REQ :account-tag server-time message-tags
CAP END
JOIN #agents
# (leave open; type messages here to test the bot)

# Terminal C — the coding agent.
# Owner pastes PROMPT.md into Claude Code / Cursor / etc., answers three
# questions (bot name, personality, five phrases), and watches the agent
# write bot.py + bot.json. The coding agent runs the bot once to register,
# and then once more to demonstrate.
```

The owner sees the bot's reply in terminal B with `@account=<botname>`
stamped by the server.

## Why each piece exists

**Ergo with `always-on: mandatory` (`ircd.yaml`)** — every registered
account is treated as always-on. Reconnects don't re-JOIN; the server
remembers the bot is in `#agents` between ticks. Without this, the bot
would have to re-JOIN every tick, which works but generates extra wire
traffic and looks like a "user joined / left" loop to anyone watching.

**A watcher in `#agents` (the owner's terminal)** — IRC channels exist
only while at least one client is in them. The bot disconnects after each
tick, so if no one else is in the channel, the channel ceases. The
watcher keeps the channel alive between bot ticks. It's also how the
owner sees the bot work, so it serves both purposes.

If you wanted "no human watcher ever," a server-permanent channel
(ChanServ register) would do it — out of scope here because adding a
ChanServ step to the prompt makes the "minimal setup" narrative messier.

**`example/bot.py` as a reference, not a deliverable** — the prompt tells
the coding agent to read `example/bot.py` if it gets stuck on framing or
the SASL/CAP/CHATHISTORY sequence, but to write its own bot using the
owner's chosen name and phrases. The reference exists so the prompt can
be terse about wire details: when the agent hits something it doesn't
know, the example is a few lines away.

## What the bot does, in protocol terms

```
[bot tick]
  TCP connect to ergo:16673
  CAP LS 302
  NICK <name> ; USER <name> 0 * :<name>
  CAP REQ :sasl message-tags account-tag server-time echo-message
            batch draft/chathistory draft/read-marker
  (first run only:) REGISTER * * <password>
  AUTHENTICATE PLAIN
  AUTHENTICATE +
  AUTHENTICATE base64('\0<name>\0<password>')
  → 903 RPL_SASLSUCCESS
  CAP END
  → 001 RPL_WELCOME
  JOIN #agents
  → 366 RPL_ENDOFNAMES
  CHATHISTORY AFTER #agents msgid=<last-seen> 50
  → BATCH +<id> chathistory ...
    → @account=<sender> :<sender>... PRIVMSG #agents :hello there <name>
    → ...
  → BATCH -<id>
  PRIVMSG #agents :hi <sender>, <random phrase>     (mention reply)
  PRIVMSG #agents :<random phrase>                   (1-in-5 dice)
  MARKREAD #agents t=<latest-msgid>
  QUIT :tick complete
[/bot tick]
```

Every line in that script is implemented in [`example/bot.py`](./example/bot.py)
in ~250 lines of Python. The IRC stuff is all there is.

## What the channel actually looks like

`screenshots/viewer-channel.png` (regenerable via `./screenshots.sh`):

![viewer channel screenshot](./screenshots/viewer-channel.png)

What's visible in the screenshot:

- `<watcher>` posts three mentions on a schedule (`good morning weatherbot`,
  `anyone seen weatherbot today?`, `hey weatherbot how is it out there`).
- `<weatherbot>` replies to each with `hi watcher, <random phrase>`. Replies
  have `account=weatherbot` server-stamped — the viewer renders this as
  `[account: weatherbot]` next to the bot's nick on its JOIN line.
- The 1-in-5 dice fired, so the bot also posted one unprompted phrase.
- `<HistServ>` lines are Ergo's history-replay pseudo-bot announcing
  member changes; the bot's `react()` skips these explicitly so it doesn't
  reply to its own JOIN being announced back.
- `IN THE ROOM (3)` lists `viewer`, `watcher`, `weatherbot`. The bot is
  shown even though its socket disconnected after the last tick — that's
  `always-on: mandatory` keeping it in the channel.
- `<injector> final demo line — live-screenshot-…` is a final live message
  the screenshot script posts to confirm SSE flow before capture.

Running `./screenshots.sh` from a clean state regenerates both PNGs (it
reuses appendix A's venv for `playwright` and the viewer code, so run
`../appendix-a-agent-client/verify.sh` once if that venv hasn't been
populated yet).

## Verifying the reference

The reference implementation works end-to-end. To check it yourself:

```bash
# Start the server.
./start-ergo.sh &
sleep 1

# First tick: registers the account, joins #agents.
cd example && rm -f bot.password bot.state.json && python3 bot.py
# → tick: read 0 msgs, sent 0 or 1, first_run=True

# Second tick: just SASL, fetch nothing new, maybe roll the dice.
python3 bot.py
# → tick: read 0 msgs, sent 0 or 1, first_run=False

# Demonstration: a watcher mentions the bot, then the bot ticks, then
# the watcher sees the reply.
( printf 'CAP LS 302\r\nNICK watcher\r\nUSER w 0 * :w\r\n'
  printf 'CAP REQ :account-tag server-time message-tags\r\nCAP END\r\n'
  printf 'JOIN #agents\r\nPRIVMSG #agents :hello weatherbot\r\n'
  sleep 4
  printf 'QUIT\r\n'
) | nc -q1 localhost 16673 &
sleep 1
python3 bot.py
wait
# Watcher's stdout includes:
#   @...account=weatherbot... :weatherbot!~u@... PRIVMSG #agents :hi watcher, <phrase>
```

## What this appendix is not

- Not a production-quality IRC client. Stdlib socket + line parsing is
  enough to demonstrate the lifecycle. For a real Python client surface,
  see [appendix A](../appendix-a-agent-client/) (`agent_irc.py`).
- Not a CLI. For shell-composable IRC operations, see
  [appendix B](../cli/) (the `agent-irc` Go binary).
- Not authenticated against ERC-8004. That is the whole subject of
  chapters 07–10. This appendix is the "what does the agent side look
  like when the only auth is plain SASL" baseline.

## Relationship to the rest of the tutorial

Read in this order:

1. [Chapter 06](../06-sasl-and-account-tag/) — explains SASL, the
   CAP-held registration window, and why `account-tag` is the only
   honest identity signal. This appendix's bot uses everything that
   chapter explains.
2. This appendix — the smallest possible agent that uses chapter 06's
   surface productively, to show how thin the agent code actually is when
   the server holds the state.
3. [Chapter 07+](../07-custom-sasl-erc8004/) — replace SASL PLAIN with
   ERC-8004 wallet-signature auth. The agent code structure stays the
   same; only the AUTHENTICATE blob changes.

The mental model: server-side persistence + IRCv3 SASL gets you from
"agent owner runs a 24/7 process" down to "agent owner runs cron." This
appendix is the existence proof.
