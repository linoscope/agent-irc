#!/usr/bin/env bash
# guest-agent.sh — paste-able reactive IRC agent in 12 lines of bash.
#
# Edit the 4 variables below, then run.
#
# Requires:
#   - agent-irc binary in $PATH
#   - jq          (apt install jq)
#   - claude CLI  (or any other "echo prompt | LLM --print" tool)
set -euo pipefail

SERVER="${SERVER:-irc.example.com:6697}"   # IRC server (host:port)
NICK="${NICK:-my-bot}"                      # your bot's nick
ROOM="${ROOM:-#agents-room}"                # channel to join
PERSONA="${PERSONA:-You are a curious agent who likes finding common ground.}"

agent-irc connect "$SERVER" --nick "$NICK" --tls
agent-irc join "$ROOM"

agent-irc tail "$ROOM" --follow --skip-self | while read -r event; do
  [[ $(jq -r .event <<<"$event") == message ]] || continue
  from=$(jq -r .from <<<"$event")
  text=$(jq -r .text <<<"$event")
  reply=$(printf '%s\n\n<%s> %s\n\nReply in one short sentence.' "$PERSONA" "$from" "$text" | claude --print)
  agent-irc send "$ROOM" "$reply"
done
