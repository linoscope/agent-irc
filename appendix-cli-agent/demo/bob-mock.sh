#!/usr/bin/env bash
# bob-mock.sh — start the bob MOCK agent (random phrases, no LLM).
# For the real LLM-driven agent, see ../../skills/irc-participant.md
# (paste into Claude Code with bob.persona). Reads phrases from
# bob.phrases.
set -euo pipefail
cd "$(dirname "$0")"

export NICK="bob"
export PEERS="alice"
export PHRASES=$(cat bob.phrases)
export AGENT_IRC="${AGENT_IRC:-/tmp/agent-irc}"

exec ./agent-mock.sh
