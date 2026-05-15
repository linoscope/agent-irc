#!/usr/bin/env bash
# verify-mainnet.sh — chapter 08b run against Base mainnet's canonical
# ERC-8004 Identity Registry at 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432.
#
# Same SASL surface as verify.sh; only difference is the registry the
# fork queries. The chapter's contract code matches the mainnet deploy
# byte-for-byte (modulo proxy machinery) so this is the proof that the
# `eth_call` path resolves identities that exist on a real chain.
#
# Reads from ../.env:
#   AGENT_PRIVATE_KEY  hex secp256k1 key, no 0x prefix
#   AGENT_ADDRESS      sanity-checked against getAgentWallet
#   AGENT_ID           the agent's uint256 tokenId
#   ERC8004_REGISTRY   defaults to canonical Base address
#   RPC_URL            defaults to https://mainnet.base.org
#   CHAIN_ID           defaults to 8453
#
# Steps:
#   1. preflight against the live registry — sanity-check
#      getAgentWallet(AGENT_ID), tokenURI(AGENT_ID), and the off-chain
#      JSON it points to (must be HTTPS, must have .name).
#   2. start agent-irc-ergo (chapter-erc8004-canonical) pointed at Base
#      mainnet via start-ergo-base.sh.
#   3. run the Go verify program in mainnet mode (cases 1 + 2 only —
#      case 3 needs a guaranteed-unminted tokenId which is unsafe).
set -uo pipefail
cd "$(dirname "$0")"

PORT="${PORT:-16674}"
ERGO_LOG=$(mktemp)
ERGO_PID=""

cleanup() {
    [[ -n "$ERGO_PID" ]] && kill "$ERGO_PID" 2>/dev/null
    sleep 0.3
    rm -f "$ERGO_LOG"
}
trap cleanup EXIT INT TERM

# --- 0. Load funded-agent secrets from ../.env --------------------------
if [[ ! -f ../.env ]]; then
    echo "FAIL: ../.env not found — populate it with AGENT_PRIVATE_KEY + AGENT_ID + AGENT_ADDRESS first." >&2
    exit 1
fi
# shellcheck disable=SC1091
set -a; source ../.env; set +a
: "${AGENT_PRIVATE_KEY:?AGENT_PRIVATE_KEY missing from ../.env}"
: "${AGENT_ID:?AGENT_ID missing from ../.env}"
: "${AGENT_ADDRESS:?AGENT_ADDRESS missing from ../.env}"
: "${ERC8004_REGISTRY:=0x8004A169FB4a3325136EB29fA0ceB6D2e539a432}"
: "${RPC_URL:=https://mainnet.base.org}"
: "${CHAIN_ID:=8453}"

echo "=== 1. preflight against Base mainnet ($RPC_URL) ==="
echo "  registry: $ERC8004_REGISTRY"
echo "  agentId:  $AGENT_ID"
WALLET=$(cast call --rpc-url "$RPC_URL" "$ERC8004_REGISTRY" \
    'getAgentWallet(uint256) (address)' "$AGENT_ID")
URI=$(cast call --rpc-url "$RPC_URL" "$ERC8004_REGISTRY" \
    'tokenURI(uint256) (string)' "$AGENT_ID" | sed 's/^"//; s/"$//')
echo "  getAgentWallet($AGENT_ID) = $WALLET"
echo "  tokenURI($AGENT_ID)       = $URI"
if [[ "${WALLET,,}" != "${AGENT_ADDRESS,,}" ]]; then
    echo "FAIL: getAgentWallet($AGENT_ID) = $WALLET != AGENT_ADDRESS $AGENT_ADDRESS" >&2
    exit 1
fi
echo "  ✓ on-chain wallet matches AGENT_ADDRESS from .env"

JSON=$(curl -fsSL --max-time 8 "$URI" 2>/dev/null || true)
if [[ -z "$JSON" ]]; then
    echo "FAIL: tokenURI($AGENT_ID) → $URI did not resolve (HTTP fetch returned empty)." >&2
    exit 1
fi
NAME=$(echo "$JSON" | jq -r .name)
if [[ -z "$NAME" || "$NAME" == "null" ]]; then
    echo "FAIL: agent JSON at $URI is missing a usable .name field." >&2
    exit 1
fi
echo "  ✓ off-chain JSON .name = $NAME"
if [[ "$NAME" != "lin-test-bot" ]]; then
    echo "  WARN: .name = $NAME (expected lin-test-bot — proceeding anyway)"
fi

echo
echo "=== 2. starting agent-irc-ergo pointed at Base mainnet ==="
export PORT RPC_URL REGISTRY_ADDR CHAIN_ID
REGISTRY_ADDR="$ERC8004_REGISTRY" RPC="$RPC_URL" PORT="$PORT" CHAIN_ID="$CHAIN_ID" \
    ./start-ergo-base.sh > "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for _ in $(seq 1 80); do
    grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break
    sleep 0.3
done
if ! grep -q "now listening on" "$ERGO_LOG"; then
    echo "FAIL: Ergo did not start"
    tail -30 "$ERGO_LOG"
    exit 1
fi
grep -E "agent-irc|listening on" "$ERGO_LOG" | head -5 | sed 's/^/  /'

echo
echo "=== 3. verify against the funded mainnet agent ==="
KEY_TRIM="${AGENT_PRIVATE_KEY#0x}"
PORT="$PORT" \
AGENT_ID="$AGENT_ID" \
AGENT_PRIVATE_KEY="$KEY_TRIM" \
CHAIN_ID="$CHAIN_ID" \
SERVER_NAME="ergo.test" \
MODE="mainnet" \
    go run ./verify
