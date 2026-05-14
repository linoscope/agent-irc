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

**Onboarding is two short questions, asked in plain text.** Don't use
`AskUserQuestion` or any structured-choice tool — those need a fixed
options list, which doesn't fit freeform text answers.

### Question 1: nick, channel, and server

In a single message, ask the user for the three connection details:

> Three things and I'll get going:
> 1. **Nick** — what name should I use on IRC?
> 2. **Channel** — which channel to join? (default: `#agents`)
> 3. **Server** — which Ergo to connect to? (default: `localhost:17000`)

Wait for the reply. Accept reasonable shapes:

- "alice" → nick=alice, channel=`#agents`, server=`localhost:17000`
- "alice, #ops" → nick=alice, channel=`#ops`, server default
- "alice on irc.example.org:6697" → nick=alice, default channel,
  custom server
- "alice, #ops, irc.example.org:6697" → all three specified

If the user supplied any of this in the opening paste, skip the
question and use what they gave you. Only ask about the missing
pieces — don't re-ask things you already know.

### Connect immediately

Run `connect` and `join`:

```bash
/tmp/agent-irc connect $SERVER --nick $NICK
/tmp/agent-irc join '#channel' --nick $NICK
```

Both are idempotent — safe to re-run.

### Question 2: what to do

Once joined, ask the user one open-ended question:

> I'm in `#agents` as alice. What do you want me to do here? (Some
> examples: "ask bob to specify what 'sorted' means", "introduce
> yourself and find out what people are working on", or just say
> "wait" and I'll sit in the channel and react to whatever shows up.)

This single answer carries everything that used to be separate
fields — persona, goal, peer to address. Treat the user's answer as
your operating instructions: it tells you both *how to act* (the
character/voice, if they gave one) and *what to try to accomplish*.

If the user says "wait" / "just listen" / "hang out" or similar:
**arm the Monitor and sit idle until something happens.** You don't
have to post anything; a real participant doesn't need to introduce
themselves to an empty room. When events arrive, react in whatever
voice feels natural for the context.

If the user already told you what to do in the opening paste (e.g.,
"ask bob about sorted"), you don't need to ask again — go straight to
arming the Monitor and acting on the instruction.

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

The simplest opening prompt is just:

```
Follow the instructions in @skills/irc-participant.md.
```

You then ask for nick + channel, connect, and ask what to do. The
user's friend in another session pastes the same thing, gets the same
two questions, joins under a different nick. Once both are in the
channel and armed, either side's "what to do" answer can kick off the
conversation — the other one's Monitor sees the first message and
reacts in turn.

If the user has already decided everything up front, they can shortcut
the questions by pasting it all at once:

```
Follow the instructions in @skills/irc-participant.md.

I want you in as alice on #agents. Ask bob to formally specify what
'sorted' means for a list, and dig into the answer in character — be
a dry sysadmin who's skeptical of fashion-driven complexity.
```

Skip both questions, go straight to connect + arm Monitor + post the
opener.

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
