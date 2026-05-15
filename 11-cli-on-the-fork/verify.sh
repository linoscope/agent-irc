#!/usr/bin/env bash
# verify.sh — end-to-end check for the chapter 11 client-side payoff:
# the agent-irc CLI authenticates two agents to the fork via ERC8004 SASL,
# both hold their on-chain names as IRC nicks, and PRIVMSGs carry verified
# account-tags. Also smoke-tests that an unregistered key is rejected.
#
# Steps:
#   1. Build the CLI.
#   2. Start anvil + deploy registry + register alice-bot + bob-bot.
#   3. Start the fork at chapter-10 tag with ERC8004 gating enabled.
#   4. agent-irc connect for alice-bot AND bob-bot using their wallet keys.
#   5. Both JOIN #agents, send one PRIVMSG each.
#   6. A "monitor" client tails the channel and asserts:
#         - we saw both nicks active
#         - account-tag on both messages matches the on-chain name
#   7. Negative test: an unregistered key fails to connect (904).
#
# Exits 0 iff everything passes.
set -uo pipefail
cd "$(dirname "$0")"

CLI_SRC="../cli"
BIN="${AGENT_IRC_BIN:-/tmp/agent-irc}"
PORT="${PORT:-16678}"
WATCH_SECONDS="${WATCH_SECONDS:-6}"

ANVIL_LOG=$(mktemp); ERGO_LOG=$(mktemp); MONITOR_LOG=$(mktemp)
ANVIL_PID=""; ERGO_PID=""

cleanup() {
    "$BIN" quit --nick alice-bot 2>/dev/null || true
    "$BIN" quit --nick bob-bot   2>/dev/null || true
    "$BIN" quit --nick monitor   2>/dev/null || true
    pkill -f "agent-irc daemon" 2>/dev/null || true
    [[ -n "$ERGO_PID"  ]] && kill "$ERGO_PID"  2>/dev/null
    [[ -n "$ANVIL_PID" ]] && kill "$ANVIL_PID" 2>/dev/null
    sleep 0.3
    rm -f "$ANVIL_LOG" "$ERGO_LOG" "$MONITOR_LOG"
}
trap cleanup EXIT INT TERM

echo "=== 1. building agent-irc ==="
( cd "$CLI_SRC" && go build -o "$BIN" ./cmd/agent-irc )
[[ -x "$BIN" ]] || { echo "FAIL: build did not produce $BIN"; exit 1; }
echo "  ok"

# Tear down any stale daemons + sockets before we boot ours.
pkill -f "agent-irc daemon" 2>/dev/null || true
rm -f "${XDG_RUNTIME_DIR:-/tmp}/agent-irc/"*.sock 2>/dev/null || true
sleep 0.3

echo
echo "=== 2. starting anvil + deploying registry ==="
./start-anvil.sh > "$ANVIL_LOG" 2>&1 &
ANVIL_PID=$!
for _ in $(seq 1 50); do
    cast client --rpc-url http://localhost:8545 >/dev/null 2>&1 && break
    sleep 0.1
done
./deploy.sh

echo
echo "=== 3. starting agent-irc-ergo at the chapter-10 tag ==="
./start-ergo.sh > "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for _ in $(seq 1 80); do
    grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break
    sleep 0.3
done
if ! grep -q "now listening on" "$ERGO_LOG"; then
    echo "FAIL: Ergo did not start"; tail -20 "$ERGO_LOG"; exit 1
fi
grep "agent-irc" "$ERGO_LOG" | head -3 | sed 's/^/  /'

echo
echo "=== 4. connecting alice-bot + bob-bot with ERC8004 SASL ==="
ALICE_ID=$(cat keys/alice-bot.agentid)
BOB_ID=$(cat keys/bob-bot.agentid)
MONITOR_ID=$(cat keys/monitor.agentid)
echo "  on-chain ids: alice=$ALICE_ID, bob=$BOB_ID, monitor=$MONITOR_ID"
"$BIN" connect "localhost:$PORT" --nick alice-bot \
    --erc8004-key keys/alice-bot.key --agent-id "$ALICE_ID" \
    --chain-id 31337 --server-name ergo.test >/dev/null
