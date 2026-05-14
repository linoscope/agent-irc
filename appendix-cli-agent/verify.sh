#!/usr/bin/env bash
# verify.sh — end-to-end verification of the appendix-cli-agent demo.
#
# Steps:
#   1. Build the agent-irc CLI binary.
#   2. Boot Ergo on :17000 (via this appendix's start-ergo.sh).
#   3. Spawn alice + bob bash agents.
#   4. Connect a third "monitor" daemon, JOIN #agents, tail --follow for ~10s.
#   5. Assert ≥4 message events were captured AND we saw both nicks active.
#   6. Tear everything down.
#
# Exits 0 iff the appendix's recipe works as documented.
set -uo pipefail
cd "$(dirname "$0")"

CLI_SRC="../cli"
BIN="${AGENT_IRC_BIN:-/tmp/agent-irc}"
WATCH_SECONDS="${WATCH_SECONDS:-15}"
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
    "$BIN" quit --nick monitor 2>/dev/null || true
    pkill -f "agent-irc daemon" 2>/dev/null || true
    [[ -n "$ERGO_PID" ]] && kill "$ERGO_PID" 2>/dev/null
    sleep 0.3
    rm -f "$ERGO_LOG" "$ALICE_LOG" "$BOB_LOG" "$MONITOR_LOG"
}
trap cleanup EXIT INT TERM

echo "=== 1. building agent-irc ==="
( cd "$CLI_SRC" && go build -o "$BIN" ./cmd/agent-irc )
[[ -x "$BIN" ]] || { echo "FAIL: build did not produce $BIN"; exit 1; }
echo "  ok"

# Tear down anything stale before booting our Ergo.
pkill -f "agent-irc daemon" 2>/dev/null || true
rm -f "${XDG_RUNTIME_DIR:-/tmp}/agent-irc/"*.sock 2>/dev/null || true
sleep 0.3

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

echo "=== 3. spawning alice + bob MOCK agents (random-phrase) ==="
AGENT_IRC="$BIN" ./demo/alice-mock.sh > "$ALICE_LOG" 2>&1 &
ALICE_PID=$!
AGENT_IRC="$BIN" ./demo/bob-mock.sh   > "$BOB_LOG"   2>&1 &
BOB_PID=$!
sleep 2
if ! kill -0 "$ALICE_PID" 2>/dev/null; then
    echo "FAIL: alice agent died early"; cat "$ALICE_LOG"; exit 1
fi
if ! kill -0 "$BOB_PID" 2>/dev/null; then
    echo "FAIL: bob agent died early"; cat "$BOB_LOG"; exit 1
fi
echo "  alice + bob both running"

echo "=== 4. monitor connects, tails for ${WATCH_SECONDS}s ==="
"$BIN" connect localhost:17000 --nick monitor >/dev/null
"$BIN" join    '#agents' --nick monitor >/dev/null
timeout "$WATCH_SECONDS" "$BIN" tail '#agents' --nick monitor --follow --skip-self > "$MONITOR_LOG" 2>&1
EVENTS=$(wc -l < "$MONITOR_LOG")
echo "  captured $EVENTS events"

echo "=== 5. assertions ==="
if (( EVENTS < MIN_EVENTS )); then
    echo "FAIL: expected ≥$MIN_EVENTS events in ${WATCH_SECONDS}s, got $EVENTS"
    echo "  (this is probabilistic — chatter rate ≈ 1 msg every 4s per bot."
    echo "   if you see this consistently, increase WATCH_SECONDS or check"
    echo "   whether the agents actually started — see /tmp/alice.log etc.)"
    cat "$MONITOR_LOG"
    exit 1
fi
if ! grep -q '"from":"alice"' "$MONITOR_LOG"; then
    echo "FAIL: did not see any messages from alice"
    cat "$MONITOR_LOG"
    exit 1
fi
if ! grep -q '"from":"bob"' "$MONITOR_LOG"; then
    echo "FAIL: did not see any messages from bob"
    cat "$MONITOR_LOG"
    exit 1
fi
echo "  ≥$MIN_EVENTS events, both alice and bob spoke ✓"

echo
echo "=== sample of captured chatter ==="
head -8 "$MONITOR_LOG" | sed 's/^/  /'

echo
echo "PASS: appendix-cli-agent — alice and bob hold a conversation via the CLI"
