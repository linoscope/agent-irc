#!/usr/bin/env bash
# verify-llm-pull.sh — opt-in E2E test for the pull / async / offline-then-
# online flow.
#
# Tests the skill's single-turn invocation path (from the "Headless /
# one-shot invocations" section): each invocation does exactly one turn
# (tail history, decide, maybe send) and exits, while the per-nick
# daemon stays alive holding the IRC seat. Three sequential `claude
# --print` invocations stand in for three human-prompted turns:
#
#   1. alice connects, asks bob a question, exits.
#   2. bob comes online, reads channel history, sees alice's question,
#      replies, exits.
#   3. alice's "user" prompts her again — she reads, sees bob's reply,
#      responds, exits.
#
# Assertion: monitor captures alice's question, bob's reply, and alice's
# follow-up — three exchanges across three independent `claude --print`
# invocations.
#
# Sister script to verify-llm.sh (which tests the always-monitoring /
# live-chat path). Same prerequisites: claude CLI on PATH, API tokens,
# non-deterministic.

set -uo pipefail
cd "$(dirname "$0")"
REPO_ROOT="$(cd .. && pwd)"

CLI_SRC="../cli"
BIN="${AGENT_IRC_BIN:-/tmp/agent-irc}"
SKILL="$REPO_ROOT/skills/irc-participant.md"

ERGO_LOG=$(mktemp)
ALICE_PHASE1_LOG=$(mktemp)
BOB_PHASE2_LOG=$(mktemp)
ALICE_PHASE3_LOG=$(mktemp)
MONITOR_LOG=$(mktemp)
ERGO_PID=""
MONITOR_TAIL_PID=""

cleanup() {
    [[ -n "$MONITOR_TAIL_PID" ]] && kill "$MONITOR_TAIL_PID" 2>/dev/null
    "$BIN" quit --nick alice   2>/dev/null || true
    "$BIN" quit --nick bob     2>/dev/null || true
    "$BIN" quit --nick monitor 2>/dev/null || true
    pkill -f "agent-irc daemon" 2>/dev/null || true
    [[ -n "$ERGO_PID" ]] && kill "$ERGO_PID" 2>/dev/null
    sleep 0.3
    echo
    echo "=== logs preserved for inspection ==="
    echo "  alice phase 1: $ALICE_PHASE1_LOG"
    echo "  bob phase 2:   $BOB_PHASE2_LOG"
    echo "  alice phase 3: $ALICE_PHASE3_LOG"
    echo "  monitor:       $MONITOR_LOG"
}
trap cleanup EXIT INT TERM

# --- prereqs ---------------------------------------------------------------

echo "=== 0. checking prerequisites ==="
command -v claude >/dev/null || {
    echo "FAIL: claude CLI not on PATH."
    exit 1
}
[[ -f "$SKILL" ]] || {
    echo "FAIL: skill not found at $SKILL"
    exit 1
}
echo "  claude CLI: $(command -v claude)"
echo "  skill:      $SKILL"

echo "=== 1. building agent-irc ==="
( cd "$CLI_SRC" && go build -o "$BIN" ./cmd/agent-irc )
[[ -x "$BIN" ]] || { echo "FAIL: build did not produce $BIN"; exit 1; }
echo "  ok"

pkill -f "agent-irc daemon" 2>/dev/null || true
rm -f "${XDG_RUNTIME_DIR:-/tmp}/agent-irc/"*.sock 2>/dev/null || true
sleep 0.3

# --- ergo ------------------------------------------------------------------

echo "=== 2. booting Ergo on :17000 ==="
./start-ergo.sh > "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for _ in $(seq 1 50); do
    grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break
    sleep 0.2
done
if ! grep -q "now listening on" "$ERGO_LOG"; then
    echo "FAIL: Ergo did not start"
    tail "$ERGO_LOG"
    exit 1
fi
echo "  Ergo up"

# Start monitor early so we capture every event from the start.
"$BIN" connect localhost:17000 --nick monitor >/dev/null
"$BIN" join    '#agents' --nick monitor >/dev/null
"$BIN" tail    '#agents' --nick monitor --follow --skip-self > "$MONITOR_LOG" 2>&1 &
MONITOR_TAIL_PID=$!

SKILL_TEXT=$(cat "$SKILL")
ALICE_PERSONA=$(cat ./demo/alice.persona)
BOB_PERSONA=$(cat ./demo/bob.persona)
ALLOWED="Bash($BIN *) Bash(sleep *) Bash(cat *) Bash(grep *)"

# ---------------------------------------------------------------------------
# Phase 1: alice connects, posts question, exits. Single-turn invocation
# per the skill's "Headless / one-shot invocations" section.
# ---------------------------------------------------------------------------

echo "=== 3. phase 1: alice posts question (bob offline) ==="
ALICE_PHASE1_PROMPT=$(cat <<EOF
$SKILL_TEXT

---

You are alice on IRC.
- Nick: alice
- Server: localhost:17000
- Channel: #agents
- Peer: bob
- Persona:
$ALICE_PERSONA

This is a single-turn invocation per the skill's "Headless / one-shot
invocations" section. Do **not** arm Monitor; do **not** enter the
always-monitoring loop. Do exactly this and then stop:

1. Connect and join.
2. Optionally check 'agent-irc nicks #agents' just to confirm state.
3. Post one in-character message asking bob to formally specify what
   'sorted' means for a list.
4. Yield with a one-line summary. Do NOT call agent-irc quit — leave
   the daemon running so bob can find your message later.

CLI binary is at $BIN.
EOF
)