"$BIN" connect "localhost:$PORT" --nick bob-bot \
    --erc8004-key keys/bob-bot.key --agent-id "$BOB_ID" \
    --chain-id 31337 --server-name ergo.test >/dev/null

# whoami round-trip to confirm both daemons came up.
"$BIN" whoami --nick alice-bot | sed 's/^/  alice: /'
"$BIN" whoami --nick bob-bot   | sed 's/^/  bob:   /'

echo
echo "=== 5. join #agents, send one PRIVMSG each, capture via monitor ==="
"$BIN" join '#agents' --nick alice-bot >/dev/null
"$BIN" join '#agents' --nick bob-bot   >/dev/null

# Use a third registered identity so --skip-self doesn't hide alice's or
# bob's messages from the tail.
"$BIN" connect "localhost:$PORT" --nick monitor \
    --erc8004-key keys/monitor.key --agent-id "$MONITOR_ID" \
    --chain-id 31337 --server-name ergo.test >/dev/null
"$BIN" join '#agents' --nick monitor >/dev/null
timeout "$WATCH_SECONDS" "$BIN" tail '#agents' --nick monitor --follow > "$MONITOR_LOG" 2>&1 &
TAIL_PID=$!

# Slight stagger so both lines land inside the monitor window.
sleep 0.5
"$BIN" send '#agents' 'hello from alice (on-chain id)' --nick alice-bot >/dev/null
sleep 0.3
"$BIN" send '#agents' 'hello from bob (on-chain id)'   --nick bob-bot   >/dev/null

wait "$TAIL_PID" 2>/dev/null || true
EVENTS=$(grep -c '"event":"message"' "$MONITOR_LOG" || true)
echo "  captured $EVENTS message events"

echo
echo "=== 6. assertions ==="
fail=0
if (( EVENTS < 2 )); then
    echo "FAIL: expected ≥2 message events, got $EVENTS"
    cat "$MONITOR_LOG"; fail=1
fi
if ! grep -q '"from":"alice-bot"' "$MONITOR_LOG"; then
    echo "FAIL: did not see alice-bot as message author"; fail=1
fi
if ! grep -q '"from":"bob-bot"' "$MONITOR_LOG"; then
    echo "FAIL: did not see bob-bot as message author"; fail=1
fi
if ! grep -q '"account":"alice-bot"' "$MONITOR_LOG"; then
    echo "FAIL: alice's message missing account-tag from on-chain registry"
    cat "$MONITOR_LOG"; fail=1
fi
if ! grep -q '"account":"bob-bot"' "$MONITOR_LOG"; then
    echo "FAIL: bob's message missing account-tag from on-chain registry"
    cat "$MONITOR_LOG"; fail=1
fi
(( fail )) && exit 1
echo "  ✓ both nicks active and account-tag = on-chain name"

echo
echo "=== 7. negative test: bogus agentId is rejected ==="
# A token id with no on-chain mint should make getAgentWallet revert.
if "$BIN" connect "localhost:$PORT" --nick bogus \
    --erc8004-key keys/alice-bot.key --agent-id 999999999 \
    --chain-id 31337 --server-name ergo.test \
    >/dev/null 2>&1
then
    echo "FAIL: connect with unregistered agentId should have failed"
    "$BIN" quit --nick bogus 2>/dev/null || true
    exit 1
fi
echo "  ✓ unregistered agentId rejected"

echo
echo "=== sample of captured chatter ==="
head -4 "$MONITOR_LOG" | sed 's/^/  /'

echo
echo "PASS: chapter 11 — agent-irc CLI authenticates via ERC8004 against the fork"
