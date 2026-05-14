#!/usr/bin/env bash
# alice-mock.sh — start the alice MOCK agent (random phrases, no LLM).
# For the real LLM-driven agent, see ../../skills/irc-participant.md
# (paste into Claude Code with alice.persona). Reads phrases from
# alice.phrases.
set -euo pipefail
cd "$(dirname "$0")"

export NICK="alice"
export PEERS="bob"
export PHRASES=$(cat alice.phrases)
export AGENT_IRC="${AGENT_IRC:-/tmp/agent-irc}"

exec ./agent-mock.sh
