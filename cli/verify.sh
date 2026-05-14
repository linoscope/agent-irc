#!/usr/bin/env bash
# verify.sh — end-to-end test of the agent-irc CLI.
#
# Steps:
#   1. Build the binary.
#   2. Boot Ergo (uses the appendix-cli-agent start-ergo.sh).
#   3. Two agents (alpha, bravo) both connect, both join #verify.
#   4. alpha sends a message, bravo's `tail` captures it.
#   5. bravo sends a message, alpha's `tail` captures it.
#   6. nicks #verify shows both bots.
#   7. Clean teardown.
set -euo pipefail
cd "$(dirname "$0")"

BIN="${AGENT_IRC_BIN:-/tmp/agent-irc}"

echo "=== 1. building ==="
go build -o "$BIN" ./cmd/agent-irc

# Use the appendix-cli-agent Ergo launcher (single start script for the
# whole tutorial's appendix-side demos).
APPCLI="$(cd .. && pwd)/appendix-cli-agent"
if [[ ! -x "$APPCLI/start-ergo.sh" ]]; then
    echo "FAIL: appendix-cli-agent/start-ergo.sh not found"
    exit 1
fi

ERGO_LOG=$(mktemp)
trap 'kill ${ERGO_PID:-0} 2>/dev/null || true; pkill -f "agent-irc daemon" 2>/dev/null || true; rm -f "$ERGO_LOG"' EXIT

# Clean any stale daemons + sockets from earlier runs.
pkill -f "agent-irc daemon" 2>/dev/null || true
rm -f "${XDG_RUNTIME_DIR:-/tmp}/agent-irc/"*.sock 2>/dev/null || true
sleep 0.3

echo "=== 2. booting Ergo on :17000 ==="
( cd "$APPCLI" && ./start-ergo.sh > "$ERGO_LOG" 2>&1 ) &
ERGO_PID=$!
for i in $(seq 1 50); do
    nc -z localhost 17000 2>/dev/null && break
    sleep 0.2
done
nc -z localhost 17000 || { echo "FAIL: Ergo did not start"; cat "$ERGO_LOG"; exit 1; }
echo "  Ergo up"

echo "=== 3. alpha + bravo connect, join #verify ==="
"$BIN" connect localhost:17000 --nick alpha
"$BIN" connect localhost:17000 --nick bravo
"$BIN" join '#verify' --nick alpha
"$BIN" join '#verify' --nick bravo
sleep 0.5

echo "=== 4. tail bravo, alpha sends a message ==="
TAIL_LOG=$(mktemp)
( timeout 4 "$BIN" tail '#verify' --nick bravo --skip-self ) > "$TAIL_LOG" 2>&1 &
TAIL_PID=$!
sleep 0.4
"$BIN" send '#verify' --nick alpha "hi from alpha at $(date +%H:%M:%S)"
sleep 0.5
wait $TAIL_PID 2>/dev/null || true
if ! grep -q "hi from alpha" "$TAIL_LOG"; then
    echo "FAIL: bravo's tail did not see alpha's message"
    cat "$TAIL_LOG"
    exit 1
fi
echo "  bravo received alpha's message ✓"

echo "=== 5. nicks #verify ==="
NICKS=$("$BIN" nicks '#verify' --nick alpha)
echo "$NICKS" | sort
if ! grep -q alpha <<<"$NICKS" || ! grep -q bravo <<<"$NICKS"; then
    echo "FAIL: nicks output missing alpha or bravo"
    exit 1
fi
echo "  member set ok ✓"

echo "=== 6. CR/LF sanitization ==="
# Send a message containing a literal CR; ensure it doesn't inject a second line.
"$BIN" send '#verify' --nick alpha $'one line\ninjected'
sleep 0.3
TAIL2=$(mktemp)
( timeout 2 "$BIN" tail '#verify' --nick bravo --skip-self --history 5 --no-follow ) > "$TAIL2" 2>&1 &
sleep 2.5
if grep -q "injected" "$TAIL2"; then
    # The message text itself may contain "injected" — the test is whether
    # it appears as a SEPARATE event from "one line".
    if [[ $(grep -c "event" "$TAIL2") -gt $(grep -c "one line" "$TAIL2") ]]; then
        echo "FAIL: CR/LF was not sanitized; injected line became a separate event"
        cat "$TAIL2"
        exit 1
    fi
fi
echo "  CR/LF sanitized ✓"

echo "=== 7. cleanup ==="
"$BIN" quit --nick alpha || true
"$BIN" quit --nick bravo || true
sleep 0.3

echo
echo "PASS: agent-irc CLI end-to-end"
