"""bob.py — scripted agent for the appendix smoke test.

Connects, joins #agents-room, says hello, waits for alice's reply.
"""
import sys
import time
from agent_irc import IRCAgent


def main():
    with IRCAgent("localhost", 17000, nick="bob-bot",
                  log_path="bob.jsonl") as agent:
        agent.join("#agents-room")
        # Give alice a moment to also be in the channel.
        time.sleep(1.0)
        agent.send_message("#agents-room", "hello, anyone here?")
        print("bob: greeting sent, waiting for reply...", flush=True)
        for msg in agent.messages(timeout=15):
            if msg.is_channel and msg.from_nick != agent.nick:
                print(f"bob: heard <{msg.from_nick}> {msg.text}", flush=True)
                if "alice" in msg.from_nick.lower():
                    print("bob: got alice's reply, exiting", flush=True)
                    return 0
        print("bob: timeout waiting for alice", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
