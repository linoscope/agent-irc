#!/usr/bin/env bash
# agent-mock.sh — a MOCK IRC "agent" in pure bash on top of agent-irc.
#
# This is NOT a real agent. It picks random phrases from $PHRASES — there is
# no LLM, no reading of what was said, no content awareness. It exists only
# to exercise the protocol mechanics end-to-end (connect, join, tail, send,
# mention-trigger, anti-loop) without dragging an LLM dependency into the
# protocol-side smoke test. For an actual content-aware agent, see the
# irc-participant skill (../../skills/irc-participant.md) which primes
# Claude Code to drive the same CLI.
#
# Reads its config from environment so the same script can run as alice or
# bob (or anyone else). Behaviour:
#
#   - connects (idempotent — second invocation attaches to the running daemon),
#   - joins $CHANNEL,
#   - tails it with --follow --skip-self,
#   - on each inbound message, replies "hi <them>, <random phrase>" if we are
#     mentioned (the phrase is random — the reply does NOT relate to what
#     was said),
#   - posts an unprompted phrase ~30% of the time, sometimes addressed at a
#     peer (to seed cross-bot conversations).
#
# This is the entire bot. ~25 lines of behaviour on top of the CLI.
#
# Environment:
#   NICK         this agent's nick on IRC      (e.g. alice)
#   SERVER       host:port of Ergo             (default: localhost:17000)
#   CHANNEL      where to live                  (default: #agents)
#   PEERS        space-separated peer nicks    (default: empty)
#   PHRASES      newline-separated phrases     (default: a few stock lines)
set -uo pipefail

NICK="${NICK:?NICK required}"
SERVER="${SERVER:-localhost:17000}"
CHANNEL="${CHANNEL:-#agents}"
PEERS="${PEERS:-}"
PHRASES="${PHRASES:-just thinking out loud
keeping an eye on things
hello channel
nothing much going on
all quiet over here}"

AGENT_IRC="${AGENT_IRC:-agent-irc}"

random_phrase() { shuf -n 1 <<<"$PHRASES"; }
random_peer()   { [[ -z "$PEERS" ]] && return; tr ' ' '\n' <<<"$PEERS" | shuf -n 1; }

shutdown() {
    [[ -n "${SPONTANEOUS_PID:-}" ]] && kill "$SPONTANEOUS_PID" 2>/dev/null
    "$AGENT_IRC" quit --nick "$NICK" 2>/dev/null
    exit 0
}
trap shutdown EXIT INT TERM

"$AGENT_IRC" connect "$SERVER" --nick "$NICK" || { echo "[$NICK] connect failed" >&2; exit 1; }
"$AGENT_IRC" join    "$CHANNEL" --nick "$NICK" || { echo "[$NICK] join failed" >&2;    exit 1; }
echo "[$NICK] connected, joined $CHANNEL — listening" >&2

# Background loop: post unprompted lines, sometimes peer-addressed.
(
    while true; do
        sleep $((2 + RANDOM % 3))                 # 2-4s between rolls
        (( RANDOM % 10 < 5 )) || continue          # ~50% chance per roll
        phrase=$(random_phrase)
        peer=$(random_peer)
        if [[ -n "$peer" ]] && (( RANDOM % 10 < 6 )); then
            "$AGENT_IRC" send "$CHANNEL" "hey $peer, $phrase" --nick "$NICK"
        else
            "$AGENT_IRC" send "$CHANNEL" "$phrase" --nick "$NICK"
        fi
    done
) &
SPONTANEOUS_PID=$!

# Foreground loop: react to mentions.
"$AGENT_IRC" tail "$CHANNEL" --nick "$NICK" --follow --skip-self | while read -r event; do
    [[ -z "$event" ]] && continue
    kind=$(jq -r .event <<<"$event" 2>/dev/null || echo "")
    [[ "$kind" == "message" ]] || continue
    from=$(jq -r .from <<<"$event")
    text=$(jq -r .text <<<"$event")
    [[ "$from" == "$NICK" ]] && continue
    # Skip messages that are already greetings to us — those are replies to
    # an earlier mention. The reply format we send is "hi <nick>, <phrase>";
    # if we reacted to those we'd loop indefinitely. Allow "hey nick, …" and
    # other peer-calls (those genuinely warrant a reply).
    if [[ "${text,,}" =~ ^hi\ ${NICK,,}[,\ !.] ]]; then
        continue
    fi
    if grep -qi -- "$NICK" <<<"$text"; then
        # Reply with 70% probability — adds some quietness so chains
        # naturally die out instead of saturating the channel.
        (( RANDOM % 10 < 7 )) || continue
        "$AGENT_IRC" send "$CHANNEL" "hi $from, $(random_phrase)" --nick "$NICK"
    fi
done
