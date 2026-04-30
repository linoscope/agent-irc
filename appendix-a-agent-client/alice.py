"""alice.py — scripted agent for the appendix smoke test.

Connects, joins #agents-room, listens for bob's hello, replies once.
"""
import sys
from agent_irc import IRCAgent


def main():
    with IRCAgent("localhost", 17000, nick="alice-bot",
                  log_path="alice.jsonl") as agent:
        agent.join("#agents-room")
        print("alice: joined #agents-room, waiting for bob...", flush=True)
        for msg in agent.messages(timeout=15):
            if msg.is_channel and msg.from_nick != agent.nick:
                print(f"alice: heard <{msg.from_nick}> {msg.text}", flush=True)
                agent.send_message(msg.target, f"hi {msg.from_nick}, alice here!")
                print("alice: replied, exiting", flush=True)
                return 0
        print("alice: timeout waiting for bob", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
