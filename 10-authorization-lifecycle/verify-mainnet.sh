#!/usr/bin/env bash
# verify-mainnet.sh — chapter 10 against a Base-mainnet-pointed fork.
#
# Runs cases 1 and 2 of the chapter-10 verify against the agent-irc-ergo
# fork pointed at Base mainnet's canonical ERC-8004 Identity Registry:
#
#   case 1 — cross-chain replay rejected
#   case 2 — happy path SASL succeeds
#
# Case 3 (JSON .name change KILLs the session) is intentionally NOT run
# against mainnet: it would require an on-chain setAgentURI to the
# production agent record, costing gas and destructively mutating the
# very identity we're testing with. To exercise case 3 against real
# infrastructure, use a separate "burner" agent registered for that
# purpose; we don't do that automatically.
#
# Prerequisites (see ../.env, populated during onboarding):
#   AGENT_PRIVATE_KEY  funded EOA holding the registered NFT
#   AGENT_ADDRESS      that EOA's address (== getAgentWallet(AGENT_ID))
#   AGENT_ID           the agent's ERC-721 token id in the registry
#   ERC8004_REGISTRY   registry address (defaults to canonical 0x8004A1…)
#   CHAIN_ID           chain id for the body binding (default 8453)
#   RPC_URL            HTTPS RPC (default https://mainnet.base.org)
#   AGENT_URI          the agent.json URL that tokenURI must already resolve to
#
# Pre-flight: tokenURI(AGENT_ID) must HTTP-resolve to JSON with `.name`.
# If you haven't pushed agent.json to the public internet yet the test
# bails early with a hint.
set -euo pipefail
cd "$(dirname "$0")"

PORT="${PORT:-16676}"

if [[ ! -f ../.env ]]; then
    echo "FAIL: ../.env not found — generate one with onboarding first." >&2
    exit 1
fi
# shellcheck disable=SC1091
set -a; source ../.env; set +a
: "${AGENT_PRIVATE_KEY:?AGENT_PRIVATE_KEY missing from ../.env}"
: "${AGENT_ADDRESS:?AGENT_ADDRESS missing from ../.env}"
: "${AGENT_ID:?AGENT_ID missing from ../.env}"
: "${CHAIN_ID:=8453}"
: "${RPC_URL:=https://mainnet.base.org}"
: "${ERC8004_REGISTRY:=0x8004A169FB4a3325136EB29fA0ceB6D2e539a432}"

ERGO_LOG=$(mktemp); ERGO_PID=""
cleanup() {
    [[ -n "$ERGO_PID" ]] && kill "$ERGO_PID" 2>/dev/null || true
    sleep 0.2
    rm -f "$ERGO_LOG"
}
trap cleanup EXIT INT TERM

echo "=== 1. pre-flight: tokenURI($AGENT_ID) must resolve ==="
URI=$(cast call --rpc-url "$RPC_URL" "$ERC8004_REGISTRY" \
        'tokenURI(uint256) (string)' "$AGENT_ID" | sed 's/^"//; s/"$//')
WALLET=$(cast call --rpc-url "$RPC_URL" "$ERC8004_REGISTRY" \
        'getAgentWallet(uint256) (address)' "$AGENT_ID")
echo "  tokenURI:        $URI"
echo "  getAgentWallet:  $WALLET"

if [[ "${WALLET,,}" != "${AGENT_ADDRESS,,}" ]]; then
    echo "FAIL: getAgentWallet($AGENT_ID) = $WALLET, expected $AGENT_ADDRESS" >&2
    exit 1
fi

JSON=$(curl -fsSL --max-time 10 "$URI" 2>/dev/null || true)
if [[ -z "$JSON" ]]; then
    echo "FAIL: tokenURI did not HTTP-resolve. Push agent.json public first." >&2
    exit 1
fi
NAME=$(echo "$JSON" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))")
if [[ -z "$NAME" ]]; then
    echo "FAIL: agent JSON has no .name field" >&2
    echo "$JSON" >&2
    exit 1
fi
echo "  agent JSON .name: $NAME"

# verify/main.go reads `.alice-agentid` + `.registry-address` and signs as
# alice's anvil key. For mainnet we override all three via env so the same
# binary works against either deployment.
echo "$ERC8004_REGISTRY" > .registry-address
echo "$AGENT_ID"         > .alice-agentid
# Strip optional 0x prefix for the verify program's HexToECDSA.
ALICE_KEY_HEX="${AGENT_PRIVATE_KEY#0x}"

echo
echo "=== 2. start agent-irc-ergo against Base mainnet (port :$PORT) ==="
# Mirror start-ergo.sh but point at mainnet. Watcher poll stays at default
# (30s) for mainnet to avoid hammering the RPC.
ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_TAG="${ERGO_TAG:-chapter-erc8004-canonical}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc-ch10-base}"

echo ">> checking out $ERGO_TAG in $ERGO_SRC and building into $ERGO_BIN"
( cd "$ERGO_SRC" \
  && git -c advice.detachedHead=false checkout "$ERGO_TAG" >/dev/null 2>&1 \
  && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data

"$ERGO_BIN" defaultconfig > ircd.yaml
python3 <<PY
import re
p = "ircd.yaml"
src = open(p).read()
src = re.sub(
    r'    listeners:.*?(?=\n    unix-bind-mode|\n    tor-listeners)',
    f'    listeners:\n        ":$PORT":\n',
    src, count=1, flags=re.DOTALL,
)
src = src.replace("    enabled: true\n\n    # default language", "    enabled: false\n\n    # default language")
src = src.replace("path: ircd.db", "path: data/ircd.db")
src = src.replace('lock-file: "ircd.lock"', 'lock-file: "data/ircd.lock"')
new_block = """
    erc8004:
        rpc-url: "$RPC_URL"
        registry-address: "$ERC8004_REGISTRY"
        chain-id: $CHAIN_ID
        cache-ttl: 30s
"""
src = re.sub(r'(\naccounts:\n)', r'\1' + new_block, src)
open(p, "w").write(src)
PY

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
"$ERGO_BIN" run --conf ircd.yaml > "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for _ in $(seq 1 80); do
    grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break
    sleep 0.3
done
if ! grep -q "now listening on" "$ERGO_LOG"; then
    echo "FAIL: Ergo did not start" >&2
    tail -30 "$ERGO_LOG" >&2
    exit 1
fi
grep "agent-irc" "$ERGO_LOG" | head -5 | sed 's/^/  /' || true

echo
echo "=== 3. run verify cases 1 + 2 only (case 3 destructive on mainnet) ==="
# Override the signing key + chain id the verify binary bakes in. We need
# a small wrapper since verify/main.go's constants are compile-time. The
# clean fix is to make them env-overridable; here we shell out a one-off
# build with -ldflags injection.
GOWORK=off go run \
    -ldflags "-X main.aliceKey=$ALICE_KEY_HEX -X main.chainIDStr=$CHAIN_ID -X main.rpcURL=$RPC_URL -X main.skipCase3=1" \
    ./verify || {
    rc=$?
    echo
    echo "=== ergo log tail ==="
    tail -20 "$ERGO_LOG"
    exit $rc
}

echo
echo "=== ergo log tail ==="
grep -E "agent-irc" "$ERGO_LOG" | tail -10 || true

echo
echo "PASS: chapter 10 (mainnet) — replay protection + happy-path SASL against canonical registry"
echo "      case 3 (mutation KILL) NOT run — would mutate the production agent record."
