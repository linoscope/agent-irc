#!/usr/bin/env bash
# verify.sh — chapter 04: confirm Ergo runs and round-trips PRIVMSG.
#
# Starts Ergo in the background, waits for it to listen, runs the verify
# program, then tears Ergo down.
set -euo pipefail
cd "$(dirname "$0")"

LOG=$(mktemp)
trap 'kill $ERGO_PID 2>/dev/null || true; rm -f "$LOG"' EXIT

echo "=== starting Ergo ==="
./start-ergo.sh > "$LOG" 2>&1 &
ERGO_PID=$!

# Wait for the listener to come up by tailing the log.
for i in $(seq 1 50); do
    if grep -q "now listening on" "$LOG" 2>/dev/null; then
        break
    fi
    sleep 0.1
done

echo "=== running verify (alice + bob, JOIN #room, PRIVMSG) ==="
go run ./verify
