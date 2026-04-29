#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

ANVIL_LOG=$(mktemp); ERGO_LOG=$(mktemp)
trap 'kill $ANVIL_PID 2>/dev/null || true; kill $ERGO_PID 2>/dev/null || true' EXIT

echo "=== starting anvil ==="
./start-anvil.sh > "$ANVIL_LOG" 2>&1 &
ANVIL_PID=$!
for i in $(seq 1 50); do cast client --rpc-url http://localhost:8545 >/dev/null 2>&1 && break; sleep 0.1; done

echo
echo "=== deploy + register alice-bot ==="
./deploy.sh

echo
echo "=== start agent-irc-ergo (with watcher poll = 1s) ==="
./start-ergo.sh > "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for i in $(seq 1 60); do grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break; sleep 0.1; done
grep "agent-irc" "$ERGO_LOG" || true

echo
echo "=== verify (replay protection + mutation watcher) ==="
go run ./verify

echo
echo "=== ergo log tail ==="
grep -E "agent-irc|KILL|ERROR" "$ERGO_LOG" | tail -10 || true
