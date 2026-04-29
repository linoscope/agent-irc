#!/usr/bin/env bash
# start-ergo.sh — chapter 08 launcher with ERC-8004 gate enabled.
#
# Reads the registry address from ./.registry-address (written by deploy.sh).
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc}"
PORT="${PORT:-16674}"
RPC="${RPC:-http://localhost:8545}"
REGISTRY_ADDR=$(cat .registry-address 2>/dev/null || true)
if [[ -z "$REGISTRY_ADDR" ]]; then
    echo "ERROR: ./.registry-address missing — run ./deploy.sh first."
    exit 1
fi

echo ">> building agent-irc-ergo from $ERGO_SRC into $ERGO_BIN"
( cd "$ERGO_SRC" && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data

if [[ ! -f ircd.yaml ]]; then
    cp ../07-custom-sasl-erc8004/ircd.yaml .
fi
sed -i -E "s/\":[0-9]+\":/\":$PORT\":/" ircd.yaml

# Inject (or refresh) the erc8004: block under accounts:.
python3 <<PY
import re
p = "ircd.yaml"
src = open(p).read()
src = re.sub(r'\n    erc8004:.*?(?=\n[a-z]|\Z)', '', src, flags=re.DOTALL)
new_block = """
    erc8004:
        rpc-url: "$RPC"
        registry-address: "$REGISTRY_ADDR"
        chain-id: 31337
        cache-ttl: 0s
"""
src = re.sub(r'(\naccounts:\n)', r'\1' + new_block, src)
open(p, "w").write(src)
PY

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
