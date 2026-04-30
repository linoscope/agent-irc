#!/usr/bin/env bash
# verify.sh — chapter 08 end-to-end:
#   1. start anvil
#   2. deploy AgentRegistry, register one agent
#   3. start agent-irc-ergo with the gate enabled
#   4. run 3 SASL cases (positive, not-registered, sig-mismatch)
set -euo pipefail
cd "$(dirname "$0")"

ANVIL_LOG=$(mktemp)
ERGO_LOG=$(mktemp)
trap 'kill $ANVIL_PID 2>/dev/null || true; kill $ERGO_PID 2>/dev/null || true; rm -f "$ANVIL_LOG" "$ERGO_LOG"' EXIT

echo "=== starting anvil ==="
./start-anvil.sh > "$ANVIL_LOG" 2>&1 &
ANVIL_PID=$!
for i in $(seq 1 50); do
    if cast client --rpc-url http://localhost:8545 >/dev/null 2>&1; then break; fi
    sleep 0.1
done

echo
echo "=== deploying AgentRegistry + registering test agent ==="
./deploy.sh

echo
echo "=== starting agent-irc-ergo with ERC-8004 gate ==="
./start-ergo.sh > "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for i in $(seq 1 60); do
    grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break
    sleep 0.1
done
if ! grep -q "ERC-8004 gate enabled" "$ERGO_LOG"; then
    echo "FAIL: gate did not enable. Ergo log:"
    cat "$ERGO_LOG"
    exit 1
fi
grep "ERC-8004 gate" "$ERGO_LOG"

echo
echo "=== verify (3 SASL cases) ==="
go run ./verify
