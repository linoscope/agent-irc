"""agent_runner.py — drive an IRC agent with the `claude` CLI.

Each inbound channel message triggers a `claude --print` invocation that
returns the agent's next utterance. The same `system-prompt` text used here
is what the README hands to a human pasting into a Claude Code session.

Usage:
    python3 agent_runner.py \\
        --nick alice-bot \\
        --persona "You are alice, a curious agent. Keep replies under 20 words." \\
        --max-turns 4

A turn is one inbound channel message that the agent responds to. The agent
exits cleanly when it hits max-turns or detects an `<EXIT>` token in any
LLM output.
"""
from __future__ import annotations

import argparse
import os
import subprocess
import sys
import time

from agent_irc import IRCAgent, Message


SYSTEM_PROMPT_TEMPLATE = """\
You are an autonomous IRC chat agent named `{nick}`.

Persona: {persona}

You are in IRC channel `{channel}` with one or more peer agents. You will be
shown recent messages and asked for your next reply. Output ONLY the text you
want to say in the channel — no quotes, no "Alice:" prefix, no markdown
formatting, no role-play stage directions like "*waves*". Reply in 1-2 short
sentences.

If you decide the conversation has reached a natural conclusion, end your
reply with the literal token <EXIT> on its own line and you will disconnect.

Recent messages (oldest first):
{history}

Your reply (as `{nick}`):"""


def ask_claude(prompt: str, timeout: float = 60.0) -> str:
    """Invoke `claude --print` with the prompt and return its trimmed response."""
    res = subprocess.run(
        ["claude", "--print", prompt],
        capture_output=True,
        text=True,
        timeout=timeout,
    )
    if res.returncode != 0:
        sys.stderr.write(f"claude failed: {res.stderr}\n")
        return ""
    return res.stdout.strip()


def format_history(history: list[Message], my_nick: str) -> str:
    """Render the conversation buffer as plain lines for the prompt."""
    lines = []
    for m in history[-20:]:
        speaker = "you" if m.from_nick == my_nick else m.from_nick
        lines.append(f"<{speaker}> {m.text}")
    return "\n".join(lines) if lines else "(no messages yet)"


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--nick", required=True)
    p.add_argument("--persona", required=True, help="One-sentence persona for the LLM.")
    p.add_argument("--channel", default="#agents-room")
    p.add_argument("--host", default="localhost")
    p.add_argument("--port", type=int, default=17000)
    p.add_argument("--max-turns", type=int, default=4,
                   help="Stop after this many of OUR replies (not total messages).")
    p.add_argument("--initial-message", default=None,
                   help="Optional opening line for this agent to say after joining.")
    p.add_argument("--log", default=None, help="JSONL log path.")
    args = p.parse_args()

    log_path = args.log or f"{args.nick}.jsonl"

    history: list[Message] = []
    turns_taken = 0

    with IRCAgent(args.host, args.port, nick=args.nick, log_path=log_path) as agent:
        agent.join(args.channel)
        print(f"[{args.nick}] joined {args.channel}", flush=True)

        if args.initial_message:
            time.sleep(0.5)  # let other agents JOIN before the first message
            agent.send_message(args.channel, args.initial_message)
            history.append(Message(
                from_nick=args.nick, target=args.channel,
                text=args.initial_message, raw="",
            ))

        for msg in agent.messages(timeout=60):
            if msg.target != args.channel:
                continue
            if msg.from_nick == args.nick:
                # Echo of our own message (Ergo doesn't echo by default but
                # be defensive in case echo-message gets enabled later).
                continue

            history.append(msg)
            print(f"[{args.nick}] heard <{msg.from_nick}> {msg.text}", flush=True)

            if turns_taken >= args.max_turns:
                print(f"[{args.nick}] reached max-turns, exiting", flush=True)
                break

            prompt = SYSTEM_PROMPT_TEMPLATE.format(
                nick=args.nick,
                persona=args.persona,
                channel=args.channel,
                history=format_history(history, args.nick),
            )
            reply = ask_claude(prompt)
            if not reply:
                print(f"[{args.nick}] empty reply, skipping", flush=True)
                continue

            should_exit = False
            if "<EXIT>" in reply:
                reply = reply.replace("<EXIT>", "").strip()
                should_exit = True

            if reply:
                agent.send_message(args.channel, reply)
                history.append(Message(
                    from_nick=args.nick, target=args.channel,
                    text=reply, raw="",
                ))
                turns_taken += 1
                print(f"[{args.nick}] said: {reply}", flush=True)

            if should_exit:
                print(f"[{args.nick}] LLM signaled exit, leaving", flush=True)
                break

    return 0


if __name__ == "__main__":
    sys.exit(main())
