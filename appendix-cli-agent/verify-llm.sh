#!/usr/bin/env bash
# verify-llm.sh — opt-in end-to-end smoke test for the LLM-flavour demo.
#
# Spawns two `claude --print` sessions as alice and bob, each primed with
# skills/irc-participant.md + a persona, and asserts they connect to
# #agents and exchange messages.
#
# NOT run by the default verify.sh because:
#   - it costs API tokens
#   - it depends on the `claude` CLI being on PATH with a working auth
#   - LLM output is non-deterministic
#
# Use this when you want to confirm the skill + claude CLI flow works
# end-to-end. The structural assertion is the same shape as verify.sh:
# ≥N message events captured, both nicks active.
set -uo pipefail
cd "$(dirname "$0")"
REPO_ROOT="$(cd .. && pwd)"

CLI_SRC="../cli"
BIN="${AGENT_IRC_BIN:-/tmp/agent-irc}"
SKILL="$REPO_ROOT/skills/irc-participant.md"
WATCH_SECONDS="${WATCH_SECONDS:-90}"
MIN_EVENTS=3

ERGO_LOG=$(mktemp)
ALICE_LOG=$(mktemp)
BOB_LOG=$(mktemp)
MONITOR_LOG=$(mktemp)
ERGO_PID=""
ALICE_PID=""
BOB_PID=""

cleanup() {
    [[ -n "$ALICE_PID" ]] && kill "$ALICE_PID" 2>/dev/null
    [[ -n "$BOB_PID"   ]] && kill "$BOB_PID"   2>/dev/null
    "$BIN" quit --nick alice   2>/dev/null || true
    "$BIN" quit --nick bob     2>/dev/null || true
    "$BIN" quit --nick monitor 2>/dev/null || true
    pkill -f "agent-irc daemon" 2>/dev/null || true
    [[ -n "$ERGO_PID" ]] && kill "$ERGO_PID" 2>/dev/null
    sleep 0.3
    echo
    echo "=== logs preserved for inspection ==="
    echo "  alice: $ALICE_LOG"
    echo "  bob:   $BOB_LOG"
    echo "  monitor: $MONITOR_LOG"
}
trap cleanup EXIT INT TERM

# --- prereqs ---------------------------------------------------------------

echo "=== 0. checking prerequisites ==="
command -v claude >/dev/null || {
    echo "FAIL: claude CLI not on PATH. Install Claude Code first."
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

# --- spawn claude --print as each agent ------------------------------------

SKILL_TEXT=$(cat "$SKILL")
ALICE_PERSONA=$(cat ./demo/alice.persona)
BOB_PERSONA=$(cat ./demo/bob.persona)

ALICE_PROMPT=$(cat <<EOF
$SKILL_TEXT

---

Run autonomously — there is no human in this session to prompt you.
Follow the skill's default operating model: connect, post an opener,
arm Claude Code's `Monitor` on `agent-irc tail --follow`, then react
to events as they arrive. You have Monitor and TaskStop in your
allowed-tools list.

You are alice on IRC.
- Nick: alice
- Server: localhost:17000
- Channel: #agents
- Peer: bob
- Persona:
$ALICE_PERSONA

Goal: post one brief in-character opener, then ask bob to formally
specify what 'sorted' means for a list. Keep chatting with him until
you reach a natural conclusion on that question (or you hit the
consecutive-send cap of ~10). Then give a two-line summary. Do not
call agent-irc quit — the harness will clean up.

CLI binary is at $BIN.
EOF
)

BOB_PROMPT=$(cat <<EOF
$SKILL_TEXT

---

Run autonomously — there is no human in this session to prompt you.
Follow the skill's default operating model: connect, post an opener,
arm Claude Code's `Monitor` on `agent-irc tail --follow`, then react
to events as they arrive. You have Monitor and TaskStop in your
allowed-tools list.

You are bob on IRC.
- Nick: bob
- Server: localhost:17000
- Channel: #agents
- Peer: alice
- Persona:
$BOB_PERSONA

Goal: post one brief in-character opener, then engage with whatever
alice asks. Stay reactive until the conversation reaches a natural
conclusion (or you hit the consecutive-send cap of ~10). Then give a
two-line summary. Do not call agent-irc quit — the harness will
clean up.

CLI binary is at $BIN.
EOF
)

echo "=== 3. spawning alice + bob via claude --print ==="
# Monitor + TaskStop let the agents use the skill's Claude-Code-specific
# push-notification operating model instead of the polling fallback.
ALLOWED="Bash($BIN *) Bash(sleep *) Bash(cat *) Bash(grep *) Monitor TaskStop"
# Pipe prompts via stdin — passing the skill markdown as a positional arg
# breaks because the YAML frontmatter (---) is parsed as a flag.
printf '%s' "$ALICE_PROMPT" | claude --print --allowed-tools "$ALLOWED" \
  > "$ALICE_LOG" 2>&1 &
ALICE_PID=$!
printf '%s' "$BOB_PROMPT" | claude --print --allowed-tools "$ALLOWED" \
  > "$BOB_LOG"   2>&1 &
BOB_PID=$!

# Give them a moment to start.
sleep 5
if ! kill -0 "$ALICE_PID" 2>/dev/null; then
    echo "FAIL: alice claude session died early"; tail -30 "$ALICE_LOG"; exit 1
fi
if ! kill -0 "$BOB_PID" 2>/dev/null; then
    echo "FAIL: bob claude session died early"; tail -30 "$BOB_LOG"; exit 1
fi
echo "  alice + bob claude sessions running"

# --- monitor ---------------------------------------------------------------

echo "=== 4. monitor connects, tails for ${WATCH_SECONDS}s ==="
"$BIN" connect localhost:17000 --nick monitor >/dev/null
"$BIN" join    '#agents' --nick monitor >/dev/null
timeout "$WATCH_SECONDS" "$BIN" tail '#agents' --nick monitor --follow --skip-self > "$MONITOR_LOG" 2>&1 || true

# Wait for the claude sessions to finish their summaries (or hit timeout).
wait "$ALICE_PID" 2>/dev/null || true
wait "$BOB_PID"   2>/dev/null || true

EVENTS=$(grep -c '"event":"message"' "$MONITOR_LOG" || echo 0)
echo "  captured $EVENTS message events"

# --- assertions ------------------------------------------------------------

echo "=== 5. assertions ==="
if (( EVENTS < MIN_EVENTS )); then
    echo "FAIL: expected ≥$MIN_EVENTS message events, got $EVENTS"
    echo "  alice log tail:"
    tail -20 "$ALICE_LOG" | sed 's/^/    /'
    echo "  bob log tail:"
    tail -20 "$BOB_LOG"   | sed 's/^/    /'
    echo "  monitor log:"
    sed 's/^/    /' "$MONITOR_LOG"
    exit 1
fi
if ! grep -qE '"event":"message".*"from":"alice"' "$MONITOR_LOG"; then
    echo "FAIL: did not see any messages from alice (joins don't count)"
    tail -20 "$ALICE_LOG" | sed 's/^/    /'
    exit 1
fi
if ! grep -qE '"event":"message".*"from":"bob"' "$MONITOR_LOG"; then
    echo "FAIL: did not see any messages from bob (joins don't count)"
    tail -20 "$BOB_LOG" | sed 's/^/    /'
    exit 1
fi
echo "  ≥$MIN_EVENTS message events, both alice and bob spoke ✓"

echo
echo "=== sample of captured chatter ==="
grep '"event":"message"' "$MONITOR_LOG" | head -8 | sed 's/^/  /'

echo
echo "PASS: claude --print + irc-participant skill — LLM agents exchanged messages via the CLI"
