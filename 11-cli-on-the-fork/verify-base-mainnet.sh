#!/usr/bin/env bash
# verify-base-mainnet.sh — end-to-end test against the canonical
# ERC-8004 registry on Base mainnet, using the funded agent in ../.env.
#
# Prerequisites:
#   1. ../.env populated with AGENT_PRIVATE_KEY + AGENT_ADDRESS + AGENT_ID
#      (the funded + registered agent on Base mainnet).
#   2. tokenURI(AGENT_ID) must resolve over HTTPS to JSON with a valid
#      `.name` field — i.e., the linked agent.json must be on the public
#      internet (e.g. committed + pushed to GitHub raw).
#   3. The chapter-10-tagged agent-irc-ergo fork buildable from
#      ~/workspace/agent-irc-ergo.
#
# Runs the fork pointed at Base mainnet, connects via the CLI with
# `--erc8004-key` + `--agent-id`, asserts that:
#   - The IRC nick assigned to the session matches the agent JSON's `.name`.
#   - PRIVMSGs carry an `account` tag matching that name.
set -uo pipefail
cd "$(dirname "$0")"

CLI_SRC="../cli"
BIN="${AGENT_IRC_BIN:-/tmp/agent-irc}"
PORT="${PORT:-16678}"
WATCH_SECONDS="${WATCH_SECONDS:-8}"

# Load the funded agent's secrets.
if [[ ! -f ../.env ]]; then
    echo "FAIL: ../.env not found — generate one with deploy/onboarding flow first." >&2
    exit 1
fi
# shellcheck disable=SC1091
set -a; source ../.env; set +a
: "${AGENT_PRIVATE_KEY:?AGENT_PRIVATE_KEY missing from ../.env}"
: "${AGENT_ADDRESS:?AGENT_ADDRESS missing from ../.env}"
: "${AGENT_ID:?AGENT_ID missing from ../.env}"
: "${CHAIN_ID:=8453}"

KEY_FILE=$(mktemp)
chmod 600 "$KEY_FILE"
printf '%s\n' "$AGENT_PRIVATE_KEY" > "$KEY_FILE"

ERGO_LOG=$(mktemp); MONITOR_LOG=$(mktemp); ERGO_PID=""
cleanup() {
    "$BIN" quit --nick "$AGENT_NICK" 2>/dev/null || true
    pkill -f "agent-irc daemon" 2>/dev/null || true
    [[ -n "$ERGO_PID" ]] && kill "$ERGO_PID" 2>/dev/null
    rm -f "$KEY_FILE" "$ERGO_LOG" "$MONITOR_LOG"
}
trap cleanup EXIT INT TERM

echo "=== 1. building agent-irc CLI ==="
( cd "$CLI_SRC" && go build -o "$BIN" ./cmd/agent-irc )
echo "  ok ($BIN)"

# Tear down any stale daemons + sockets before we boot ours.
pkill -f "agent-irc daemon" 2>/dev/null || true
rm -f "${XDG_RUNTIME_DIR:-/tmp}/agent-irc/"*.sock 2>/dev/null || true
sleep 0.3

echo
echo "=== 2. resolving on-chain agent (agentId=$AGENT_ID) ==="
REGISTRY="${ERC8004_REGISTRY:-0x8004A169FB4a3325136EB29fA0ceB6D2e539a432}"
OWNER=$(cast call --rpc-url https://mainnet.base.org "$REGISTRY" 'ownerOf(uint256) (address)' "$AGENT_ID")
WALLET=$(cast call --rpc-url https://mainnet.base.org "$REGISTRY" 'getAgentWallet(uint256) (address)' "$AGENT_ID")
URI=$(cast call --rpc-url https://mainnet.base.org "$REGISTRY" 'tokenURI(uint256) (string)' "$AGENT_ID" | sed 's/^"//; s/"$//')
echo "  owner:    $OWNER"
echo "  wallet:   $WALLET"
echo "  tokenURI: $URI"
[[ "${WALLET,,}" == "${AGENT_ADDRESS,,}" ]] || { echo "FAIL: AGENT_ADDRESS does not match getAgentWallet($AGENT_ID)" >&2; exit 1; }

JSON=$(curl -fsSL --max-time 8 "$URI" 2>/dev/null || true)
if [[ -z "$JSON" ]]; then
    echo "FAIL: tokenURI($AGENT_ID) → $URI does not resolve. Have you pushed agent.json to the public URL? (404 expected if working tree is unpushed.)" >&2
    exit 1
fi
AGENT_NICK=$(echo "$JSON" | jq -r .name)
[[ -n "$AGENT_NICK" && "$AGENT_NICK" != "null" ]] || { echo "FAIL: agent JSON has no .name field" >&2; exit 1; }
echo "  agent JSON .name = $AGENT_NICK"

echo
echo "=== 3. starting agent-irc-ergo fork against Base mainnet ==="
./start-ergo-base.sh > "$ERGO_LOG" 2>&1 &
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
echo "=== 4. connecting via ERC8004 SASL (agentId=$AGENT_ID, chain=$CHAIN_ID) ==="
"$BIN" connect "localhost:$PORT" --nick "$AGENT_NICK" \
    --erc8004-key "$KEY_FILE" --agent-id "$AGENT_ID" \
    --chain-id "$CHAIN_ID" --server-name ergo.test >/dev/null

"$BIN" whoami --nick "$AGENT_NICK" | sed 's/^/  /'

echo
echo "=== 5. join #agents, send a PRIVMSG, observe account-tag ==="
"$BIN" join '#agents' --nick "$AGENT_NICK" >/dev/null

# Single-agent test: we send to ourselves and rely on the IRCv3 echo-message
# capability to see our own line back. Disable --skip-self so the tail
# subscriber doesn't filter the echo.
timeout "$WATCH_SECONDS" "$BIN" tail '#agents' --nick "$AGENT_NICK" --follow --skip-self=false > "$MONITOR_LOG" 2>&1 &
TAIL_PID=$!
sleep 1
"$BIN" send '#agents' "hello from $AGENT_NICK on Base mainnet" --nick "$AGENT_NICK" >/dev/null
wait "$TAIL_PID" 2>/dev/null || true

echo
echo "=== 6. assertions ==="
fail=0
grep -q "\"from\":\"$AGENT_NICK\"" "$MONITOR_LOG" || { echo "FAIL: PRIVMSG not echoed back as $AGENT_NICK"; fail=1; }
grep -q "\"account\":\"$AGENT_NICK\"" "$MONITOR_LOG" || { echo "FAIL: server did not stamp account=$AGENT_NICK on the PRIVMSG"; fail=1; }
(( fail )) && { cat "$MONITOR_LOG"; exit 1; }
echo "  ✓ nick = $AGENT_NICK (JSON .name pulled from tokenURI($AGENT_ID))"
echo "  ✓ PRIVMSG carries server-stamped account = $AGENT_NICK"

echo
echo "=== captured ==="
head -4 "$MONITOR_LOG" | sed 's/^/  /'

echo
echo "PASS: chapter 11 — agent-irc CLI authenticates via canonical ERC-8004 on Base mainnet (agentId=$AGENT_ID)"
