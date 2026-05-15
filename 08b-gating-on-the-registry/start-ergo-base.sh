#!/usr/bin/env bash
# start-ergo-base.sh — chapter 08b launcher pointed at Base mainnet's
# canonical ERC-8004 Identity Registry (0x8004A169FB4a3325136EB29fA0ceB6D2e539a432).
#
# Used by verify-mainnet.sh to exercise the same SASL gate against a real
# on-chain registry instead of a local anvil. Prerequisite: the agent
# you'll authenticate as must already be registered, and tokenURI(agentId)
# must resolve to JSON whose `.name` is a valid IRC nick.
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_TAG="${ERGO_TAG:-chapter-11}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc-ch08b-base}"
PORT="${PORT:-16674}"
RPC="${RPC:-https://mainnet.base.org}"
REGISTRY_ADDR="${REGISTRY_ADDR:-0x8004A169FB4a3325136EB29fA0ceB6D2e539a432}"
CHAIN_ID="${CHAIN_ID:-8453}"
CACHE_TTL="${CACHE_TTL:-30s}"

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
src = src.replace('type: "* -userinput -useroutput"', 'type: "* userinput useroutput"')
src = re.sub(r'^        level: info$', '        level: debug', src, count=1, flags=re.MULTILINE)
new_block = """
    erc8004:
        rpc-url: "$RPC"
        registry-address: "$REGISTRY_ADDR"
        chain-id: $CHAIN_ID
        cache-ttl: $CACHE_TTL
"""
src = re.sub(r'(\naccounts:\n)', r'\1' + new_block, src)
open(p, "w").write(src)
PY

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
