#!/usr/bin/env bash
# verify.sh — end-to-end check for chapter 10:
#
#   1. boot anvil + deploy spec-compliant ERC-8004 registry + register
#      alice-bot via register(agentURI) with an inlined data: URI;
#   2. start the agent-irc-ergo fork at the chapter-erc8004-canonical tag
#      with AGENT_IRC_WATCHER_INTERVAL=1 (1s poll for fast test feedback);
#   3. run the three-case Go verify:
#        case 1: cross-chain signature rejected (904)
#        case 2: correct binding authenticates (903 → 001 RPL_WELCOME)
#        case 3: setAgentURI on-chain changes .name → watcher KILLs case-2's
#                still-open socket within ~3 polls.
#
# Exits 0 iff all three cases pass.
set -euo pipefail
cd "$(dirname "$0")"

ANVIL_LOG=$(mktemp); ERGO_LOG=$(mktemp)
ANVIL_PID=""; ERGO_PID=""
cleanup() {
    [[ -n "$ERGO_PID"  ]] && kill "$ERGO_PID"  2>/dev/null || true
    [[ -n "$ANVIL_PID" ]] && kill "$ANVIL_PID" 2>/dev/null || true
    sleep 0.2
    rm -f "$ANVIL_LOG" "$ERGO_LOG"
}
trap cleanup EXIT INT TERM

echo "=== 1. starting anvil ==="
./start-anvil.sh > "$ANVIL_LOG" 2>&1 &
ANVIL_PID=$!
for _ in $(seq 1 50); do
    cast client --rpc-url http://localhost:8545 >/dev/null 2>&1 && break
    sleep 0.1
done

echo
echo "=== 2. deploy registry + register alice-bot ==="
./deploy.sh

echo
echo "=== 3. start agent-irc-ergo (watcher poll = 1s) ==="
./start-ergo.sh > "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for _ in $(seq 1 80); do
    grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break
    sleep 0.2
done
if ! grep -q "now listening on" "$ERGO_LOG"; then
    echo "FAIL: Ergo did not start" >&2
    tail -30 "$ERGO_LOG" >&2
    exit 1
fi
grep "agent-irc" "$ERGO_LOG" | head -5 | sed 's/^/  /' || true

echo
echo "=== 4. verify (replay protection + mutation watcher) ==="
GOWORK=off go run ./verify

echo
echo "=== ergo log tail ==="
grep -E "agent-irc|KILL|ERROR" "$ERGO_LOG" | tail -10 || true
