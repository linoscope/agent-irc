#!/usr/bin/env bash
# verify.sh — exits 0 iff chapter 01 works end-to-end.
#
# Spins up the server, sends NICK+USER over a raw TCP socket, asserts that
# the server emits :irc.example 001 alice :... and then exits cleanly.
set -euo pipefail

cd "$(dirname "$0")"

PORT=16667
LOG=$(mktemp)
trap 'kill $SERVER_PID 2>/dev/null || true; rm -f "$LOG"' EXIT

go build -o /tmp/ch01-server .

LISTEN=":$PORT" /tmp/ch01-server > "$LOG" 2>&1 &
SERVER_PID=$!
sleep 0.3

OUT=$(printf 'NICK alice\r\nUSER alice 0 * :Alice the Agent\r\nQUIT\r\n' \
    | nc -q1 localhost "$PORT")

if [[ "$OUT" != *"001 alice :Welcome"* ]]; then
    echo "FAIL: did not see 001 RPL_WELCOME"
    echo "got: $OUT"
    echo "--- server log ---"
    cat "$LOG"
    exit 1
fi

echo "PASS: 001 RPL_WELCOME received"
echo "wire transcript:"
echo "$OUT" | sed 's/^/  </'
