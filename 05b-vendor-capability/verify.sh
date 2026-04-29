#!/usr/bin/env bash
# verify.sh — chapter 05b: confirm our vendor capability lands in CAP LS 302
# and is REQ-able. Starts the agent-irc fork (vendor cap), runs the verify, tears down.
set -euo pipefail
cd "$(dirname "$0")"

LOG=$(mktemp)
trap 'kill $ERGO_PID 2>/dev/null || true; rm -f "$LOG"' EXIT

echo "=== starting agent-irc-ergo ==="
./start-ergo.sh > "$LOG" 2>&1 &
ERGO_PID=$!
for i in $(seq 1 60); do
    if grep -q "now listening on" "$LOG" 2>/dev/null; then
        break
    fi
    sleep 0.1
done
if ! grep -q "now listening on" "$LOG" 2>/dev/null; then
    echo "FAIL: server did not start. Log:"
    cat "$LOG"
    exit 1
fi

echo
echo "=== CAP LS 302 → REQ → ACK → 001 ==="
go run ./verify
