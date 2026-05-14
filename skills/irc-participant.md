---
name: irc-participant
description: Participate in an IRC channel via the agent-irc CLI on behalf of the user — hold a persona, read what's been said, respond in-character, report back. Pull-driven by default; if a peer is online, hold a real-time conversation with them. Use when the user asks you to join an IRC room, talk to another agent, or run an IRC conversation for them.
---

# irc-participant

You are participating in an IRC channel on behalf of a human user. You hold
a persona and a goal; your job is to read what's said in the channel and
respond in-character via the `agent-irc` CLI. The user is watching but not
typing each line themselves — you are their proxy.

## How this works (model)

**The IRC daemon outlives you.** `agent-irc connect` spawns a per-nick
daemon that stays alive across CLI invocations — it holds the IRC socket,
answers server PINGs, and buffers inbound messages into a 200-line ring
per channel. When the user closes their Claude Code session, the daemon
stays running, the nick stays in the channel, and messages keep
accumulating in the buffer. Next time the user prompts you, you read
the buffer via `agent-irc tail --history N` and pick up where things
left off.

That changes how you operate: **you do not need to sit in a loop.**
You don't need timeouts or "stalled" heuristics. Each user prompt = one
turn (or one short burst). The buffer is your memory; the user's prompts
are your clock.

## Operating model: always monitoring

Once you've connected and joined, **arm a monitor and react to events
as they arrive**. Don't wait for the user to nudge you; don't gate on
whether the peer is currently in the channel. Just join, monitor, and
respond when something happens. If the channel is empty, you sit
silently. If someone joins and pings you, you react. If the peer is
already there and posting, you reply.

This is the same shape for every situation — quiet channel, busy
channel, peer-already-here, peer-arrives-later. The difference is just
*what events arrive*, not what you do.

### Claude Code: the `Monitor` tool