printf '%s' "$ALICE_PHASE1_PROMPT" | claude --print --allowed-tools "$ALLOWED" \
  > "$ALICE_PHASE1_LOG" 2>&1
echo "  alice phase 1 exited"
sleep 2

# Quick check: verify alice's message landed in the channel buffer.
SAW_ALICE_Q=$(grep -c '"from":"alice"' "$MONITOR_LOG" || echo 0)
if (( SAW_ALICE_Q < 1 )); then
    echo "FAIL: phase 1 produced no message from alice"
    tail -20 "$ALICE_PHASE1_LOG"
    exit 1
fi
echo "  ✓ alice's question landed in #agents"

# ---------------------------------------------------------------------------
# Phase 2: bob comes online, reads history via tail --history, sees alice's
# question, replies, yields. Alice's brain is offline; alice's daemon is
# still in the channel.
# ---------------------------------------------------------------------------

echo "=== 4. phase 2: bob comes online, pulls alice's question, replies ==="
BOB_PHASE2_PROMPT=$(cat <<EOF
$SKILL_TEXT

---

You are bob on IRC.
- Nick: bob
- Server: localhost:17000
- Channel: #agents
- Peer: alice
- Persona:
$BOB_PERSONA

This is a single-turn invocation per the skill's "Headless / one-shot
invocations" section. Do **not** arm Monitor; do **not** enter the
always-monitoring loop. Do exactly this and then stop:

1. Connect and join.
2. Read recent channel history via tail --history 20 --follow=false.
3. You'll see a question alice asked earlier. Reply in-character with
   one short message.
4. Yield with a one-line summary. Do NOT call agent-irc quit — leave
   the daemon running. Alice's brain isn't here right now, so any
   monitoring would just sit idle; one-turn is the right shape.

CLI binary is at $BIN.
EOF
)

printf '%s' "$BOB_PHASE2_PROMPT" | claude --print --allowed-tools "$ALLOWED" \
  > "$BOB_PHASE2_LOG" 2>&1
echo "  bob phase 2 exited"
sleep 2

SAW_BOB_REPLY=$(grep -c '"from":"bob"' "$MONITOR_LOG" || echo 0)
if (( SAW_BOB_REPLY < 1 )); then
    echo "FAIL: phase 2 produced no message from bob"
    tail -20 "$BOB_PHASE2_LOG"
    exit 1
fi
echo "  ✓ bob's reply landed in #agents"

# ---------------------------------------------------------------------------
# Phase 3: alice's "user" prompts her again. She reads, sees bob's reply,
# responds. Same one-turn shape.
# ---------------------------------------------------------------------------

echo "=== 5. phase 3: alice pulls bob's reply and responds ==="
ALICE_PHASE3_PROMPT=$(cat <<EOF
$SKILL_TEXT

---

You are alice on IRC, returning to a session you started earlier. Your
daemon is still running with your nick; the IRC seat is warm.
- Nick: alice
- Server: localhost:17000
- Channel: #agents
- Peer: bob
- Persona:
$ALICE_PERSONA

This is a single-turn invocation per the skill's "Headless / one-shot
invocations" section. Do **not** arm Monitor; do **not** enter the
always-monitoring loop. Do exactly this and then stop:

1. Read recent channel history via tail --history 20 --follow=false.
2. You'll see bob has replied to your earlier question. Respond
   in-character with one short message.
3. Yield with a one-line summary. Do NOT call agent-irc quit.

CLI binary is at $BIN.
EOF
)

printf '%s' "$ALICE_PHASE3_PROMPT" | claude --print --allowed-tools "$ALLOWED" \
  > "$ALICE_PHASE3_LOG" 2>&1
echo "  alice phase 3 exited"
sleep 2

# ---------------------------------------------------------------------------
# Assertions
# ---------------------------------------------------------------------------

kill "$MONITOR_TAIL_PID" 2>/dev/null
sleep 0.3

echo "=== 6. assertions ==="
EVENTS=$(grep -c '"event":"message"' "$MONITOR_LOG" || echo 0)
ALICE_MSGS=$(grep -E -c '"event":"message".*"from":"alice"' "$MONITOR_LOG" || echo 0)
BOB_MSGS=$(grep -E -c '"event":"message".*"from":"bob"' "$MONITOR_LOG" || echo 0)
echo "  total message events: $EVENTS  (alice: $ALICE_MSGS, bob: $BOB_MSGS)"

if (( EVENTS < 3 )); then
    echo "FAIL: expected ≥3 message events, got $EVENTS"
    echo "  monitor log:"
    sed 's/^/    /' "$MONITOR_LOG"
    exit 1
fi
if (( ALICE_MSGS < 2 )); then
    echo "FAIL: expected ≥2 messages from alice (Q + follow-up), got $ALICE_MSGS"
    exit 1
fi
if (( BOB_MSGS < 1 )); then
    echo "FAIL: expected ≥1 message from bob, got $BOB_MSGS"
    exit 1
fi
echo "  ✓ ≥2 alice messages (question + follow-up), ≥1 bob reply"

echo
echo "=== captured chatter ==="
grep '"event":"message"' "$MONITOR_LOG" | sed 's/^/  /'

echo
echo "PASS: pull-flow E2E — alice and bob exchanged messages asynchronously across three independent claude --print sessions"
