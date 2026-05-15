#!/usr/bin/env bash
# verify.sh — chapter 08b end-to-end against a local anvil.
#
# Steps:
#   1. boot anvil
#   2. deploy canonical AgentRegistry, register alice-bot, capture agentId
#   3. start agent-irc-ergo (chapter-erc8004-canonical tag) with the gate on
#   4. run the 3-case Go verify program against the live registry
#
# Cleanup: trap kills both background processes and removes temp files
# regardless of how the script exits.
set -uo pipefail
cd "$(dirname "$0")"

ANVIL_LOG=$(mktemp)
ERGO_LOG=$(mktemp)
ANVIL_PID=""
ERGO_PID=""

cleanup() {
    [[ -n "$ERGO_PID"  ]] && kill "$ERGO_PID"  2>/dev/null
    [[ -n "$ANVIL_PID" ]] && kill "$ANVIL_PID" 2>/dev/null
    sleep 0.3
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
if ! cast client --rpc-url http://localhost:8545 >/dev/null 2>&1; then
    echo "FAIL: anvil did not start"
    tail -20 "$ANVIL_LOG"
    exit 1
fi
echo "  ok"

echo
echo "=== 2. deploying AgentRegistry + registering alice-bot ==="
./deploy.sh

echo
echo "=== 3. starting agent-irc-ergo with ERC-8004 gate ==="
./start-ergo.sh > "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for _ in $(seq 1 80); do
    grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break
    sleep 0.3
done
if ! grep -q "now listening on" "$ERGO_LOG"; then
    echo "FAIL: Ergo did not start"
    tail -30 "$ERGO_LOG"
    exit 1
fi
grep -E "agent-irc|listening on" "$ERGO_LOG" | head -5 | sed 's/^/  /'

echo
echo "=== 4. verify (3 SASL cases against the live canonical registry) ==="
go run ./verify
