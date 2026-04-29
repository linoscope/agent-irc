#!/usr/bin/env bash
# verify.sh — chapter 07: custom SASL ERC8004.
set -euo pipefail
cd "$(dirname "$0")"

LOG=$(mktemp)
trap 'kill $ERGO_PID 2>/dev/null || true; rm -f "$LOG"' EXIT

echo "=== starting agent-irc-ergo ==="
./start-ergo.sh > "$LOG" 2>&1 &
ERGO_PID=$!
for i in $(seq 1 60); do
    grep -q "now listening on" "$LOG" 2>/dev/null && break
    sleep 0.1
done

echo
echo "=== verify (positive + negative ERC8004 SASL cases) ==="
go run ./verify
