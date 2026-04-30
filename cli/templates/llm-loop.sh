#!/usr/bin/env bash
# llm-loop.sh — richer LLM-driven IRC agent.
#
# Differences from guest-agent.sh:
#   - Maintains a rolling conversation context (last N messages) so the LLM
#     sees more than one inbound message at a time.
#   - Honors a max-turns counter so the agent disconnects cleanly.
#   - Passes a system prompt with a persona slot.
#
# Edit the env vars below or pass them inline:
#
#   SERVER=irc.foo:6697 NICK=mybot ROOM='#room' PERSONA='You are...' \
#       MAX_TURNS=10 ./llm-loop.sh
set -euo pipefail

SERVER="${SERVER:-localhost:17000}"
NICK="${NICK:-llm-bot}"
ROOM="${ROOM:-#agents-room}"
PERSONA="${PERSONA:-You are a thoughtful, brief agent. Reply in one short sentence.}"
MAX_TURNS="${MAX_TURNS:-5}"
CONTEXT_LINES="${CONTEXT_LINES:-12}"

# --- bring up daemon + join ---
agent-irc connect "$SERVER" --nick "$NICK" ${TLS:+--tls}
agent-irc join "$ROOM"

# --- conversation buffer ---
context_file=$(mktemp)
turns_taken=0
trap 'rm -f "$context_file"' EXIT

# --- main loop ---
agent-irc tail "$ROOM" --follow --skip-self | while read -r event; do
  [[ $(jq -r .event <<<"$event") == message ]] || continue
  from=$(jq -r .from <<<"$event")
  text=$(jq -r .text <<<"$event")

  # Append to context, keep only the last N lines.
  printf '<%s> %s\n' "$from" "$text" >> "$context_file"
  tail -n "$CONTEXT_LINES" "$context_file" > "$context_file.tmp"
  mv "$context_file.tmp" "$context_file"

  # Build the prompt and ask the LLM.
  prompt=$(printf 'You are %s.\n\nPersona: %s\n\nRecent messages:\n%s\n\nYour reply (just the text, no quotes, no role-play):' \
                  "$NICK" "$PERSONA" "$(cat "$context_file")")
  reply=$(printf '%s' "$prompt" | claude --print)
  reply=$(printf '%s' "$reply" | tr -d '\r' | head -c 400)
  [[ -z "$reply" ]] && continue

  printf '<%s> %s\n' "$NICK" "$reply" >> "$context_file"
  agent-irc send "$ROOM" "$reply"

  turns_taken=$((turns_taken + 1))
  if [[ $turns_taken -ge $MAX_TURNS ]]; then
    echo "[$NICK] max turns reached, exiting" >&2
    agent-irc send "$ROOM" "(stepping away — see you next time)"
    agent-irc quit
    exit 0
  fi
done
