#!/usr/bin/env bash
# verify.sh — chapter 09: registry-name → IRC nick (validated).
set -euo pipefail
cd "$(dirname "$0")"

ANVIL_LOG=$(mktemp); ERGO_LOG=$(mktemp)
trap 'kill $ANVIL_PID 2>/dev/null || true; kill $ERGO_PID 2>/dev/null || true; rm -f "$ANVIL_LOG" "$ERGO_LOG"' EXIT

echo "=== starting anvil ==="
./start-anvil.sh > "$ANVIL_LOG" 2>&1 &
ANVIL_PID=$!
for i in $(seq 1 50); do cast client --rpc-url http://localhost:8545 >/dev/null 2>&1 && break; sleep 0.1; done

echo
echo "=== deploying AgentRegistry + registering 2 agents (one valid, one invalid name) ==="
./deploy.sh

echo
echo "=== starting agent-irc-ergo with registry gate ==="
./start-ergo.sh > "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for i in $(seq 1 60); do grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break; sleep 0.1; done

echo
echo "=== verify (2 cases: valid name → 001, invalid name → 904) ==="
go run ./verify
