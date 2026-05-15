#!/usr/bin/env bash
# verify.sh — chapter 09: agent-JSON .name (fetched from tokenURI) → IRC nick,
# validated by ValidateIRCName before it's accepted as the account name.
#
# Pipeline:
#   1. start anvil
#   2. deploy AgentRegistry, register 3 test agents (alice-bot, bad-name, long-name)
#   3. start the fork at chapter-erc8004-canonical
#   4. run the Go verify program (3 cases: 1 success, 2 negative)
set -euo pipefail
cd "$(dirname "$0")"

ANVIL_LOG=$(mktemp); ERGO_LOG=$(mktemp)
trap 'kill $ANVIL_PID 2>/dev/null || true; kill $ERGO_PID 2>/dev/null || true; rm -f "$ANVIL_LOG" "$ERGO_LOG"' EXIT

echo "=== starting anvil ==="
./start-anvil.sh > "$ANVIL_LOG" 2>&1 &
ANVIL_PID=$!
for _ in $(seq 1 50); do
    cast client --rpc-url http://localhost:8545 >/dev/null 2>&1 && break
    sleep 0.1
done

echo
echo "=== deploying canonical ERC-8004 registry + registering 3 agents ==="
./deploy.sh

echo
echo "=== starting agent-irc-ergo with ERC-8004 gate ==="
./start-ergo.sh > "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for _ in $(seq 1 80); do
    grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break
    sleep 0.1
done
if ! grep -q "now listening on" "$ERGO_LOG"; then
    echo "FAIL: Ergo did not start"; tail -30 "$ERGO_LOG"; exit 1
fi

echo
echo "=== verify (3 cases: alice-bot → 001, bad name → 904, long name → 904) ==="
go run ./verify