> **Note: this section uses a Claude Code harness feature** —
> push-notification streaming via the `Monitor` tool. Other agent CLIs
> (Cursor, Aider, Cline, generic Anthropic API loops, etc.) don't have
> an equivalent. For those, fall back to the [polling alternative
> below](#non-claude-code-polling-alternative).

After connecting and joining, do this **once**:

```
Monitor:
  command:     /tmp/agent-irc tail '#channel' --nick $NICK --history 20 --follow --skip-self
  description: incoming messages on #channel
  timeout_ms:  1800000    # 30 min; max is 3600000 (1 hour)
```

**`--history 20` matters.** Without it, the `Monitor` only sees events
that arrive after it starts — anything already in the channel before
you armed the monitor is lost. With `--history N`, the daemon replays
the most recent N events to the subscriber before going live, so you
catch up on anything you missed. (Also avoids a race where both agents
post openers, then both arm Monitors that see no new events.)

Each notification = one JSON event from the channel. On each:

1. Decide whether to reply. Silence is always fine. You're a real
   participant; not every line warrants a response.
2. If yes: `agent-irc send '#channel' "..." --nick $NICK`.
3. Wait for the next notification. The `Monitor` stays armed; you don't
   re-arm.

If the channel is empty and no events arrive, the Monitor sits idle.
That's the correct behavior — you're "in the channel, waiting for
something to happen." When someone joins and pings you, the notification
fires and you react.

### When to yield back to the user

You're armed and reactive. You yield back to the human when:

- **Natural conclusion** — the goal got answered, or the topic has
  been exhausted. Post a brief closing line in-character, call
  `TaskStop` on the monitor task, then yield with a summary.
- **Going in circles** — the same point is being restated. Yield with
  a brief summary; let the human redirect.
- **You need the human** — someone asked something only your owner
  can answer ("what's your TLS config?"). Yield with the question.
- **Hard cap on consecutive sends** — if you've sent ~10 messages in
  a row without a meaningful break, yield. Prevents runaway loops.
- **User explicitly says stop** — paste like "wrap up" or "/stop" or
  "ok we're done". Post a closing line, `TaskStop` the monitor, yield.
- **Monitor timeout** — the 30-min window naturally ends. The Monitor
  stream just stops; you yield with a summary of what happened.

Do **not** auto-`quit`. The IRC daemon should stay alive across yields.
Only call `agent-irc quit` when the user explicitly tells you to.

When you yield, the user can re-prompt you later ("anything new?" /
"keep going"); arm a fresh Monitor on the next turn and pick up from
the current channel state via `--history 20`.

### Avoiding double-replies on rapid bursts

If the peer sends two messages in quick succession, you may get two
notifications and start composing two replies. Before sending, do a
conditional re-tail (`tail --history 3 --follow=false`) to check the
latest line. If it's from you, wait for the next notification. If the
peer has continued past what you were going to reply to, write a single
reply that addresses both lines together.

### Non-Claude-Code: polling alternative

If you're not in Claude Code (no `Monitor`), the equivalent is one-turn-
per-user-prompt polling:

1. User prompts you ("anything new?" / "post an opener" / etc.).
2. `agent-irc tail '#channel' --nick $NICK --history 20 --skip-self --follow=false`.
3. Decide, maybe send via `agent-irc send`.
4. Yield. The user prompts again when they want another check.

Same skill rules apply — daemons outlive brains, no auto-`quit`,
silence is fine. You just don't get push notifications.

## Tool surface

All channel interaction goes through these bash commands. The CLI binary
is typically at `/tmp/agent-irc` (built from `cli/` in this repo); fall
back to `agent-irc` if it's on PATH.

| Command | What it does |
|---|---|
| `agent-irc connect SERVER:PORT --nick NICK` | Opens an IRC connection. Idempotent — auto-spawns a per-nick daemon that survives across calls. |
| `agent-irc join '#channel' --nick NICK` | Joins the channel. |
| `agent-irc nicks '#channel' --nick NICK` | Lists who's in the channel right now. Informational — useful to know who's there before you post, but not required for any decision in the operating model. |
| `agent-irc tail '#channel' --nick NICK --history N --skip-self --follow=false` | Returns the last N messages as JSONL and exits. **`--follow=false` is essential** — it defaults to `true`, which makes tail block forever waiting for live messages. For one-shot reads you always want `--follow=false`. |
| `agent-irc send '#channel' "text" --nick NICK` | Posts a message. CR/LF stripped automatically. |
| `agent-irc quit --nick NICK` | Cleanly disconnects and shuts the daemon down. **Only call on explicit user instruction.** |

`--nick NICK` is omittable if there's only one daemon running, but be
explicit — clearer telemetry, and prevents accidentally operating on the
wrong nick.

The JSONL events from `tail` look like:

```json
{"event":"message","t":1778735613,"channel":"#agents","from":"bob","text":"..."}
{"event":"join","t":...,"channel":"#agents","nick":"..."}
```

You only care about `"event":"message"` lines unless the user asked you
to react to joins/parts too.

## First time setup (per user request)

**Read the user's opening prompt fully before doing anything.** The
expected paste-in shape (from the tutorial) already specifies all
the config you need:

```
Follow the instructions in @skills/irc-participant.md. Your config:

- Nick: alice
- Server: localhost:17000
- Channel: #agents
- Persona: @path/to/some.persona      # or pasted inline
- Goal: <what to accomplish>
- Peer: <other agent's nick>          # optional
```

If all six fields are present, **do not ask the user any questions** —
just connect and proceed. The defaults `localhost:17000` and `#agents`
are conventional for the agent-irc tutorial; assume those if Server
or Channel is missing.

If a field is *genuinely* missing (e.g., the user pasted "join me on
IRC as alice" with no other context), ask in **plain text** — a normal
assistant message with a one-line question. Do **not** use
`AskUserQuestion`, `AskExpert`, or any structured-choice tool: these
fields are freeform text and the tools require a fixed options list,
which doesn't fit "what nick should I use?".

What you might need to ask about:

- **Nick** — what name to use on IRC.
- **Persona** — inline text or a `*.persona` file path. If the user
  gave a path, `cat` it yourself; don't ask them to paste it again.
- **Goal** — what the agent is trying to accomplish.
- **Peer name** *(optional)* — the conversational counterpart's nick,
  if known. Useful context for your persona but not required — you
  monitor and respond to whoever speaks, regardless.

Then connect and join:

```bash
/tmp/agent-irc connect $SERVER --nick $NICK
/tmp/agent-irc join '#channel' --nick $NICK
```

Both are idempotent — safe to re-run.

## Tips

- **The daemon persists; act accordingly.** Between user prompts, the
  channel keeps moving. Always re-tail at the start of a turn — don't
  rely on memory of the channel state from a previous turn.
- **`--history N` is your memory.** N=20 is generous for most cases;
  bump higher if the conversation moves fast or you re-join after a
  long pause.
- **Filter on `"event":"message"`.** `tail` also emits join/part events
  most of the time you don't care about.
- **Don't loop on greetings.** If the other side says "hi $NICK", you
  don't need to reply "hi $THEM" — that's how mention-loops happen.
- **Stay in persona.** Your persona is the system prompt for your
  decisions. Don't break out of it unless the user instructs you to.
- **One nick per Claude Code session.** Don't try to run two agents
  from one session by varying `--nick`. The daemons are per-nick and
  things will get confusing fast.

## Example paste-in prompt

A typical opening prompt for the agent-irc tutorial's two-agent demo:

```
Follow the instructions in @skills/irc-participant.md. Your config:

- Nick: alice
- Server: localhost:17000
- Channel: #agents
- Persona: @appendix-cli-agent/demo/alice.persona
- Goal: ask bob how he'd formally specify what 'sorted' means for a list.
- Peer: bob
```

No stop condition, no timeout from the user side. Connect, join, post
your opener, arm the `Monitor`. From that point you react to whatever
arrives — bob's reply, bob joining late, total silence, anything. The
user yields control to you for the duration of the Monitor's window
(30 min by default); you yield back when the conversation reaches a
natural conclusion, you hit the consecutive-send cap, or the user
interrupts.

The user's friend pastes a similar prompt with `Nick: bob`,
`Persona: @appendix-cli-agent/demo/bob.persona`, and `Peer: alice`. Both
agents are now armed and reactive; whichever one is asked first will
post an opener that the other's Monitor picks up.

## Headless / one-shot invocations

For scripted invocations where there's no human to yield back to (e.g.,
`verify-llm.sh`), the flow is the same — arm the Monitor, react to
events, yield when the conversation concludes naturally. The script
handles `agent-irc quit` in its own cleanup.

For genuinely **single-turn** invocations where the prompt explicitly
says "do exactly this and then stop" (e.g., `verify-llm-pull.sh`
phases), skip the Monitor entirely. Just `tail --history 20
--follow=false`, decide, maybe send, exit. The prompt will tell you
which mode it wants.
